package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// KeySearch is the TopicSettings key of the search configuration.
const KeySearch = "search"

// KeySubscriptions is the TopicSettings key of the subscription configuration.
const KeySubscriptions = "subscriptions"

// KeySellers is the TopicSettings key of marketplace sellers and their products.
const KeySellers = "sellers"

// GetDoc loads a tenant document into v. It reports whether it exists.
func (d *DB) GetDoc(ctx context.Context, tenant, kind, id string, v any) (bool, error) {
	found := false
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var b []byte
		err := tx.QueryRow(ctx, `SELECT data FROM tenant_docs WHERE tenant_id=$1 AND kind=$2 AND id=$3`, tenant, kind, id).Scan(&b)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return json.Unmarshal(b, v)
	})
	return found, err
}

// ListDocs returns every document of a kind, by id.
func (d *DB) ListDocs(ctx context.Context, tenant, kind string) ([]Doc, error) {
	var out []Doc
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, data, updated_at FROM tenant_docs WHERE tenant_id=$1 AND kind=$2 ORDER BY id`, tenant, kind)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Doc, error) {
			var x Doc
			var b []byte
			err := r.Scan(&x.ID, &b, &x.UpdatedAt)
			x.Data = b
			return x, err
		})
		return err
	})
	return out, err
}

// SetDocTx upserts a tenant document, announces the change under the
// TopicSettings key (when not empty) and records it in the audit log.
func SetDocTx(ctx context.Context, tx pgx.Tx, tenant, actor, kind, id, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO tenant_docs (tenant_id, kind, id, data) VALUES ($1,$2,$3,$4)
		ON CONFLICT (tenant_id, kind, id) DO UPDATE SET data=EXCLUDED.data, updated_at=now()`, tenant, kind, id, b); err != nil {
		return err
	}
	if key != "" {
		if err := Emit(ctx, tx, tenant, TopicSettings, key, map[string]any{"kind": kind, "id": id}); err != nil {
			return err
		}
	}
	return Audit(ctx, tx, tenant, actor, kind+".set", id, json.RawMessage(b))
}

// SetDoc is SetDocTx in its own transaction.
func (d *DB) SetDoc(ctx context.Context, tenant, actor, kind, id, key string, v any) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error { return SetDocTx(ctx, tx, tenant, actor, kind, id, key, v) })
}

// RemoveDoc deletes a tenant document.
func (d *DB) RemoveDoc(ctx context.Context, tenant, actor, kind, id, key string) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `DELETE FROM tenant_docs WHERE tenant_id=$1 AND kind=$2 AND id=$3`, tenant, kind, id)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		if key != "" {
			if err := Emit(ctx, tx, tenant, TopicSettings, key, map[string]any{"kind": kind, "id": id, "deleted": true}); err != nil {
				return err
			}
		}
		return Audit(ctx, tx, tenant, actor, kind+".delete", id, map[string]any{})
	})
}

// ---------- search terms ----------

// TermCount is how often a term was searched and what it found.
type TermCount struct {
	Term     string `json:"term"`
	Searches int    `json:"searches"`
	Results  int    `json:"results"`
}

// AddSearchTerms adds today's searches of a store.
func (d *DB) AddSearchTerms(ctx context.Context, tenant string, terms []TermCount) error {
	if len(terms) == 0 {
		return nil
	}
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		for _, t := range terms {
			if _, err := tx.Exec(ctx, `INSERT INTO search_terms_daily (tenant_id, day, term, searches, results) VALUES ($1, (now() AT TIME ZONE 'America/Mexico_City')::date, $2, $3, $4)
				ON CONFLICT (tenant_id, day, term) DO UPDATE SET searches = search_terms_daily.searches + EXCLUDED.searches, results = EXCLUDED.results`,
				tenant, t.Term, t.Searches, t.Results); err != nil {
				return err
			}
		}
		return nil
	})
}

// SearchTerms returns the most searched terms of the last days. zero limits
// the list to terms that found nothing the last time.
func (d *DB) SearchTerms(ctx context.Context, tenant string, days, limit int, zero bool) ([]TermCount, error) {
	var out []TermCount
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT term, sum(searches)::int, (array_agg(results ORDER BY day DESC))[1] AS last
			FROM search_terms_daily WHERE tenant_id=$1 AND day > (now() AT TIME ZONE 'America/Mexico_City')::date - $2::int
			GROUP BY term HAVING NOT $4 OR (array_agg(results ORDER BY day DESC))[1] = 0 ORDER BY 2 DESC, term LIMIT $3`, tenant, days, limit, zero)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (TermCount, error) {
			var t TermCount
			return t, r.Scan(&t.Term, &t.Searches, &t.Results)
		})
		return err
	})
	return out, err
}

// ---------- reviews ----------

// Review is a product review.
type Review struct {
	ID        string     `json:"id"`
	ProductID string     `json:"productId"`
	Rating    int        `json:"rating"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Author    string     `json:"author"`
	Verified  bool       `json:"verified"`
	Status    string     `json:"status"`
	Reply     string     `json:"reply"`
	RepliedAt *time.Time `json:"repliedAt,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

const reviewCols = `id, product_id, rating, title, body, author, verified, status, reply, replied_at, created_at`

func scanReview(r pgx.CollectableRow) (Review, error) {
	var v Review
	return v, r.Scan(&v.ID, &v.ProductID, &v.Rating, &v.Title, &v.Body, &v.Author, &v.Verified, &v.Status, &v.Reply, &v.RepliedAt, &v.CreatedAt)
}

// CreateReview stores a review as pending. One review per product and email:
// writing again replaces the previous one and sends it back to moderation.
// skus are the SKUs of the product, used to mark verified purchases.
func (d *DB) CreateReview(ctx context.Context, tenant string, v Review, emailHash []byte, hashHex string, skus []string) (Review, error) {
	v.ID = NewID("rev_")
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM customers c JOIN orders o ON o.tenant_id=c.tenant_id AND o.customer_id=c.id
				JOIN order_lines l ON l.tenant_id=o.tenant_id AND l.order_id=o.id
			WHERE c.tenant_id=$1 AND c.email_hash=$2 AND l.sku = ANY($3) AND o.status NOT IN ('pending_payment','cancelled'))`, tenant, emailHash, skus).Scan(&v.Verified); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `INSERT INTO reviews (tenant_id, id, product_id, rating, title, body, author, email_hash, verified)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (tenant_id, product_id, email_hash) DO UPDATE SET rating=EXCLUDED.rating, title=EXCLUDED.title, body=EXCLUDED.body,
				author=EXCLUDED.author, verified=EXCLUDED.verified, status='pending', created_at=now()
			RETURNING `+reviewCols, tenant, v.ID, v.ProductID, v.Rating, v.Title, v.Body, v.Author, hashHex, v.Verified)
		if err != nil {
			return err
		}
		v, err = pgx.CollectExactlyOneRow(rows, scanReview)
		return err
	})
	return v, err
}

// Reviews lists reviews for moderation, newest first. before pages by time.
func (d *DB) Reviews(ctx context.Context, tenant, status, productID string, before time.Time, limit int) ([]Review, map[string]int, error) {
	var out []Review
	counts := map[string]int{}
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+reviewCols+` FROM reviews WHERE tenant_id=$1 AND ($2='' OR status=$2) AND ($3='' OR product_id=$3)
			AND ($4::timestamptz IS NULL OR created_at < $4) ORDER BY created_at DESC LIMIT $5`, tenant, status, productID, nullTime(before), limit)
		if err != nil {
			return err
		}
		if out, err = pgx.CollectRows(rows, scanReview); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT status, count(*)::int FROM reviews WHERE tenant_id=$1 GROUP BY status`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s string
			var n int
			if err := rows.Scan(&s, &n); err != nil {
				return err
			}
			counts[s] = n
		}
		return rows.Err()
	})
	return out, counts, err
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// ModerateReview changes the status and, when reply is not nil, the public answer.
func (d *DB) ModerateReview(ctx context.Context, tenant, actor, id, status string, reply *string) (Review, error) {
	var v Review
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE reviews SET status = CASE WHEN $3='' THEN status ELSE $3 END,
				reply = COALESCE($4, reply), replied_at = CASE WHEN $4::text IS NULL THEN replied_at WHEN $4='' THEN NULL ELSE now() END
			WHERE tenant_id=$1 AND id=$2 RETURNING `+reviewCols, tenant, id, status, reply)
		if err != nil {
			return err
		}
		v, err = pgx.CollectExactlyOneRow(rows, scanReview)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "review.moderate", id, map[string]any{"status": v.Status, "replied": v.Reply != ""})
	})
	return v, err
}

// DeleteReview removes a review for good.
func (d *DB) DeleteReview(ctx context.Context, tenant, actor, id string) (string, error) {
	var product string
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `DELETE FROM reviews WHERE tenant_id=$1 AND id=$2 RETURNING product_id`, tenant, id).Scan(&product)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "review.delete", id, map[string]any{"productId": product})
	})
	return product, err
}

// ProductReviews is what the product page shows.
type ProductReviews struct {
	Count   int      `json:"count"`
	Average float64  `json:"average"`
	Stars   [5]int   `json:"stars"` // Stars[0] = one star
	Items   []Review `json:"items"`
}

// PublicReviews returns the approved reviews of a product with their summary.
func (d *DB) PublicReviews(ctx context.Context, tenant, productID string, limit int) (ProductReviews, error) {
	var out ProductReviews
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT rating, count(*)::int FROM reviews WHERE tenant_id=$1 AND product_id=$2 AND status='approved' GROUP BY rating`, tenant, productID)
		if err != nil {
			return err
		}
		sum := 0
		for rows.Next() {
			var r, n int
			if err := rows.Scan(&r, &n); err != nil {
				rows.Close()
				return err
			}
			out.Stars[r-1] = n
			out.Count += n
			sum += r * n
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if out.Count == 0 {
			out.Items = []Review{}
			return nil
		}
		out.Average = float64(sum) / float64(out.Count)
		rows, err = tx.Query(ctx, `SELECT `+reviewCols+` FROM reviews WHERE tenant_id=$1 AND product_id=$2 AND status='approved'
			ORDER BY verified DESC, created_at DESC LIMIT $3`, tenant, productID, limit)
		if err != nil {
			return err
		}
		out.Items, err = pgx.CollectRows(rows, scanReview)
		return err
	})
	return out, err
}

// ---------- wish lists ----------

// SetWish adds or removes a product from the wish list of a customer.
func (d *DB) SetWish(ctx context.Context, tenant, customerID, productID string, on bool) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if on {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM wishlist_items WHERE tenant_id=$1 AND customer_id=$2`, tenant, customerID).Scan(&n); err != nil {
				return err
			}
			if n >= 200 {
				return ErrConflict
			}
			_, err := tx.Exec(ctx, `INSERT INTO wishlist_items (tenant_id, customer_id, product_id) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, tenant, customerID, productID)
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM wishlist_items WHERE tenant_id=$1 AND customer_id=$2 AND product_id=$3`, tenant, customerID, productID)
		return err
	})
}

// Wishes returns the product ids a customer saved, newest first.
func (d *DB) Wishes(ctx context.Context, tenant, customerID string) ([]string, error) {
	var out []string
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT product_id FROM wishlist_items WHERE tenant_id=$1 AND customer_id=$2 ORDER BY created_at DESC`, tenant, customerID)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	return out, err
}

// WishCount is how many customers saved a product.
type WishCount struct {
	ProductID string `json:"productId"`
	Customers int    `json:"customers"`
}

// TopWishes returns the most saved products.
func (d *DB) TopWishes(ctx context.Context, tenant string, limit int) ([]WishCount, error) {
	var out []WishCount
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT product_id, count(*)::int FROM wishlist_items WHERE tenant_id=$1 GROUP BY product_id ORDER BY 2 DESC, 1 LIMIT $2`, tenant, limit)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (WishCount, error) {
			var w WishCount
			return w, r.Scan(&w.ProductID, &w.Customers)
		})
		return err
	})
	return out, err
}

// SellerNames maps each product that belongs to an active marketplace seller
// to the seller's name (what the product page shows as "sold by").
func (d *DB) SellerNames(ctx context.Context, tenant string) (map[string]string, error) {
	out := map[string]string{}
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT sp.product_id, s.name FROM seller_products sp JOIN sellers s ON s.tenant_id=sp.tenant_id AND s.id=sp.seller_id WHERE sp.tenant_id=$1`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p, n string
			if err := rows.Scan(&p, &n); err != nil {
				return err
			}
			out[p] = n
		}
		return rows.Err()
	})
	return out, err
}

// KeyLanguages is the TopicSettings key of the storefront languages.
const KeyLanguages = "languages"

// KeyCheckoutBlocks is the TopicSettings key of checkout blocks.
const KeyCheckoutBlocks = "checkout_blocks"

// KeyCheckoutRules is the TopicSettings key of checkout rules.
const KeyCheckoutRules = "checkout_rules"

// KeyProductCustoms is the TopicSettings key of product personalization fields.
const KeyProductCustoms = "product_customs"

// KeyQtyRules is the TopicSettings key of product quantity rules.
const KeyQtyRules = "qty_rules"

// KeyOrderFees is the TopicSettings key of the extra charges per order.
const KeyOrderFees = "order_fees"

// KeyGiftOptions is the TopicSettings key of gift wrapping, messages and registries.
const KeyGiftOptions = "gift_options"

// KeyTags is the TopicSettings key of the storefront tags and pixels: every
// node rebuilds its copy when it changes, so a published tag shows on the
// next page view without a deploy.
const KeyTags = "tags"
