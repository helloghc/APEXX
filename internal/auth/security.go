package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- RFC 6238 authenticator apps use HMAC-SHA1
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/store"
)

// Errors of two-step verification and invitations.
var (
	ErrTOTPRequired = errors.New("auth: verification code required")
	ErrBadCode      = errors.New("auth: wrong verification code")
	ErrTOTPState    = errors.New("auth: two-step verification is not in that state")
	ErrInvitation   = errors.New("auth: invitation is not valid")
	ErrExists       = errors.New("auth: already a member")
)

// Cipher encrypts the secrets of two-step verification with the store key.
type Cipher interface {
	Encrypt(tenant string, plain []byte, aad string) ([]byte, error)
	Decrypt(tenant string, enc []byte, aad string) ([]byte, error)
}

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// totpCode is RFC 6238: HMAC-SHA1, 30 second steps, 6 digits.
func totpCode(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step)) // #nosec G115 -- step is a positive time counter
	h := hmac.New(sha1.New, secret)
	h.Write(msg[:])
	sum := h.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1_000_000)
}

// matchTOTP returns the step that code is valid for (now ± 1 step), or 0.
func matchTOTP(secret []byte, code string, now time.Time) int64 {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return 0
	}
	cur := now.Unix() / 30
	for _, st := range []int64{cur, cur - 1, cur + 1} {
		if subtle.ConstantTimeCompare([]byte(totpCode(secret, st)), []byte(code)) == 1 {
			return st
		}
	}
	return 0
}

func hashCode(c string) string {
	sum := sha256.Sum256([]byte(strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(c), "-", ""))))
	return hex.EncodeToString(sum[:])
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// TOTPBegin creates a new secret for the member (not active until confirmed)
// and returns it with the otpauth:// URI for the authenticator app.
func (s *Service) TOTPBegin(ctx context.Context, tenant, memberID, issuer string) (secret, uri string, err error) {
	if s.Keys == nil {
		return "", "", errors.New("auth: two-step verification is not configured")
	}
	raw := make([]byte, 20)
	if _, err = rand.Read(raw); err != nil {
		return
	}
	enc, err := s.Keys.Encrypt(tenant, raw, "totp:"+memberID)
	if err != nil {
		return
	}
	var email string
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var enabled *time.Time
		if err := tx.QueryRow(ctx, `SELECT email, totp_enabled_at FROM members WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, memberID).Scan(&email, &enabled); err != nil {
			return ErrNotFound
		}
		if enabled != nil {
			return ErrTOTPState
		}
		_, err := tx.Exec(ctx, `UPDATE members SET totp_secret=$3 WHERE tenant_id=$1 AND id=$2`, tenant, memberID, enc)
		return err
	})
	if err != nil {
		return "", "", err
	}
	secret = b32.EncodeToString(raw)
	if issuer == "" {
		issuer = "Apexx"
	}
	uri = "otpauth://totp/" + url.PathEscape(issuer+":"+email) + "?secret=" + secret + "&issuer=" + url.QueryEscape(issuer) + "&algorithm=SHA1&digits=6&period=30"
	return secret, uri, nil
}

// TOTPConfirm activates two-step verification after the member proves the app
// works, and returns one-time recovery codes (shown only now).
func (s *Service) TOTPConfirm(ctx context.Context, tenant, memberID, code string) ([]string, error) {
	var codes []string
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var enc []byte
		var enabled *time.Time
		if err := tx.QueryRow(ctx, `SELECT totp_secret, totp_enabled_at FROM members WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, memberID).Scan(&enc, &enabled); err != nil {
			return ErrNotFound
		}
		if enabled != nil || enc == nil || s.Keys == nil {
			return ErrTOTPState
		}
		secret, err := s.Keys.Decrypt(tenant, enc, "totp:"+memberID)
		if err != nil {
			return err
		}
		step := matchTOTP(secret, code, s.now())
		if step == 0 {
			return ErrBadCode
		}
		hashes := make([]string, 0, 8)
		for i := 0; i < 8; i++ {
			b := make([]byte, 5)
			_, _ = rand.Read(b)
			c := b32.EncodeToString(b)
			c = c[:4] + "-" + c[4:]
			codes = append(codes, c)
			hashes = append(hashes, hashCode(c))
		}
		hj, _ := json.Marshal(hashes)
		if _, err := tx.Exec(ctx, `UPDATE members SET totp_enabled_at=now(), totp_last_step=$3, recovery_codes=$4 WHERE tenant_id=$1 AND id=$2`, tenant, memberID, step, hj); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, "member:"+memberID, "member.2fa_enabled", memberID, map[string]any{})
	})
	return codes, err
}

// TOTPDisable turns two-step verification off. The member confirms with the
// password; an administrator resetting someone else passes checkPassword=false.
func (s *Service) TOTPDisable(ctx context.Context, tenant, actor, memberID, password string, checkPassword bool) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var hash string
		var enabled *time.Time
		if err := tx.QueryRow(ctx, `SELECT password_hash, totp_enabled_at FROM members WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, memberID).Scan(&hash, &enabled); err != nil {
			return ErrNotFound
		}
		if checkPassword && !CheckPassword(hash, password) {
			return ErrBadCredentials
		}
		if enabled == nil {
			return ErrTOTPState
		}
		if _, err := tx.Exec(ctx, `UPDATE members SET totp_secret=NULL, totp_enabled_at=NULL, totp_last_step=0, recovery_codes='[]' WHERE tenant_id=$1 AND id=$2`, tenant, memberID); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "member.2fa_disabled", memberID, map[string]any{"byAdmin": !checkPassword})
	})
}

// checkSecondStep verifies the code of a login. A recovery code works once.
func (s *Service) checkSecondStep(ctx context.Context, tenant, memberID, code string) error {
	if strings.TrimSpace(code) == "" {
		return ErrTOTPRequired
	}
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var enc, rj []byte
		var last int64
		if err := tx.QueryRow(ctx, `SELECT totp_secret, totp_last_step, recovery_codes FROM members WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, memberID).Scan(&enc, &last, &rj); err != nil {
			return err
		}
		if s.Keys == nil {
			return errors.New("auth: two-step verification is not configured")
		}
		secret, err := s.Keys.Decrypt(tenant, enc, "totp:"+memberID)
		if err != nil {
			return err
		}
		if step := matchTOTP(secret, code, s.now()); step > last {
			_, err := tx.Exec(ctx, `UPDATE members SET totp_last_step=$3 WHERE tenant_id=$1 AND id=$2`, tenant, memberID, step)
			return err
		}
		var hashes []string
		_ = json.Unmarshal(rj, &hashes)
		h := hashCode(code)
		for i, x := range hashes {
			if subtle.ConstantTimeCompare([]byte(x), []byte(h)) == 1 {
				hashes = append(hashes[:i], hashes[i+1:]...)
				hj, _ := json.Marshal(hashes)
				if _, err := tx.Exec(ctx, `UPDATE members SET recovery_codes=$3 WHERE tenant_id=$1 AND id=$2`, tenant, memberID, hj); err != nil {
					return err
				}
				return store.Audit(ctx, tx, tenant, "member:"+memberID, "member.recovery_code_used", memberID, map[string]any{"left": len(hashes)})
			}
		}
		return ErrBadCode
	})
}

// ---------- sessions ----------

// Session is a signed-in device of a member.
type Session struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"userAgent"`
	Current    bool      `json:"current"`
}

// Sessions lists the active sessions of a member, most recent first.
func (s *Service) Sessions(ctx context.Context, tenant, memberID, currentID string) ([]Session, error) {
	rows, err := s.DB.System.Query(ctx, `SELECT id, created_at, last_seen_at, expires_at, ip, user_agent FROM sessions
		WHERE tenant_id=$1 AND member_id=$2 AND expires_at > now() ORDER BY last_seen_at DESC`, tenant, memberID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Session, error) {
		var x Session
		err := r.Scan(&x.ID, &x.CreatedAt, &x.LastSeenAt, &x.ExpiresAt, &x.IP, &x.UserAgent)
		x.Current = x.ID == currentID
		return x, err
	})
}

// RevokeSession signs a device out. id "" signs out every session except keepID.
func (s *Service) RevokeSession(ctx context.Context, tenant, memberID, id, keepID string) (int64, error) {
	ct, err := s.DB.System.Exec(ctx, `DELETE FROM sessions WHERE tenant_id=$1 AND member_id=$2 AND ($3 <> '' AND id=$3 OR $3 = '' AND id <> $4)`, tenant, memberID, id, keepID)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// ---------- invitations ----------

// Invitation asks someone to join the team.
type Invitation struct {
	ID          string      `json:"id"`
	Email       string      `json:"email"`
	Name        string      `json:"name"`
	Permissions Permissions `json:"permissions"`
	InvitedBy   string      `json:"invitedBy"`
	CreatedAt   time.Time   `json:"createdAt"`
	ExpiresAt   time.Time   `json:"expiresAt"`
	Expired     bool        `json:"expired"`
}

func inviteHash(token string) []byte {
	sum := sha256.Sum256([]byte("invite:" + token))
	return sum[:]
}

// Invite creates an invitation valid for 7 days and returns its secret token.
// sellerID ties the new member to a marketplace seller (they only see the seller portal).
func (s *Service) Invite(ctx context.Context, tenant, actor, email, name string, perms Permissions, sellerID string) (Invitation, string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") || len(email) > 200 {
		return Invitation{}, "", errors.New("auth: invalid email")
	}
	if err := perms.Validate(); err != nil {
		return Invitation{}, "", fmt.Errorf("auth: %s", strings.TrimPrefix(err.Error(), "auth: "))
	}
	token := newID("", 24)
	inv := Invitation{ID: newID("inv_", 8), Email: email, Name: strings.TrimSpace(name), Permissions: perms, InvitedBy: actor}
	pj, _ := json.Marshal(perms)
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE tenant_id=$1 AND email=$2)`, tenant, email).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return ErrExists
		}
		// A new invitation replaces a pending one for the same address.
		if _, err := tx.Exec(ctx, `UPDATE invitations SET revoked_at=now() WHERE tenant_id=$1 AND email=$2 AND accepted_at IS NULL AND revoked_at IS NULL`, tenant, email); err != nil {
			return err
		}
		var sid *string
		if sellerID != "" {
			sid = &sellerID
		}
		if err := tx.QueryRow(ctx, `INSERT INTO invitations (tenant_id, id, email, name, permissions, token_hash, invited_by, expires_at, seller_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7, now() + interval '7 days', $8) RETURNING created_at, expires_at`,
			tenant, inv.ID, email, inv.Name, pj, inviteHash(token), actor, sid).Scan(&inv.CreatedAt, &inv.ExpiresAt); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "member.invited", inv.ID, map[string]any{"email": email})
	})
	return inv, token, err
}

// Invitations lists the invitations that are still waiting.
func (s *Service) Invitations(ctx context.Context, tenant string) ([]Invitation, error) {
	var out []Invitation
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, email, name, permissions, invited_by, created_at, expires_at FROM invitations
			WHERE tenant_id=$1 AND accepted_at IS NULL AND revoked_at IS NULL ORDER BY created_at DESC`, tenant)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Invitation, error) {
			var x Invitation
			var pj []byte
			err := r.Scan(&x.ID, &x.Email, &x.Name, &pj, &x.InvitedBy, &x.CreatedAt, &x.ExpiresAt)
			_ = json.Unmarshal(pj, &x.Permissions)
			x.Expired = x.ExpiresAt.Before(time.Now())
			return x, err
		})
		return err
	})
	return out, err
}

// RevokeInvitation cancels a pending invitation.
func (s *Service) RevokeInvitation(ctx context.Context, tenant, actor, id string) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `UPDATE invitations SET revoked_at=now() WHERE tenant_id=$1 AND id=$2 AND accepted_at IS NULL AND revoked_at IS NULL`, tenant, id)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		return store.Audit(ctx, tx, tenant, actor, "member.invitation_revoked", id, map[string]any{})
	})
}

// InvitationInfo tells the invitee which store and address the link is for.
func (s *Service) InvitationInfo(ctx context.Context, token string) (tenant, storeName, email, name string, err error) {
	err = s.DB.System.QueryRow(ctx, `SELECT i.tenant_id, t.name, i.email, i.name FROM invitations i JOIN tenants t ON t.id=i.tenant_id
		WHERE i.token_hash=$1 AND i.accepted_at IS NULL AND i.revoked_at IS NULL AND i.expires_at > now()`, inviteHash(token)).Scan(&tenant, &storeName, &email, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrInvitation
	}
	return
}

// AcceptInvitation creates the member with the password they chose. The
// invitation works once.
func (s *Service) AcceptInvitation(ctx context.Context, token, name, password string) (string, Member, error) {
	if err := ValidatePassword(password); err != nil {
		return "", Member{}, err
	}
	tenant, _, email, invName, err := s.InvitationInfo(ctx, token)
	if err != nil {
		return "", Member{}, err
	}
	if strings.TrimSpace(name) == "" {
		name = invName
	}
	hash, err := HashPassword(password)
	if err != nil {
		return "", Member{}, err
	}
	m := Member{ID: newID("mem_", 8), Email: email, Name: strings.TrimSpace(name), Status: "active"}
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var pj []byte
		var id string
		var sid *string
		// Claiming the row inside the transaction makes a double click harmless.
		if err := tx.QueryRow(ctx, `UPDATE invitations SET accepted_at=now() WHERE tenant_id=$1 AND token_hash=$2 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now()
			RETURNING id, permissions, seller_id`, tenant, inviteHash(token)).Scan(&id, &pj, &sid); err != nil {
			return ErrInvitation
		}
		_ = json.Unmarshal(pj, &m.Permissions)
		if sid != nil {
			m.SellerID = *sid
		}
		if err := tx.QueryRow(ctx, `INSERT INTO members (tenant_id, id, email, name, password_hash, permissions, seller_id) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING created_at`,
			tenant, m.ID, email, m.Name, hash, pj, sid).Scan(&m.CreatedAt); err != nil {
			return ErrExists
		}
		return store.Audit(ctx, tx, tenant, "member:"+m.ID, "member.joined", m.ID, map[string]any{"invitation": id})
	})
	return tenant, m, err
}
