package orders

import (
	"context"
	"crypto/hmac"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/crypto"
	"github.com/helloghc/apexx/internal/store"
)

// Download is the buyer's access to one file of a digital product.
type Download struct {
	ID           string    `json:"id"`
	FileID       string    `json:"fileId"`
	SKU          string    `json:"sku"`
	Filename     string    `json:"filename"`
	Size         int64     `json:"size"`
	Downloads    int       `json:"downloads"`
	MaxDownloads int       `json:"maxDownloads"`
	ExpiresAt    time.Time `json:"expiresAt"`
	Revoked      bool      `json:"revoked,omitempty"`
	URL          string    `json:"url,omitempty"` // signed; empty when it can no longer be used
}

// DownloadFile is what the download link serves.
type DownloadFile struct {
	Filename, ContentType, StorageKey string
	Size                              int64
}

// Download errors, each with its own message for the buyer.
var (
	ErrDownloadInvalid = errors.New("enlace de descarga no válido")
	ErrDownloadRevoked = errors.New("este enlace ya no está activo: el pedido fue cancelado o reembolsado")
	ErrDownloadExpired = errors.New("el enlace venció")
	ErrDownloadLimit   = errors.New("se alcanzó el límite de descargas")
)

// DownloadSig signs a grant id; the link is /s/{tenant}/api/download/{id}.{sig}.
func DownloadSig(keys *crypto.Keyring, tenant, id string) string {
	b, err := keys.Blind(tenant, "download-signing", id)
	if err != nil {
		return ""
	}
	return hex.EncodeToString(b[:16])
}

// DownloadPath is the signed path of a grant.
func DownloadPath(keys *crypto.Keyring, tenant, id string) string {
	return "/s/" + tenant + "/api/download/" + id + "." + DownloadSig(keys, tenant, id)
}

func (s *Service) downloadsTx(ctx context.Context, tx pgx.Tx, tenant, orderID string) ([]Download, error) {
	rows, err := tx.Query(ctx, `SELECT g.id, g.file_id, COALESCE(f.sku,''), COALESCE(f.filename,''), COALESCE(f.size,0), g.downloads, g.max_downloads, g.expires_at,
			g.revoked_at IS NOT NULL OR f.id IS NULL
		FROM download_grants g LEFT JOIN digital_files f ON f.tenant_id=g.tenant_id AND f.id=g.file_id
		WHERE g.tenant_id=$1 AND g.order_id=$2 ORDER BY g.created_at, f.filename`, tenant, orderID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Download, error) {
		var d Download
		return d, r.Scan(&d.ID, &d.FileID, &d.SKU, &d.Filename, &d.Size, &d.Downloads, &d.MaxDownloads, &d.ExpiresAt, &d.Revoked)
	})
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for i := range out {
		if !out[i].Revoked && out[i].Downloads < out[i].MaxDownloads && now.Before(out[i].ExpiresAt) {
			out[i].URL = DownloadPath(s.Keys, tenant, out[i].ID)
		}
	}
	return out, nil
}

// Downloads lists the download grants of an order.
func (s *Service) Downloads(ctx context.Context, tenant, orderID string) ([]Download, error) {
	var out []Download
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		out, err = s.downloadsTx(ctx, tx, tenant, orderID)
		return err
	})
	return out, err
}

// OpenDownload checks a signed link and counts the download atomically: the
// row lock makes two simultaneous clicks on the last download count once.
// count=false checks without counting (resumed downloads of the same file).
func (s *Service) OpenDownload(ctx context.Context, tenant, token string, count bool) (DownloadFile, error) {
	id, sig, ok := strings.Cut(token, ".")
	if !ok || !strings.HasPrefix(id, "dlg_") || len(id) > 40 || !hmac.Equal([]byte(sig), []byte(DownloadSig(s.Keys, tenant, id))) {
		return DownloadFile{}, ErrDownloadInvalid
	}
	var f DownloadFile
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var downloads, max int
		var expires time.Time
		var revoked bool
		err := tx.QueryRow(ctx, `SELECT g.downloads, g.max_downloads, g.expires_at, g.revoked_at IS NOT NULL, f.filename, f.content_type, f.storage_key, f.size
			FROM download_grants g JOIN digital_files f ON f.tenant_id=g.tenant_id AND f.id=g.file_id
			WHERE g.tenant_id=$1 AND g.id=$2 FOR UPDATE OF g`, tenant, id).Scan(&downloads, &max, &expires, &revoked, &f.Filename, &f.ContentType, &f.StorageKey, &f.Size)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrDownloadRevoked // the file was removed from the product
		case err != nil:
			return err
		case revoked:
			return ErrDownloadRevoked
		case time.Now().After(expires):
			return ErrDownloadExpired
		case downloads >= max:
			return ErrDownloadLimit
		}
		if !count {
			return nil
		}
		_, err = tx.Exec(ctx, `UPDATE download_grants SET downloads=downloads+1, last_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, id)
		return err
	})
	return f, err
}

// ResetDownload gives a grant its downloads back and a new expiry (support:
// "my link expired"). A revoked grant stays revoked.
func (s *Service) ResetDownload(ctx context.Context, tenant, actor, orderID, grantID string) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `UPDATE download_grants g SET downloads=0, expires_at=now() + make_interval(days => f.link_days)
			FROM digital_files f WHERE g.tenant_id=$1 AND g.order_id=$2 AND g.id=$3 AND g.revoked_at IS NULL AND f.tenant_id=$1 AND f.id=g.file_id`, tenant, orderID, grantID)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		var st string
		if err := tx.QueryRow(ctx, `SELECT status FROM orders WHERE tenant_id=$1 AND id=$2`, tenant, orderID).Scan(&st); err != nil {
			return err
		}
		return s.event(ctx, tx, tenant, orderID, actor, "download.reset", &st, st, map[string]any{"grant": grantID})
	})
}

// DigitalFile is a file attached to a SKU, delivered after payment.
type DigitalFile struct {
	ID           string    `json:"id"`
	SKU          string    `json:"sku"`
	Filename     string    `json:"filename"`
	ContentType  string    `json:"contentType"`
	Size         int64     `json:"size"`
	MaxDownloads int       `json:"maxDownloads"`
	LinkDays     int       `json:"linkDays"`
	CreatedAt    time.Time `json:"createdAt"`
	StorageKey   string    `json:"-"`
}

const digitalCols = `id, sku, filename, content_type, size, max_downloads, link_days, created_at, storage_key`

func scanDigital(r pgx.CollectableRow) (DigitalFile, error) {
	var f DigitalFile
	return f, r.Scan(&f.ID, &f.SKU, &f.Filename, &f.ContentType, &f.Size, &f.MaxDownloads, &f.LinkDays, &f.CreatedAt, &f.StorageKey)
}

// DigitalFiles lists the files of a SKU ("" = every SKU).
func (s *Service) DigitalFiles(ctx context.Context, tenant, sku string) ([]DigitalFile, error) {
	var out []DigitalFile
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+digitalCols+` FROM digital_files WHERE tenant_id=$1 AND ($2='' OR sku=$2) ORDER BY sku, created_at`, tenant, sku)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, scanDigital)
		return err
	})
	return out, err
}

// AddDigitalFile records a stored file; at most 20 per SKU.
func (s *Service) AddDigitalFile(ctx context.Context, tenant, actor string, f DigitalFile, sum []byte) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM digital_files WHERE tenant_id=$1 AND sku=$2`, tenant, f.SKU).Scan(&n); err != nil {
			return err
		}
		if n >= 20 {
			return FieldError{"file", "Un producto tiene hasta 20 archivos"}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO digital_files (tenant_id, id, sku, filename, content_type, size, sha256, storage_key, max_downloads, link_days)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, tenant, f.ID, f.SKU, f.Filename, f.ContentType, f.Size, sum, f.StorageKey, f.MaxDownloads, f.LinkDays); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "digital.added", f.SKU, map[string]any{"file": f.ID, "name": f.Filename, "size": f.Size})
	})
}

// UpdateDigitalFile changes the name and limits of a file. Links already
// given keep their own limits.
func (s *Service) UpdateDigitalFile(ctx context.Context, tenant, actor, sku, id string, name string, maxDownloads, linkDays int) (DigitalFile, error) {
	var f DigitalFile
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE digital_files SET filename=$4, max_downloads=$5, link_days=$6 WHERE tenant_id=$1 AND sku=$2 AND id=$3 RETURNING `+digitalCols,
			tenant, sku, id, name, maxDownloads, linkDays)
		if err != nil {
			return err
		}
		if f, err = pgx.CollectExactlyOneRow(rows, scanDigital); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "digital.updated", sku, map[string]any{"file": id})
	})
	return f, err
}

// DeleteDigitalFile removes a file: the links already given stop working.
// It returns the storage key so the caller can delete the bytes.
func (s *Service) DeleteDigitalFile(ctx context.Context, tenant, actor, sku, id string) (string, error) {
	var key string
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `DELETE FROM digital_files WHERE tenant_id=$1 AND sku=$2 AND id=$3 RETURNING storage_key`, tenant, sku, id).Scan(&key)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE download_grants SET revoked_at=now() WHERE tenant_id=$1 AND file_id=$2 AND revoked_at IS NULL`, tenant, id); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "digital.deleted", sku, map[string]any{"file": id})
	})
	return key, err
}

// PreorderBalances lists the paid orders of a preorder SKU that still owe
// their balance.
func (s *Service) PreorderBalances(ctx context.Context, tenant, sku string) ([]string, error) {
	var ids []string
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT o.id FROM orders o JOIN order_lines l ON l.tenant_id=o.tenant_id AND l.order_id=o.id
			WHERE o.tenant_id=$1 AND l.sku=$2 AND l.preorder_ships_on IS NOT NULL AND o.deferred > 0
			  AND o.status IN ('paid','fulfilled') AND o.payment_status='balance_due' ORDER BY o.id`, tenant, sku)
		if err != nil {
			return err
		}
		ids, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	return ids, err
}
