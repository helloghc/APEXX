// Package notify is the transactional email system: built-in templates that a
// store can reword, per-store sender and provider (SMTP, SendGrid or the
// development outbox), and a queue with retries.
//
// A message is inserted with EnqueueTx in the same transaction as the change
// that caused it, together with an outbox event, so an email is never sent for
// a change that rolled back and never lost for one that committed. A worker
// (Send) delivers due messages and retries failures with exponential backoff.
// Recipient, variables and the rendered copy are encrypted with the tenant key.
package notify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/crypto"
	"github.com/helloghc/apexx/internal/store"
)

// Topic of the outbox events of this package.
const Topic = "notification.changed"

// MaxAttempts before a message is parked as dead.
const MaxAttempts = 6

// Errors.
var (
	ErrNotFound = errors.New("notify: not found")
	ErrInvalid  = errors.New("notify: invalid")
)

// Service sends and manages notifications.
type Service struct {
	DB           *store.DB
	Keys         *crypto.Keyring
	AllowPrivate bool // allow SMTP relays on private addresses (development)
	// ProviderFor overrides provider construction (tests).
	ProviderFor func(tenant string, st Settings) (Provider, error)
	// Backoff overrides the retry delay (tests).
	Backoff func(attempt int) time.Duration
	// Phrases translates fixed phrases into a buyer's language with the
	// store's dictionaries (its own translations first); nil keeps Spanish.
	Phrases func(tenant, lang string) func(string) string
}

// Settings is the per-store configuration.
type Settings struct {
	Provider            string     `json:"provider"`
	SenderName          string     `json:"senderName"`
	SenderEmail         string     `json:"senderEmail"`
	ReplyTo             string     `json:"replyTo"`
	Brand               Brand      `json:"brand"`
	CartReminderMinutes int        `json:"cartReminderMinutes"`
	SMTP                SMTPConfig `json:"smtp"`
	HasSMTPPassword     bool       `json:"hasSmtpPassword"`
	SendGridKey         string     `json:"sendgridKey,omitempty"` // write-only
	HasSendGridKey      bool       `json:"hasSendgridKey"`
}

type secretConfig struct {
	SMTP        SMTPConfig `json:"smtp"`
	SendGridKey string     `json:"sendgridKey"`
}

func newID(prefix string) string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func validEmail(s string) bool {
	if len(s) > 254 || strings.ContainsAny(s, " \r\n<>") {
		return false
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s && strings.Contains(s[strings.LastIndex(s, "@")+1:], ".")
}

// settingsTx loads settings with secrets (internal use).
func (s *Service) settingsTx(ctx context.Context, tx pgx.Tx, tenant string) (Settings, secretConfig, error) {
	st := Settings{Provider: "outbox", CartReminderMinutes: 60}
	var sec secretConfig
	var enc, brand []byte
	err := tx.QueryRow(ctx, `SELECT provider, sender_name, sender_email, reply_to, config, brand, cart_reminder_minutes FROM notification_settings WHERE tenant_id=$1`, tenant).
		Scan(&st.Provider, &st.SenderName, &st.SenderEmail, &st.ReplyTo, &enc, &brand, &st.CartReminderMinutes)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return st, sec, err
	}
	_ = json.Unmarshal(brand, &st.Brand)
	if len(enc) > 0 {
		plain, err := s.Keys.Decrypt(tenant, enc, "notification_settings")
		if err != nil {
			return st, sec, err
		}
		_ = json.Unmarshal(plain, &sec)
	}
	if st.Brand.StoreName == "" {
		var name string
		_ = tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id=$1`, tenant).Scan(&name)
		if name == "" {
			name = tenant
		}
		st.Brand.StoreName = name
	}
	if st.Brand.Color == "" {
		st.Brand.Color = "#1d3fae"
	}
	st.SMTP = sec.SMTP
	st.SMTP.Password = ""
	st.HasSMTPPassword = sec.SMTP.Password != ""
	st.HasSendGridKey = sec.SendGridKey != ""
	return st, sec, nil
}

// GetSettings returns the settings without secrets.
func (s *Service) GetSettings(ctx context.Context, tenant string) (Settings, error) {
	var st Settings
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var e error
		st, _, e = s.settingsTx(ctx, tx, tenant)
		return e
	})
	return st, err
}

// FieldError is a validation error on one field.
type FieldError struct{ Field, Message string }

func (e FieldError) Error() string { return e.Field + ": " + e.Message }

// SaveSettings validates and stores the settings. Empty secrets keep the stored ones.
func (s *Service) SaveSettings(ctx context.Context, tenant, actor string, in Settings) (Settings, error) {
	in.SenderName, in.SenderEmail, in.ReplyTo = strings.TrimSpace(in.SenderName), strings.TrimSpace(in.SenderEmail), strings.TrimSpace(in.ReplyTo)
	switch in.Provider {
	case "outbox", "smtp", "sendgrid":
	default:
		return in, FieldError{"provider", "Elige outbox, smtp o sendgrid"}
	}
	if len(in.SenderName) > 80 || strings.ContainsAny(in.SenderName, "\r\n") {
		return in, FieldError{"senderName", "Usa un nombre de hasta 80 caracteres"}
	}
	if in.SenderEmail != "" && !validEmail(in.SenderEmail) {
		return in, FieldError{"senderEmail", "Correo del remitente inválido"}
	}
	if in.ReplyTo != "" && !validEmail(in.ReplyTo) {
		return in, FieldError{"replyTo", "Correo de respuesta inválido"}
	}
	if in.Provider != "outbox" && in.SenderEmail == "" {
		return in, FieldError{"senderEmail", "Escribe el correo del remitente para enviar correos reales"}
	}
	if in.Brand.Color != "" && !hexColor.MatchString(in.Brand.Color) {
		return in, FieldError{"brand.color", "Usa un color hexadecimal como #1d3fae"}
	}
	if in.Brand.LogoURL != "" && !strings.HasPrefix(in.Brand.LogoURL, "https://") {
		return in, FieldError{"brand.logoUrl", "El logo debe ser una dirección https://"}
	}
	if len(in.Brand.StoreName) > 80 || len(in.Brand.Footer) > 300 || len(in.Brand.LogoURL) > 500 {
		return in, FieldError{"brand.storeName", "Texto demasiado largo"}
	}
	if in.CartReminderMinutes == 0 {
		in.CartReminderMinutes = 60
	}
	if in.CartReminderMinutes < 15 || in.CartReminderMinutes > 10080 {
		return in, FieldError{"cartReminderMinutes", "Entre 15 minutos y 7 días"}
	}
	var out Settings
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		_, sec, err := s.settingsTx(ctx, tx, tenant)
		if err != nil {
			return err
		}
		if in.Provider == "smtp" {
			c := in.SMTP
			c.Host = strings.TrimSpace(c.Host)
			if c.Host == "" || len(c.Host) > 200 || strings.ContainsAny(c.Host, " /:@") {
				return FieldError{"smtp.host", "Escribe el servidor SMTP, por ejemplo smtp.proveedor.com"}
			}
			if c.Port == 0 {
				c.Port = 587
			}
			if c.Port != 25 && c.Port != 465 && c.Port != 587 && c.Port != 2525 && !s.AllowPrivate {
				return FieldError{"smtp.port", "Puerto SMTP: 25, 465, 587 o 2525"}
			}
			if c.Password == "" {
				c.Password = sec.SMTP.Password
			}
			sec.SMTP = c
		}
		if in.Provider == "sendgrid" {
			if in.SendGridKey != "" {
				sec.SendGridKey = strings.TrimSpace(in.SendGridKey)
			}
			if sec.SendGridKey == "" {
				return FieldError{"sendgridKey", "Pega la API key de SendGrid"}
			}
		}
		raw, _ := json.Marshal(sec)
		enc, err := s.Keys.Encrypt(tenant, raw, "notification_settings")
		if err != nil {
			return err
		}
		brand, _ := json.Marshal(in.Brand)
		if _, err := tx.Exec(ctx, `INSERT INTO notification_settings (tenant_id, provider, sender_name, sender_email, reply_to, config, brand, cart_reminder_minutes)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (tenant_id) DO UPDATE SET provider=EXCLUDED.provider, sender_name=EXCLUDED.sender_name,
			sender_email=EXCLUDED.sender_email, reply_to=EXCLUDED.reply_to, config=EXCLUDED.config, brand=EXCLUDED.brand,
			cart_reminder_minutes=EXCLUDED.cart_reminder_minutes, updated_at=now()`,
			tenant, in.Provider, in.SenderName, in.SenderEmail, in.ReplyTo, enc, brand, in.CartReminderMinutes); err != nil {
			return err
		}
		if err := store.Emit(ctx, tx, tenant, Topic, "settings", map[string]any{"type": "notification.settings_changed"}); err != nil {
			return err
		}
		if err := store.Audit(ctx, tx, tenant, actor, "notification.settings", "settings", map[string]any{"provider": in.Provider}); err != nil {
			return err
		}
		out, _, err = s.settingsTx(ctx, tx, tenant)
		return err
	})
	return out, err
}

// Template is a built-in template merged with the store's overrides.
type Template struct {
	Definition
	Enabled    bool       `json:"enabled"`
	Customized bool       `json:"customized"`
	UpdatedAt  *time.Time `json:"updatedAt,omitempty"`
	Default    struct {
		Subject string `json:"subject"`
		Blocks  Blocks `json:"blocks"`
	} `json:"default"`
}

func (s *Service) templateTx(ctx context.Context, tx pgx.Tx, tenant, key string) (Template, error) {
	d, ok := DefinitionOf(key)
	if !ok {
		return Template{}, ErrNotFound
	}
	t := Template{Definition: d, Enabled: !d.Optional}
	t.Default.Subject, t.Default.Blocks = d.Subject, d.Blocks
	var blocks []byte
	var at time.Time
	err := tx.QueryRow(ctx, `SELECT subject, blocks, enabled, updated_at FROM notification_templates WHERE tenant_id=$1 AND key=$2`, tenant, key).
		Scan(&t.Subject, &blocks, &t.Enabled, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, nil
	}
	if err != nil {
		return t, err
	}
	_ = json.Unmarshal(blocks, &t.Blocks)
	t.Customized = t.Subject != d.Subject || t.Blocks != d.Blocks
	t.UpdatedAt = &at
	return t, nil
}

// Templates lists every template with the store's overrides.
func (s *Service) Templates(ctx context.Context, tenant string) ([]Template, error) {
	var out []Template
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		for _, d := range Definitions {
			t, err := s.templateTx(ctx, tx, tenant, d.Key)
			if err != nil {
				return err
			}
			out = append(out, t)
		}
		return nil
	})
	return out, err
}

// ValidateBlocks checks an edited template.
func ValidateBlocks(d Definition, subject string, b Blocks) error {
	if strings.TrimSpace(subject) == "" || len(subject) > 200 || strings.ContainsAny(subject, "\r\n") {
		return FieldError{"subject", "Escribe un asunto de una línea (hasta 200 caracteres)"}
	}
	if strings.TrimSpace(b.Body) == "" || len(b.Body) > 5000 {
		return FieldError{"body", "Escribe el mensaje (hasta 5,000 caracteres)"}
	}
	if len(b.Heading) > 200 || len(b.CTA) > 60 || len(b.Footer) > 500 {
		return FieldError{"heading", "Título, botón o pie demasiado largos"}
	}
	if unk := UnknownVariables(d, subject, b); len(unk) > 0 {
		return FieldError{"body", "Variables que esta plantilla no tiene: {{" + strings.Join(unk, "}}, {{") + "}}"}
	}
	if d.Key == TplAccessCode && !strings.Contains(b.Body+subject, "codigo") {
		return FieldError{"body", "La plantilla debe incluir {{codigo}}"}
	}
	return nil
}

// SaveTemplate stores an override. reset restores the built-in copy.
func (s *Service) SaveTemplate(ctx context.Context, tenant, actor, key, subject string, b Blocks, enabled, reset bool) (Template, error) {
	d, ok := DefinitionOf(key)
	if !ok {
		return Template{}, ErrNotFound
	}
	if reset {
		subject, b = d.Subject, d.Blocks
	}
	if err := ValidateBlocks(d, subject, b); err != nil {
		return Template{}, err
	}
	if !d.Optional {
		enabled = true // transactional emails cannot be switched off
	}
	var out Template
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		raw, _ := json.Marshal(b)
		if _, err := tx.Exec(ctx, `INSERT INTO notification_templates (tenant_id, key, subject, blocks, enabled) VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (tenant_id, key) DO UPDATE SET subject=EXCLUDED.subject, blocks=EXCLUDED.blocks, enabled=EXCLUDED.enabled, updated_at=now()`,
			tenant, key, strings.TrimSpace(subject), raw, enabled); err != nil {
			return err
		}
		if err := store.Emit(ctx, tx, tenant, Topic, key, map[string]any{"type": "notification.template_changed"}); err != nil {
			return err
		}
		if err := store.Audit(ctx, tx, tenant, actor, "notification.template", key, map[string]any{"enabled": enabled, "reset": reset}); err != nil {
			return err
		}
		var err error
		out, err = s.templateTx(ctx, tx, tenant, key)
		return err
	})
	return out, err
}

// Preview renders a template (optionally with unsaved edits) using sample data.
func (s *Service) Preview(ctx context.Context, tenant, key string, subject *string, b *Blocks, brand *Brand, lang ...string) (Rendered, error) {
	var out Rendered
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		t, err := s.templateTx(ctx, tx, tenant, key)
		if err != nil {
			return err
		}
		st, _, err := s.settingsTx(ctx, tx, tenant)
		if err != nil {
			return err
		}
		if subject != nil {
			t.Subject = *subject
		}
		if b != nil {
			t.Blocks = *b
		}
		br := st.Brand
		if brand != nil {
			if brand.StoreName != "" {
				br.StoreName = brand.StoreName
			}
			if hexColor.MatchString(brand.Color) {
				br.Color = brand.Color
			}
			br.LogoURL, br.Footer = brand.LogoURL, brand.Footer
		}
		d := Sample(t.Definition)
		l := "es"
		if len(lang) > 0 && lang[0] != "" {
			l = lang[0]
		}
		s.localize(tenant, l, &t, &d)
		out = RenderLang(t.Subject, t.Blocks, d, br, t.ShowItems, l)
		return nil
	})
	return out, err
}

// Message is a notification to enqueue.
type Message struct {
	Template   string
	To         string
	Name       string
	Data       Data
	OrderID    string
	CustomerID string
	DedupeKey  string // same key = sent once
	Force      bool   // send even when the (optional) template is switched off
	// Lang is the buyer's language; empty takes the order's, else the
	// customer's, else Spanish.
	Lang string
}

type payload struct {
	Lang     string    `json:"lang,omitempty"`
	To       string    `json:"to"`
	Name     string    `json:"name"`
	Data     Data      `json:"data"`
	Rendered *Rendered `json:"rendered,omitempty"`
}

// EnqueueTx queues m inside the caller's transaction. It returns "" when the
// template is switched off or the dedupe key was already used.
func (s *Service) EnqueueTx(ctx context.Context, tx pgx.Tx, tenant string, m Message) (string, error) {
	if s == nil {
		return "", nil
	}
	to := strings.ToLower(strings.TrimSpace(m.To))
	if !validEmail(to) {
		return "", nil // nothing to send to; never fail the business transaction for it
	}
	t, err := s.templateTx(ctx, tx, tenant, m.Template)
	if err != nil {
		return "", err
	}
	if !t.Enabled && !m.Force {
		return "", nil
	}
	id := newID("msg_")
	lang := m.Lang
	if lang == "" {
		lang = langFrom(ctx)
	}
	if lang == "" && m.OrderID != "" {
		_ = tx.QueryRow(ctx, `SELECT lang FROM orders WHERE tenant_id=$1 AND id=$2`, tenant, m.OrderID).Scan(&lang)
	}
	if lang == "" && m.CustomerID != "" {
		_ = tx.QueryRow(ctx, `SELECT lang FROM customers WHERE tenant_id=$1 AND id=$2`, tenant, m.CustomerID).Scan(&lang)
	}
	raw, _ := json.Marshal(payload{Lang: lang, To: to, Name: m.Name, Data: m.Data})
	enc, err := s.Keys.Encrypt(tenant, raw, id)
	if err != nil {
		return "", err
	}
	hash, err := s.Keys.Blind(tenant, "email", to)
	if err != nil {
		return "", err
	}
	var dedupe *string
	if m.DedupeKey != "" {
		dedupe = &m.DedupeKey
	}
	ct, err := tx.Exec(ctx, `INSERT INTO notification_messages (tenant_id, id, template, to_hash, payload, order_id, customer_id, dedupe_key)
		VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),$8) ON CONFLICT DO NOTHING`, tenant, id, m.Template, hash, enc, m.OrderID, m.CustomerID, dedupe)
	if err != nil {
		return "", err
	}
	if ct.RowsAffected() == 0 {
		return "", nil
	}
	return id, store.Emit(ctx, tx, tenant, Topic, id, map[string]any{"type": "notification.queued", "template": m.Template})
}

// Enqueue queues m in its own transaction (test sends, manual reminders).
func (s *Service) Enqueue(ctx context.Context, tenant string, m Message) (string, error) {
	var id string
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var e error
		id, e = s.EnqueueTx(ctx, tx, tenant, m)
		return e
	})
	return id, err
}

func (s *Service) provider(tenant string, st Settings, sec secretConfig) (Provider, error) {
	if s.ProviderFor != nil {
		return s.ProviderFor(tenant, st)
	}
	switch st.Provider {
	case "smtp":
		return &SMTP{Config: sec.SMTP, AllowPrivate: s.AllowPrivate}, nil
	case "sendgrid":
		return &SendGrid{APIKey: sec.SendGridKey}, nil
	}
	return Outbox{}, nil
}

func (s *Service) backoff(attempt int) time.Duration {
	if s.Backoff != nil {
		return s.Backoff(attempt)
	}
	return 30 * time.Second * time.Duration(1<<min(attempt, 8))
}

// Send delivers due messages. Several nodes can run it: rows are leased with
// SKIP LOCKED, and the message id travels to the provider as a dedupe hint.
func (s *Service) Send(ctx context.Context) (int, error) {
	rows, err := s.DB.System.Query(ctx, `WITH due AS (
			SELECT tenant_id, id FROM notification_messages WHERE status IN ('queued','failed') AND next_attempt <= now()
			ORDER BY next_attempt LIMIT 25 FOR UPDATE SKIP LOCKED)
		UPDATE notification_messages m SET next_attempt = now() + interval '2 minutes'
		FROM due WHERE m.tenant_id=due.tenant_id AND m.id=due.id RETURNING m.tenant_id, m.id`)
	if err != nil {
		return 0, err
	}
	type ti struct{ t, id string }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ti, error) {
		var x ti
		return x, r.Scan(&x.t, &x.id)
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, x := range list {
		ok, err := s.sendOne(ctx, x.t, x.id)
		if err != nil {
			return n, err
		}
		if ok {
			n++
		}
	}
	return n, nil
}

func (s *Service) sendOne(ctx context.Context, tenant, id string) (bool, error) {
	var (
		p        payload
		st       Settings
		sec      secretConfig
		tpl      Template
		attempts int
	)
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var enc []byte
		var key string
		if err := tx.QueryRow(ctx, `SELECT template, payload, attempts FROM notification_messages WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&key, &enc, &attempts); err != nil {
			return err
		}
		plain, err := s.Keys.Decrypt(tenant, enc, id)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(plain, &p); err != nil {
			return err
		}
		if st, sec, err = s.settingsTx(ctx, tx, tenant); err != nil {
			return err
		}
		tpl, err = s.templateTx(ctx, tx, tenant, key)
		return err
	})
	if err != nil {
		// Unreadable message (key missing, template removed): park it instead of looping.
		_ = s.finish(ctx, tenant, id, "dead", "", "", truncate(err.Error(), 300), attempts+1, nil)
		return false, nil
	}
	if o := p.Data.Override; o != nil {
		tpl.Subject, tpl.Blocks = o.Subject, o.Blocks
	}
	s.localize(tenant, p.Lang, &tpl, &p.Data)
	r := RenderLang(tpl.Subject, tpl.Blocks, p.Data, st.Brand, tpl.ShowItems, p.Lang)
	prov, err := s.provider(tenant, st, sec)
	var ref string
	if err == nil {
		from := st.SenderEmail
		if from == "" {
			from = "no-reply@" + tenant + ".apexx.local"
		}
		name := st.SenderName
		if name == "" {
			name = st.Brand.StoreName
		}
		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		ref, err = prov.Send(sctx, Email{FromName: name, FromEmail: from, ReplyTo: st.ReplyTo, ToName: p.Name, ToEmail: p.To,
			Subject: r.Subject, HTML: r.HTML, Text: r.Text, MessageID: id})
		cancel()
	}
	provName := st.Provider
	if prov != nil {
		provName = prov.Name()
	}
	if err != nil {
		status := "failed"
		if attempts+1 >= MaxAttempts {
			status = "dead"
		}
		return false, s.finish(ctx, tenant, id, status, provName, "", truncate(err.Error(), 300), attempts+1, nil)
	}
	// Keep the rendered copy for the log. A one-time code that really left the
	// server is not kept; the development outbox keeps it so it can be read.
	if tpl.Key == TplAccessCode && provName != "outbox" {
		p.Data.Vars = map[string]string{"codigo": "••••••"}
		r = RenderLang(tpl.Subject, tpl.Blocks, p.Data, st.Brand, tpl.ShowItems, p.Lang)
	}
	p.Rendered = &r
	return true, s.finish(ctx, tenant, id, "sent", provName, ref, "", attempts+1, &p)
}

func (s *Service) finish(ctx context.Context, tenant, id, status, provider, ref, lastErr string, attempts int, p *payload) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		next := time.Now().Add(s.backoff(attempts - 1))
		if p != nil {
			raw, _ := json.Marshal(p)
			enc, err := s.Keys.Encrypt(tenant, raw, id)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE notification_messages SET payload=$3 WHERE tenant_id=$1 AND id=$2`, tenant, id, enc); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE notification_messages SET status=$3, provider=$4, provider_ref=$5, last_error=$6, attempts=$7, next_attempt=$8,
			sent_at=CASE WHEN $3='sent' THEN now() ELSE sent_at END WHERE tenant_id=$1 AND id=$2`, tenant, id, status, provider, ref, lastErr, attempts, next); err != nil {
			return err
		}
		return store.Emit(ctx, tx, tenant, Topic, id, map[string]any{"type": "notification." + status, "attempts": attempts})
	})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Entry is one row of the message log.
type Entry struct {
	ID          string     `json:"id"`
	Template    string     `json:"template"`
	Status      string     `json:"status"`
	To          string     `json:"to"`
	Subject     string     `json:"subject"`
	Provider    string     `json:"provider"`
	Attempts    int        `json:"attempts"`
	LastError   string     `json:"lastError,omitempty"`
	NextAttempt *time.Time `json:"nextAttempt,omitempty"`
	OrderID     string     `json:"orderId,omitempty"`
	CustomerID  string     `json:"customerId,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	SentAt      *time.Time `json:"sentAt,omitempty"`
	HTML        string     `json:"html,omitempty"`
	Text        string     `json:"text,omitempty"`
}

// MaskEmail hides most of the local part: "ana.torres@x.com" → "a•••@x.com".
func MaskEmail(e string) string {
	i := strings.LastIndex(e, "@")
	if i < 1 {
		return "•••"
	}
	return e[:1] + "•••" + e[i:]
}

// ListFilter filters the message log.
type ListFilter struct {
	Status, Template, Email, OrderID, CustomerID string
	Cursor                                       string // "createdAt|id" of the last row
	Limit                                        int
}

const entryCols = `id, template, status, payload, provider, attempts, last_error, next_attempt, COALESCE(order_id,''), COALESCE(customer_id,''), created_at, sent_at`

func (s *Service) scanEntry(tenant string, r pgx.Row, full, pii bool) (Entry, error) {
	var e Entry
	var enc []byte
	var next time.Time
	if err := r.Scan(&e.ID, &e.Template, &e.Status, &enc, &e.Provider, &e.Attempts, &e.LastError, &next, &e.OrderID, &e.CustomerID, &e.CreatedAt, &e.SentAt); err != nil {
		return e, err
	}
	if e.Status == "queued" || e.Status == "failed" {
		e.NextAttempt = &next
	}
	plain, err := s.Keys.Decrypt(tenant, enc, e.ID)
	if err != nil {
		return e, nil // unreadable payload: still list the row
	}
	var p payload
	_ = json.Unmarshal(plain, &p)
	e.To = p.To
	if !pii {
		e.To = MaskEmail(p.To)
	}
	if p.Rendered != nil {
		e.Subject = p.Rendered.Subject
		if full && pii {
			e.HTML, e.Text = p.Rendered.HTML, p.Rendered.Text
		}
	}
	return e, nil
}

// List returns the message log, newest first.
func (s *Service) List(ctx context.Context, tenant string, f ListFilter, pii bool) ([]Entry, string, error) {
	if f.Limit < 1 || f.Limit > 200 {
		f.Limit = 50
	}
	q := `SELECT ` + entryCols + ` FROM notification_messages WHERE tenant_id=$1`
	args := []any{tenant}
	add := func(cond string, v any) {
		args = append(args, v)
		q += fmt.Sprintf(" AND "+cond, len(args))
	}
	if f.Status != "" {
		add("status=$%d", f.Status)
	}
	if f.Template != "" {
		add("template=$%d", f.Template)
	}
	if f.OrderID != "" {
		add("order_id=$%d", f.OrderID)
	}
	if f.CustomerID != "" {
		add("customer_id=$%d", f.CustomerID)
	}
	if f.Email != "" {
		h, err := s.Keys.Blind(tenant, "email", strings.ToLower(strings.TrimSpace(f.Email)))
		if err != nil {
			return nil, "", err
		}
		add("to_hash=$%d", h)
	}
	if f.Cursor != "" {
		at, id, ok := strings.Cut(f.Cursor, "|")
		ts, err := time.Parse(time.RFC3339Nano, at)
		if !ok || err != nil {
			return nil, "", ErrInvalid
		}
		args = append(args, ts, id)
		q += fmt.Sprintf(" AND (created_at, id) < ($%d, $%d)", len(args)-1, len(args))
	}
	args = append(args, f.Limit+1)
	q += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", len(args))
	var out []Entry
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e, err := s.scanEntry(tenant, rows, false, pii)
			if err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	next := ""
	if len(out) > f.Limit {
		out = out[:f.Limit]
		l := out[len(out)-1]
		next = l.CreatedAt.Format(time.RFC3339Nano) + "|" + l.ID
	}
	return out, next, err
}

// Get returns one message with its rendered copy.
func (s *Service) Get(ctx context.Context, tenant, id string, pii bool) (Entry, error) {
	var e Entry
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		e, err = s.scanEntry(tenant, tx.QueryRow(ctx, `SELECT `+entryCols+` FROM notification_messages WHERE tenant_id=$1 AND id=$2`, tenant, id), true, pii)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return e, err
}

// Retry puts a failed or dead message back in the queue.
func (s *Service) Retry(ctx context.Context, tenant, actor, id string) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `UPDATE notification_messages SET status='queued', attempts=0, last_error='', next_attempt=now()
			WHERE tenant_id=$1 AND id=$2 AND status IN ('failed','dead')`, tenant, id)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		if err := store.Emit(ctx, tx, tenant, Topic, id, map[string]any{"type": "notification.queued", "retry": true}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "notification.retry", id, map[string]any{})
	})
}

// Stats counts messages by status for the last 30 days.
func (s *Service) Stats(ctx context.Context, tenant string) (map[string]int, error) {
	out := map[string]int{}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT status, count(*) FROM notification_messages WHERE tenant_id=$1 AND created_at > now() - interval '30 days' GROUP BY status`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var st string
			var n int
			if err := rows.Scan(&st, &n); err != nil {
				return err
			}
			out[st] = n
		}
		return rows.Err()
	})
	return out, err
}
