package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/customers"
	"github.com/helloghc/apexx/internal/orders"
	"github.com/helloghc/apexx/internal/store"
)

// Privacy requests: a customer can ask for a copy of their data or for it to
// be erased. Erasing keeps the accounting: orders, payments, refunds and
// invoices keep their amounts, but every personal detail in them is replaced
// and the links to the person are cut. Done from the panel by an admin of
// customers, or by the shopper from "Mi cuenta" after signing in with a code
// sent to their email (that sign-in is the identity check).

var errActiveSubscription = errors.New("customer has active subscriptions")

// exportCustomer gathers everything the store holds about a person.
func (s *Server) exportCustomer(ctx context.Context, tenant, id string) (map[string]any, error) {
	c, err := s.Customers.Get(ctx, tenant, id, true)
	if err != nil {
		return nil, err
	}
	page, err := s.Orders.Query(ctx, tenant, orders.ListFilter{CustomerID: id, Limit: 200}, true)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"exportedAt": time.Now().UTC(), "store": tenant, "customer": c, "orders": page.Items}
	// Custom fields, reviews and saved products.
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var data, secret []byte
		if err := tx.QueryRow(ctx, `SELECT data, secret FROM custom_values WHERE tenant_id=$1 AND resource='customer' AND owner_id=$2`, tenant, id).Scan(&data, &secret); err == nil {
			vals := map[string]string{}
			_ = json.Unmarshal(data, &vals)
			if len(secret) > 0 {
				if plain, err := s.Keys.Decrypt(tenant, secret, "fields:customer:"+id); err == nil {
					var p map[string]string
					_ = json.Unmarshal(plain, &p)
					for k, v := range p {
						vals[k] = v
					}
				}
			}
			out["customFields"] = vals
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var hash []byte
		if err := tx.QueryRow(ctx, `SELECT email_hash FROM customers WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&hash); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT product_id, rating, title, body, created_at FROM reviews WHERE tenant_id=$1 AND email_hash=$2 ORDER BY created_at`, tenant, hex.EncodeToString(hash))
		if err != nil {
			return err
		}
		var reviews []map[string]any
		for rows.Next() {
			var pid, title, body string
			var rating int
			var at time.Time
			if rows.Scan(&pid, &rating, &title, &body, &at) == nil {
				reviews = append(reviews, map[string]any{"productId": pid, "rating": rating, "title": title, "body": body, "createdAt": at})
			}
		}
		rows.Close()
		out["reviews"] = reviews
		var saved []string
		rows, err = tx.Query(ctx, `SELECT product_id FROM wishlist_items WHERE tenant_id=$1 AND customer_id=$2`, tenant, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var pid string
			if rows.Scan(&pid) == nil {
				saved = append(saved, pid)
			}
		}
		rows.Close()
		out["savedProducts"] = saved
		return nil
	})
	return out, err
}

// eraseCustomer removes a person from the store. It refuses while the person
// has active subscriptions: those charge a card and must be cancelled first.
func (s *Server) eraseCustomer(ctx context.Context, tenant, actor, id string) error {
	anon, _ := json.Marshal(orders.Customer{Name: "Cliente eliminado"})
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var hash []byte
		err := tx.QueryRow(ctx, `SELECT email_hash FROM customers WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, id).Scan(&hash)
		if errors.Is(err, pgx.ErrNoRows) {
			return customers.ErrNotFound
		}
		if err != nil {
			return err
		}
		var active int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM subscriptions WHERE tenant_id=$1 AND customer_id=$2 AND status IN ('active','paused','past_due')`, tenant, id).Scan(&active); err != nil {
			return err
		}
		if active > 0 {
			return errActiveSubscription
		}
		// Orders keep their amounts; the person inside them is replaced.
		reencrypt := func(table string, where string, args ...any) error {
			rows, err := tx.Query(ctx, `SELECT id FROM `+table+` WHERE tenant_id=$1 AND `+where, append([]any{tenant}, args...)...)
			if err != nil {
				return err
			}
			ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				return err
			}
			for _, oid := range ids {
				enc, err := s.Keys.Encrypt(tenant, anon, oid)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE `+table+` SET customer=$3 WHERE tenant_id=$1 AND id=$2`, tenant, oid, enc); err != nil {
					return err
				}
			}
			return nil
		}
		if err := reencrypt("orders", "(customer_id=$2 OR email_hash=$3)", id, hash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE orders SET email_hash=NULL WHERE tenant_id=$1 AND (customer_id=$2 OR email_hash=$3)`, tenant, id, hash); err != nil {
			return err
		}
		if err := reencrypt("subscriptions", "customer_id=$2", id); err != nil {
			return err
		}
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM notification_messages WHERE tenant_id=$1 AND customer_id=$2`, []any{tenant, id}},
			{`DELETE FROM abandoned_carts WHERE tenant_id=$1 AND (customer_id=$2 OR email_hash=$3)`, []any{tenant, id, hash}},
			{`DELETE FROM wishlist_items WHERE tenant_id=$1 AND customer_id=$2`, []any{tenant, id}},
			{`DELETE FROM custom_values WHERE tenant_id=$1 AND resource='customer' AND owner_id=$2`, []any{tenant, id}},
			{`DELETE FROM company_buyers WHERE tenant_id=$1 AND email_hash=$2`, []any{tenant, hash}},
			{`UPDATE reviews SET author='Cliente', email_hash='' WHERE tenant_id=$1 AND email_hash=encode($2,'hex')`, []any{tenant, hash}},
		} {
			if _, err := tx.Exec(ctx, q.sql, q.args...); err != nil {
				return err
			}
		}
		return store.Audit(ctx, tx, tenant, actor, "customer.erased", id, map[string]any{})
	})
	if err != nil {
		return err
	}
	return s.Customers.Delete(ctx, tenant, actor, id)
}

func (s *Server) customerExport(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	out, err := s.exportCustomer(r.Context(), p.Tenant, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeExport(w, out, r.PathValue("id"))
}

func writeExport(w http.ResponseWriter, out map[string]any, id string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="datos-`+id+`.json"`)
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

func (s *Server) failErase(w http.ResponseWriter, err error) {
	if errors.Is(err, errActiveSubscription) {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "active_subscriptions", Detail: "Tiene suscripciones activas. Cancélalas primero para no cobrar a una persona que ya no existe en la tienda"})
		return
	}
	s.fail(w, err)
}
