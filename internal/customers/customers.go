// Package customers keeps the people who buy from a store: profiles,
// addresses, tags, notes, marketing consent, groups and lifetime stats, plus
// shopper accounts (sign in with a one-time code or a password) and abandoned
// carts.
//
// Personal data is encrypted with the tenant key. Lookups by email, phone or a
// word of the name go through blind indexes (HMAC under a key derived from the
// tenant key), so searching works without any clear text in the database.
package customers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"github.com/helloghc/apexx/internal/crypto"
	"github.com/helloghc/apexx/internal/notify"
	"github.com/helloghc/apexx/internal/store"
)

// Topic of the outbox events of this package.
const Topic = "customer.changed"

// Errors.
var (
	ErrNotFound = errors.New("customers: not found")
	ErrConflict = errors.New("customers: conflict")
	ErrVersion  = errors.New("customers: version mismatch")
)

// FieldError is a validation error on one field.
type FieldError struct{ Field, Message string }

func (e FieldError) Error() string { return e.Field + ": " + e.Message }

// Service manages customers.
type Service struct {
	DB     *store.DB
	Keys   *crypto.Keyring
	Notify *notify.Service
	// Simulate prices a cart for abandoned-cart tracking (wired to the projection).
	Simulate func(tenant, channel string, items []CartItem) (lines []CartLine, total int64, currency string, ok bool)
	// PublicURL is the origin used in links inside emails.
	PublicURL string
	Now       func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func newID(prefix string) string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// Profile is the encrypted part of a customer.
type Profile struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	Phone string `json:"phone,omitempty"`
}

// Address is a postal address of a customer.
type Address struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Phone      string `json:"phone,omitempty"`
	Line1      string `json:"line1"`
	Line2      string `json:"line2,omitempty"`
	City       string `json:"city"`
	State      string `json:"state"`
	PostalCode string `json:"postalCode"`
	Country    string `json:"country"`
	Default    bool   `json:"default"`
}

// Note is an internal note about a customer.
type Note struct {
	ID         string    `json:"id"`
	Body       string    `json:"body"`
	Author     string    `json:"author"`
	AuthorName string    `json:"authorName"`
	CreatedAt  time.Time `json:"createdAt"`
}

// Customer is the API view.
type Customer struct {
	ID               string     `json:"id"`
	Email            string     `json:"email"`
	Name             string     `json:"name"`
	Phone            string     `json:"phone,omitempty"`
	Tags             []string   `json:"tags"`
	MarketingConsent bool       `json:"marketingConsent"`
	ConsentAt        *time.Time `json:"consentAt,omitempty"`
	ConsentSource    string     `json:"consentSource,omitempty"`
	HasAccount       bool       `json:"hasAccount"`
	HasPassword      bool       `json:"hasPassword"`
	Status           string     `json:"status"`
	OrdersCount      int        `json:"ordersCount"`
	TotalSpent       int64      `json:"totalSpent"`
	Currency         string     `json:"currency"`
	FirstOrderAt     *time.Time `json:"firstOrderAt,omitempty"`
	LastOrderAt      *time.Time `json:"lastOrderAt,omitempty"`
	Version          int64      `json:"version"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	Masked           bool       `json:"masked,omitempty"`
	Addresses        []Address  `json:"addresses,omitempty"`
	Notes            []Note     `json:"notes,omitempty"`
	Groups           []GroupRef `json:"groups,omitempty"`
}

// GroupRef names a group a customer belongs to.
type GroupRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// ---------- normalization and blind indexes ----------

// NormalizeEmail trims and lower-cases an email; ok is false when it is not valid.
func NormalizeEmail(e string) (string, bool) {
	e = strings.ToLower(strings.TrimSpace(e))
	if len(e) < 3 || len(e) > 254 || strings.ContainsAny(e, " \r\n<>,;") {
		return "", false
	}
	a, err := mail.ParseAddress(e)
	if err != nil || a.Address != e {
		return "", false
	}
	return e, true
}

// PhoneDigits keeps the digits of a phone ("+52 (55) 1234-5678" → "525512345678").
func PhoneDigits(p string) string {
	var b strings.Builder
	for _, r := range p {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func fold(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, _ := transform.String(t, strings.ToLower(s))
	return out
}

func nameWords(s string) []string {
	ws := strings.FieldsFunc(fold(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	seen := map[string]bool{}
	var out []string
	for _, w := range ws {
		if len(w) >= 2 && !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

// EmailHash is the blind index of an email.
func EmailHash(k *crypto.Keyring, tenant, email string) ([]byte, error) {
	return k.Blind(tenant, "email", strings.ToLower(strings.TrimSpace(email)))
}

func phoneHash(k *crypto.Keyring, tenant, phone string) ([]byte, error) {
	d := PhoneDigits(phone)
	if len(d) < 7 {
		return nil, nil
	}
	// Compare on the last 10 digits so "+52 55…" and "55…" match.
	if len(d) > 10 {
		d = d[len(d)-10:]
	}
	return k.Blind(tenant, "phone", d)
}

func nameTokens(k *crypto.Keyring, tenant, name string) ([][]byte, error) {
	out := [][]byte{}
	for _, w := range nameWords(name) {
		h, err := k.Blind(tenant, "name", w)
		if err != nil {
			return nil, err
		}
		out = append(out, h[:12])
	}
	return out, nil
}

func sealProfile(k *crypto.Keyring, tenant, id string, p Profile) (enc, emailH, phoneH []byte, tokens [][]byte, err error) {
	raw, _ := json.Marshal(p)
	if enc, err = k.Encrypt(tenant, raw, id); err != nil {
		return
	}
	if emailH, err = EmailHash(k, tenant, p.Email); err != nil {
		return
	}
	if phoneH, err = phoneHash(k, tenant, p.Phone); err != nil {
		return
	}
	tokens, err = nameTokens(k, tenant, p.Name)
	return
}

func openProfile(k *crypto.Keyring, tenant, id string, enc []byte) (Profile, error) {
	var p Profile
	plain, err := k.Decrypt(tenant, enc, id)
	if err != nil {
		return p, err
	}
	return p, json.Unmarshal(plain, &p)
}

func maskName(n string) string {
	parts := strings.Fields(n)
	for i, p := range parts {
		r := []rune(p)
		parts[i] = string(r[:1]) + "."
	}
	return strings.Join(parts, " ")
}

// ---------- upsert from checkout ----------

// UpsertByEmailTx finds the customer with this email or creates it, inside the
// caller's transaction (checkout, draft orders, newsletter). Existing data is
// never overwritten: only an empty name or phone is filled in.
func UpsertByEmailTx(ctx context.Context, tx pgx.Tx, k *crypto.Keyring, tenant, email, name, phone string) (id string, created bool, err error) {
	e, ok := NormalizeEmail(email)
	if !ok {
		return "", false, FieldError{"email", "Correo inválido"}
	}
	h, err := EmailHash(k, tenant, e)
	if err != nil {
		return "", false, err
	}
	// Serialize concurrent first purchases of the same email.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('customer:' || $1 || ':' || encode($2,'hex')))`, tenant, h); err != nil {
		return "", false, err
	}
	var enc []byte
	err = tx.QueryRow(ctx, `SELECT id, profile FROM customers WHERE tenant_id=$1 AND email_hash=$2`, tenant, h).Scan(&id, &enc)
	if err == nil {
		p, perr := openProfile(k, tenant, id, enc)
		if perr != nil {
			return id, false, nil // unreadable profile: keep the link, change nothing
		}
		changed := false
		if p.Name == "" && strings.TrimSpace(name) != "" {
			p.Name, changed = strings.TrimSpace(name), true
		}
		if p.Phone == "" && strings.TrimSpace(phone) != "" {
			p.Phone, changed = strings.TrimSpace(phone), true
		}
		if changed {
			enc, eh, ph, toks, serr := sealProfile(k, tenant, id, p)
			if serr != nil {
				return "", false, serr
			}
			if _, err = tx.Exec(ctx, `UPDATE customers SET profile=$3, email_hash=$4, phone_hash=$5, name_tokens=$6, version=version+1, updated_at=now() WHERE tenant_id=$1 AND id=$2`,
				tenant, id, enc, eh, ph, toks); err != nil {
				return "", false, err
			}
		}
		return id, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, err
	}
	id = newID("cus_")
	enc, eh, ph, toks, err := sealProfile(k, tenant, id, Profile{Email: e, Name: strings.TrimSpace(name), Phone: strings.TrimSpace(phone)})
	if err != nil {
		return "", false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO customers (tenant_id, id, email_hash, phone_hash, name_tokens, profile) VALUES ($1,$2,$3,$4,$5,$6)`,
		tenant, id, eh, ph, toks, enc); err != nil {
		return "", false, err
	}
	if err = store.Emit(ctx, tx, tenant, Topic, id, map[string]any{"type": "customer.created"}); err != nil {
		return "", false, err
	}
	return id, true, nil
}

// SaveAddressIfNoneTx stores a free-text checkout address as the first address of a customer.
func SaveAddressIfNoneTx(ctx context.Context, tx pgx.Tx, k *crypto.Keyring, tenant, customerID, name, phone, line1, postal string) error {
	if strings.TrimSpace(line1) == "" {
		return nil
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM customer_addresses WHERE tenant_id=$1 AND customer_id=$2`, tenant, customerID).Scan(&n); err != nil || n > 0 {
		return err
	}
	a := Address{ID: newID("adr_"), Name: name, Phone: phone, Line1: line1, PostalCode: postal, Country: "MX", Default: true}
	raw, _ := json.Marshal(a)
	enc, err := k.Encrypt(tenant, raw, a.ID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO customer_addresses (tenant_id, id, customer_id, data, is_default) VALUES ($1,$2,$3,$4,true)`, tenant, a.ID, customerID, enc)
	return err
}

// ---------- read ----------

const customerCols = `id, profile, tags, marketing_consent, consent_at, consent_source, password_hash <> '', verified_at IS NOT NULL, status,
	orders_count, total_spent, currency, first_order_at, last_order_at, version, created_at, updated_at`

func (s *Service) scan(tenant string, r pgx.Row, pii bool) (Customer, error) {
	var c Customer
	var enc []byte
	if err := r.Scan(&c.ID, &enc, &c.Tags, &c.MarketingConsent, &c.ConsentAt, &c.ConsentSource, &c.HasPassword, &c.HasAccount, &c.Status,
		&c.OrdersCount, &c.TotalSpent, &c.Currency, &c.FirstOrderAt, &c.LastOrderAt, &c.Version, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return c, err
	}
	if c.Tags == nil {
		c.Tags = []string{}
	}
	p, err := openProfile(s.Keys, tenant, c.ID, enc)
	if err != nil {
		c.Masked, c.Name, c.Email = true, "(dato ilegible)", ""
		return c, nil
	}
	if pii {
		c.Email, c.Name, c.Phone = p.Email, p.Name, p.Phone
	} else {
		c.Masked, c.Email, c.Name = true, notify.MaskEmail(p.Email), maskName(p.Name)
	}
	return c, nil
}

// Get loads a customer with addresses, notes and groups.
func (s *Service) Get(ctx context.Context, tenant, id string, pii bool) (Customer, error) {
	var c Customer
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		c, err = s.scan(tenant, tx.QueryRow(ctx, `SELECT `+customerCols+` FROM customers WHERE tenant_id=$1 AND id=$2`, tenant, id), pii)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if pii {
			if c.Addresses, err = s.addressesTx(ctx, tx, tenant, id); err != nil {
				return err
			}
			rows, err := tx.Query(ctx, `SELECT id, body, author, author_name, created_at FROM customer_notes WHERE tenant_id=$1 AND customer_id=$2 ORDER BY created_at DESC LIMIT 200`, tenant, id)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var n Note
				var enc []byte
				if err := rows.Scan(&n.ID, &enc, &n.Author, &n.AuthorName, &n.CreatedAt); err != nil {
					return err
				}
				if plain, derr := s.Keys.Decrypt(tenant, enc, n.ID); derr == nil {
					n.Body = string(plain)
				}
				c.Notes = append(c.Notes, n)
			}
			rows.Close()
		}
		c.Groups, err = s.groupsOfTx(ctx, tx, tenant, id)
		return err
	})
	return c, err
}

func (s *Service) addressesTx(ctx context.Context, tx pgx.Tx, tenant, id string) ([]Address, error) {
	rows, err := tx.Query(ctx, `SELECT id, data, is_default FROM customer_addresses WHERE tenant_id=$1 AND customer_id=$2 ORDER BY is_default DESC, created_at`, tenant, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Address{}
	for rows.Next() {
		var a Address
		var aid string
		var enc []byte
		var def bool
		if err := rows.Scan(&aid, &enc, &def); err != nil {
			return nil, err
		}
		if plain, derr := s.Keys.Decrypt(tenant, enc, aid); derr == nil {
			_ = json.Unmarshal(plain, &a)
		}
		a.ID, a.Default = aid, def
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListFilter filters the customer list.
type ListFilter struct {
	Q       string // email, phone or words of the name (exact, blind-indexed)
	Tag     string
	Group   string
	Consent string // "yes" | "no" | ""
	Account string // "yes" | "no" | ""
	Sort    string // created_desc (default) | spent_desc | orders_desc | last_order_desc
	Cursor  string
	Limit   int
}

var sortCols = map[string]string{
	"created_desc":    "created_at",
	"spent_desc":      "total_spent",
	"orders_desc":     "orders_count",
	"last_order_desc": "COALESCE(last_order_at, 'epoch'::timestamptz)",
}

// List returns customers and the cursor of the next page.
func (s *Service) List(ctx context.Context, tenant string, f ListFilter, pii bool) ([]Customer, string, int, error) {
	if f.Limit < 1 || f.Limit > 200 {
		f.Limit = 50
	}
	col, ok := sortCols[f.Sort]
	if !ok {
		f.Sort, col = "created_desc", sortCols["created_desc"]
	}
	where := ` WHERE c.tenant_id=$1`
	args := []any{tenant}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		switch {
		case strings.Contains(q, "@"):
			h, err := EmailHash(s.Keys, tenant, q)
			if err != nil {
				return nil, "", 0, err
			}
			where += ` AND c.email_hash=` + arg(h)
		case len(PhoneDigits(q)) >= 7 && !strings.ContainsFunc(q, unicode.IsLetter):
			h, err := phoneHash(s.Keys, tenant, q)
			if err != nil {
				return nil, "", 0, err
			}
			where += ` AND c.phone_hash=` + arg(h)
		default:
			toks, err := nameTokens(s.Keys, tenant, q)
			if err != nil {
				return nil, "", 0, err
			}
			if len(toks) == 0 {
				return []Customer{}, "", 0, nil
			}
			where += ` AND c.name_tokens @> ` + arg(toks)
		}
	}
	if f.Tag != "" {
		where += ` AND c.tags @> ` + arg([]string{f.Tag})
	}
	switch f.Consent {
	case "yes":
		where += ` AND c.marketing_consent`
	case "no":
		where += ` AND NOT c.marketing_consent`
	}
	switch f.Account {
	case "yes":
		where += ` AND c.verified_at IS NOT NULL`
	case "no":
		where += ` AND c.verified_at IS NULL`
	}
	var out []Customer
	total := 0
	next := ""
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if f.Group != "" {
			g, err := s.groupTx(ctx, tx, tenant, f.Group)
			if err != nil {
				return err
			}
			if g.Kind == "manual" {
				where += ` AND EXISTS (SELECT 1 FROM customer_group_members m WHERE m.tenant_id=c.tenant_id AND m.customer_id=c.id AND m.group_id=` + arg(g.ID) + `)`
			} else {
				cond, err := ruleSQL(g.Rules, arg)
				if err != nil {
					return err
				}
				where += ` AND (` + cond + `)`
			}
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM customers c`+where, args...).Scan(&total); err != nil {
			return err
		}
		q := `SELECT ` + customerCols + `, (` + col + `)::text FROM customers c` + where
		if f.Cursor != "" {
			v, id, ok := strings.Cut(f.Cursor, "|")
			if !ok {
				return FieldError{"cursor", "Cursor inválido"}
			}
			cast := "::bigint"
			if f.Sort == "created_desc" || f.Sort == "last_order_desc" {
				cast = "::timestamptz"
			}
			q += ` AND (` + col + `, c.id) < (` + arg(v) + cast + `, ` + arg(id) + `)`
		}
		q += ` ORDER BY ` + col + ` DESC, c.id DESC LIMIT ` + arg(f.Limit+1)
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		var keys []string
		for rows.Next() {
			var key string
			c, err := s.scan(tenant, scanPlus{rows, &key}, pii)
			if err != nil {
				return err
			}
			out = append(out, c)
			keys = append(keys, key)
		}
		if len(out) > f.Limit {
			out = out[:f.Limit]
			next = keys[f.Limit-1] + "|" + out[f.Limit-1].ID
		}
		return rows.Err()
	})
	if out == nil {
		out = []Customer{}
	}
	return out, next, total, err
}

// scanPlus scans the customer columns plus one trailing sort key.
type scanPlus struct {
	rows pgx.Rows
	key  *string
}

func (s scanPlus) Scan(dst ...any) error { return s.rows.Scan(append(dst, s.key)...) }

// ---------- write ----------

// Input creates or updates a customer.
type Input struct {
	Email            *string   `json:"email"`
	Name             *string   `json:"name"`
	Phone            *string   `json:"phone"`
	Tags             *[]string `json:"tags"`
	MarketingConsent *bool     `json:"marketingConsent"`
	Status           *string   `json:"status"`
}

// CleanTags normalizes tags: trimmed, lower-case, unique, at most 20 of 40 characters.
func CleanTags(in []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, t := range in {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] {
			continue
		}
		if len(t) > 40 || strings.ContainsAny(t, ",\n\r\t") {
			return nil, FieldError{"tags", "Cada etiqueta: hasta 40 caracteres, sin comas"}
		}
		seen[t] = true
		out = append(out, t)
	}
	if len(out) > 20 {
		return nil, FieldError{"tags", "Máximo 20 etiquetas por cliente"}
	}
	sort.Strings(out)
	return out, nil
}

func checkProfile(p Profile) error {
	if len(p.Name) > 120 {
		return FieldError{"name", "El nombre admite hasta 120 caracteres"}
	}
	if p.Phone != "" && (len(PhoneDigits(p.Phone)) < 7 || len(p.Phone) > 30) {
		return FieldError{"phone", "Teléfono inválido: usa al menos 7 dígitos"}
	}
	return nil
}

// Create adds a customer from the admin.
func (s *Service) Create(ctx context.Context, tenant, actor string, in Input) (Customer, error) {
	if in.Email == nil {
		return Customer{}, FieldError{"email", "El correo es obligatorio"}
	}
	e, ok := NormalizeEmail(*in.Email)
	if !ok {
		return Customer{}, FieldError{"email", "Correo inválido"}
	}
	p := Profile{Email: e}
	if in.Name != nil {
		p.Name = strings.TrimSpace(*in.Name)
	}
	if in.Phone != nil {
		p.Phone = strings.TrimSpace(*in.Phone)
	}
	if err := checkProfile(p); err != nil {
		return Customer{}, err
	}
	tags := []string{}
	if in.Tags != nil {
		var err error
		if tags, err = CleanTags(*in.Tags); err != nil {
			return Customer{}, err
		}
	}
	id := newID("cus_")
	enc, eh, ph, toks, err := sealProfile(s.Keys, tenant, id, p)
	if err != nil {
		return Customer{}, err
	}
	consent := in.MarketingConsent != nil && *in.MarketingConsent
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `INSERT INTO customers (tenant_id, id, email_hash, phone_hash, name_tokens, profile, tags, marketing_consent, consent_at, consent_source)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8, CASE WHEN $8 THEN now() END, CASE WHEN $8 THEN 'admin' ELSE '' END) ON CONFLICT (tenant_id, email_hash) DO NOTHING`,
			tenant, id, eh, ph, toks, enc, tags, consent)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrConflict
		}
		if err := store.Emit(ctx, tx, tenant, Topic, id, map[string]any{"type": "customer.created"}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "customer.created", id, map[string]any{"tags": tags})
	})
	if err != nil {
		return Customer{}, err
	}
	return s.Get(ctx, tenant, id, true)
}

// Update changes the given fields. ifVersion > 0 enforces optimistic locking.
func (s *Service) Update(ctx context.Context, tenant, actor, id string, in Input, ifVersion int64, source string) (Customer, error) {
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var enc []byte
		var version int64
		var consent bool
		err := tx.QueryRow(ctx, `SELECT profile, version, marketing_consent FROM customers WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, id).Scan(&enc, &version, &consent)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if ifVersion > 0 && ifVersion != version {
			return ErrVersion
		}
		p, err := openProfile(s.Keys, tenant, id, enc)
		if err != nil {
			return err
		}
		changed := []string{}
		if in.Email != nil {
			e, ok := NormalizeEmail(*in.Email)
			if !ok {
				return FieldError{"email", "Correo inválido"}
			}
			if e != p.Email {
				p.Email = e
				changed = append(changed, "email")
				// A new email is not proven yet: sessions and the verified mark go away.
				if _, err := tx.Exec(ctx, `UPDATE customers SET verified_at=NULL WHERE tenant_id=$1 AND id=$2`, tenant, id); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `DELETE FROM shopper_sessions WHERE tenant_id=$1 AND customer_id=$2`, tenant, id); err != nil {
					return err
				}
			}
		}
		if in.Name != nil && strings.TrimSpace(*in.Name) != p.Name {
			p.Name = strings.TrimSpace(*in.Name)
			changed = append(changed, "name")
		}
		if in.Phone != nil && strings.TrimSpace(*in.Phone) != p.Phone {
			p.Phone = strings.TrimSpace(*in.Phone)
			changed = append(changed, "phone")
		}
		if err := checkProfile(p); err != nil {
			return err
		}
		enc2, eh, ph, toks, err := sealProfile(s.Keys, tenant, id, p)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE customers SET profile=$3, email_hash=$4, phone_hash=$5, name_tokens=$6, version=version+1, updated_at=now() WHERE tenant_id=$1 AND id=$2`,
			tenant, id, enc2, eh, ph, toks); err != nil {
			if strings.Contains(err.Error(), "customers_tenant_id_email_hash_key") {
				return ErrConflict
			}
			return err
		}
		if in.Tags != nil {
			tags, err := CleanTags(*in.Tags)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE customers SET tags=$3 WHERE tenant_id=$1 AND id=$2`, tenant, id, tags); err != nil {
				return err
			}
			changed = append(changed, "tags")
		}
		if in.MarketingConsent != nil && *in.MarketingConsent != consent {
			if _, err := tx.Exec(ctx, `UPDATE customers SET marketing_consent=$3, consent_at=now(), consent_source=$4 WHERE tenant_id=$1 AND id=$2`, tenant, id, *in.MarketingConsent, source); err != nil {
				return err
			}
			changed = append(changed, "marketingConsent")
		}
		if in.Status != nil {
			if *in.Status != "active" && *in.Status != "disabled" {
				return FieldError{"status", "status: active o disabled"}
			}
			if _, err := tx.Exec(ctx, `UPDATE customers SET status=$3 WHERE tenant_id=$1 AND id=$2`, tenant, id, *in.Status); err != nil {
				return err
			}
			if *in.Status == "disabled" {
				if _, err := tx.Exec(ctx, `DELETE FROM shopper_sessions WHERE tenant_id=$1 AND customer_id=$2`, tenant, id); err != nil {
					return err
				}
			}
			changed = append(changed, "status")
		}
		if err := store.Emit(ctx, tx, tenant, Topic, id, map[string]any{"type": "customer.updated", "fields": changed}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "customer.updated", id, map[string]any{"fields": changed})
	})
	if err != nil {
		return Customer{}, err
	}
	return s.Get(ctx, tenant, id, true)
}

// Delete erases a customer: profile, addresses, notes, sessions and group
// memberships. Orders stay (they are accounting records) but lose the link.
func (s *Service) Delete(ctx context.Context, tenant, actor, id string) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE orders SET customer_id=NULL WHERE tenant_id=$1 AND customer_id=$2`, tenant, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE abandoned_carts SET customer_id=NULL WHERE tenant_id=$1 AND customer_id=$2`, tenant, id); err != nil {
			return err
		}
		ct, err := tx.Exec(ctx, `DELETE FROM customers WHERE tenant_id=$1 AND id=$2`, tenant, id)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		if err := store.Emit(ctx, tx, tenant, Topic, id, map[string]any{"type": "customer.deleted"}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "customer.deleted", id, map[string]any{})
	})
}

// BulkTag adds or removes one tag on many customers.
func (s *Service) BulkTag(ctx context.Context, tenant, actor string, ids []string, tag string, remove bool) (int, error) {
	tags, err := CleanTags([]string{tag})
	if err != nil || len(tags) != 1 {
		return 0, FieldError{"tag", "Etiqueta inválida"}
	}
	if len(ids) == 0 || len(ids) > 500 {
		return 0, FieldError{"ids", "Selecciona entre 1 y 500 clientes"}
	}
	n := 0
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		q := `UPDATE customers SET tags = (SELECT array_agg(DISTINCT t ORDER BY t) FROM unnest(tags || $3::text) t), version=version+1, updated_at=now()
			WHERE tenant_id=$1 AND id = ANY($2) AND NOT tags @> ARRAY[$3::text] AND cardinality(tags) < 20`
		if remove {
			q = `UPDATE customers SET tags = array_remove(tags, $3::text), version=version+1, updated_at=now() WHERE tenant_id=$1 AND id = ANY($2) AND tags @> ARRAY[$3::text]`
		}
		ct, err := tx.Exec(ctx, q, tenant, ids, tags[0])
		if err != nil {
			return err
		}
		n = int(ct.RowsAffected())
		if err := store.Emit(ctx, tx, tenant, Topic, "bulk", map[string]any{"type": "customer.bulk_tagged", "count": n}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "customer.bulk_tag", tags[0], map[string]any{"count": n, "remove": remove})
	})
	return n, err
}

// Tags lists the tags in use with how many customers carry each.
func (s *Service) Tags(ctx context.Context, tenant string) (map[string]int, error) {
	out := map[string]int{}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT t, count(*) FROM customers, unnest(tags) t WHERE tenant_id=$1 GROUP BY t`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var t string
			var n int
			if err := rows.Scan(&t, &n); err != nil {
				return err
			}
			out[t] = n
		}
		return rows.Err()
	})
	return out, err
}

// ---------- addresses ----------

func checkAddress(a *Address) error {
	for _, f := range []*string{&a.Name, &a.Phone, &a.Line1, &a.Line2, &a.City, &a.State, &a.PostalCode, &a.Country} {
		*f = strings.TrimSpace(*f)
		if len(*f) > 200 {
			return FieldError{"line1", "Texto demasiado largo"}
		}
	}
	if a.Line1 == "" {
		return FieldError{"line1", "Escribe calle y número"}
	}
	if a.Country == "" {
		a.Country = "MX"
	}
	if a.Country == "MX" {
		if len(a.PostalCode) != 5 || PhoneDigits(a.PostalCode) != a.PostalCode {
			return FieldError{"postalCode", "El código postal debe tener 5 dígitos"}
		}
	} else if a.PostalCode == "" {
		return FieldError{"postalCode", "Escribe el código postal"}
	}
	if a.City == "" {
		return FieldError{"city", "Escribe la ciudad o alcaldía"}
	}
	if a.State == "" {
		return FieldError{"state", "Escribe el estado"}
	}
	return nil
}

// SaveAddress creates (a.ID empty) or updates an address.
func (s *Service) SaveAddress(ctx context.Context, tenant, actor, customerID string, a Address) (Address, error) {
	if err := checkAddress(&a); err != nil {
		return a, err
	}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM customer_addresses WHERE tenant_id=$1 AND customer_id=$2`, tenant, customerID).Scan(&n); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM customers WHERE tenant_id=$1 AND id=$2)`, tenant, customerID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		create := a.ID == ""
		if create {
			if n >= 20 {
				return FieldError{"line1", "Máximo 20 direcciones por cliente"}
			}
			a.ID = newID("adr_")
			if n == 0 {
				a.Default = true
			}
		}
		raw, _ := json.Marshal(a)
		enc, err := s.Keys.Encrypt(tenant, raw, a.ID)
		if err != nil {
			return err
		}
		if a.Default {
			if _, err := tx.Exec(ctx, `UPDATE customer_addresses SET is_default=false WHERE tenant_id=$1 AND customer_id=$2`, tenant, customerID); err != nil {
				return err
			}
		}
		if create {
			_, err = tx.Exec(ctx, `INSERT INTO customer_addresses (tenant_id, id, customer_id, data, is_default) VALUES ($1,$2,$3,$4,$5)`, tenant, a.ID, customerID, enc, a.Default)
		} else {
			var ct interface{ RowsAffected() int64 }
			ct, err = tx.Exec(ctx, `UPDATE customer_addresses SET data=$4, is_default=(is_default OR $5), updated_at=now() WHERE tenant_id=$1 AND id=$2 AND customer_id=$3`, tenant, a.ID, customerID, enc, a.Default)
			if err == nil && ct.RowsAffected() == 0 {
				return ErrNotFound
			}
		}
		if err != nil {
			return err
		}
		if err := store.Emit(ctx, tx, tenant, Topic, customerID, map[string]any{"type": "customer.address_saved"}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "customer.address_saved", customerID, map[string]any{"address": a.ID})
	})
	return a, err
}

// DeleteAddress removes an address; the oldest remaining one becomes the default.
func (s *Service) DeleteAddress(ctx context.Context, tenant, actor, customerID, addressID string) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `DELETE FROM customer_addresses WHERE tenant_id=$1 AND id=$2 AND customer_id=$3`, tenant, addressID, customerID)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		if _, err := tx.Exec(ctx, `UPDATE customer_addresses SET is_default=true WHERE tenant_id=$1 AND id=(
			SELECT id FROM customer_addresses WHERE tenant_id=$1 AND customer_id=$2 ORDER BY created_at LIMIT 1)
			AND NOT EXISTS (SELECT 1 FROM customer_addresses WHERE tenant_id=$1 AND customer_id=$2 AND is_default)`, tenant, customerID); err != nil {
			return err
		}
		if err := store.Emit(ctx, tx, tenant, Topic, customerID, map[string]any{"type": "customer.address_deleted"}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "customer.address_deleted", customerID, map[string]any{"address": addressID})
	})
}

// Addresses lists the addresses of a customer.
func (s *Service) Addresses(ctx context.Context, tenant, customerID string) ([]Address, error) {
	var out []Address
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var e error
		out, e = s.addressesTx(ctx, tx, tenant, customerID)
		return e
	})
	return out, err
}

// ---------- notes ----------

// AddNote appends an internal note.
func (s *Service) AddNote(ctx context.Context, tenant, actor, actorName, customerID, body string) (Note, error) {
	body = strings.TrimSpace(body)
	if body == "" || len(body) > 2000 {
		return Note{}, FieldError{"body", "Escribe una nota de hasta 2,000 caracteres"}
	}
	n := Note{ID: newID("cnt_"), Body: body, Author: actor, AuthorName: actorName, CreatedAt: s.now()}
	enc, err := s.Keys.Encrypt(tenant, []byte(body), n.ID)
	if err != nil {
		return n, err
	}
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO customer_notes (tenant_id, id, customer_id, body, author, author_name, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			tenant, n.ID, customerID, enc, actor, actorName, n.CreatedAt); err != nil {
			if strings.Contains(err.Error(), "foreign key") {
				return ErrNotFound
			}
			return err
		}
		if err := store.Emit(ctx, tx, tenant, Topic, customerID, map[string]any{"type": "customer.note_added"}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "customer.note_added", customerID, map[string]any{"note": n.ID})
	})
	return n, err
}

// DeleteNote removes a note.
func (s *Service) DeleteNote(ctx context.Context, tenant, actor, customerID, noteID string) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `DELETE FROM customer_notes WHERE tenant_id=$1 AND id=$2 AND customer_id=$3`, tenant, noteID, customerID)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		if err := store.Emit(ctx, tx, tenant, Topic, customerID, map[string]any{"type": "customer.note_deleted"}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "customer.note_deleted", customerID, map[string]any{"note": noteID})
	})
}

// Backfill links orders placed before customers existed: it decrypts the
// order's customer, finds or creates the customer and recomputes the stats.
// decrypt returns the email, name, phone and address of an order.
func (s *Service) Backfill(ctx context.Context, decrypt func(tenant, orderID string, enc []byte) (email, name, phone, address string, err error)) (int, error) {
	rows, err := s.DB.System.Query(ctx, `SELECT tenant_id, id, customer, postal_code FROM orders WHERE customer_id IS NULL AND email_hash IS NULL ORDER BY created_at LIMIT 500`)
	if err != nil {
		return 0, err
	}
	type row struct {
		tenant, id, postal string
		enc                []byte
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		return x, r.Scan(&x.tenant, &x.id, &x.enc, &x.postal)
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, x := range list {
		email, name, phone, addr, derr := decrypt(x.tenant, x.id, x.enc)
		e, ok := NormalizeEmail(email)
		err := s.DB.Tx(ctx, x.tenant, func(tx pgx.Tx) error {
			if derr != nil || !ok {
				// Mark as looked at so the next round does not retry it forever.
				_, err := tx.Exec(ctx, `UPDATE orders SET email_hash='\x00' WHERE tenant_id=$1 AND id=$2`, x.tenant, x.id)
				return err
			}
			cid, _, err := UpsertByEmailTx(ctx, tx, s.Keys, x.tenant, e, name, phone)
			if err != nil {
				return err
			}
			h, _ := EmailHash(s.Keys, x.tenant, e)
			if _, err := tx.Exec(ctx, `UPDATE orders SET customer_id=$3, email_hash=$4 WHERE tenant_id=$1 AND id=$2`, x.tenant, x.id, cid, h); err != nil {
				return err
			}
			if err := SaveAddressIfNoneTx(ctx, tx, s.Keys, x.tenant, cid, name, phone, addr, x.postal); err != nil {
				return err
			}
			return RecalcStatsTx(ctx, tx, x.tenant, cid)
		})
		if err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// RecalcStatsTx recomputes the lifetime stats of a customer from its orders.
func RecalcStatsTx(ctx context.Context, tx pgx.Tx, tenant, customerID string) error {
	_, err := tx.Exec(ctx, `UPDATE customers c SET orders_count=s.n, total_spent=s.total, first_order_at=s.first, last_order_at=s.last,
			currency=COALESCE(s.cur, c.currency)
		FROM (SELECT count(*) FILTER (WHERE status IN ('paid','fulfilled')) AS n,
				COALESCE(sum(GREATEST(grand_total - refunded_total, 0)) FILTER (WHERE status IN ('paid','fulfilled','refunded')), 0) AS total,
				min(created_at) FILTER (WHERE status IN ('paid','fulfilled','refunded')) AS first,
				max(created_at) FILTER (WHERE status IN ('paid','fulfilled','refunded')) AS last,
				max(currency) AS cur
			FROM orders WHERE tenant_id=$1 AND customer_id=$2) s
		WHERE c.tenant_id=$1 AND c.id=$2`, tenant, customerID)
	return err
}
