package api

import (
	"context"
	"crypto/hmac"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/customers"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/notify"
	"github.com/helloghc/apexx/internal/store"
)

// Email campaigns: a message to a customer group, only to those who agreed
// to receive news, each with a link to unsubscribe. Orders placed after a
// click on the campaign's button carry it, so its sales are measured.

type mailCampaign struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	GroupID   string        `json:"groupId"`
	Subject   string        `json:"subject"`
	Blocks    notify.Blocks `json:"blocks"`
	Link      string        `json:"link"`
	Status    string        `json:"status"`
	SendAt    *time.Time    `json:"sendAt,omitempty"`
	SentCount int           `json:"sentCount"`
	SentAt    *time.Time    `json:"sentAt,omitempty"`
	CreatedAt time.Time     `json:"createdAt"`
	Orders    int           `json:"orders"`
	Revenue   int64         `json:"revenue"`
}

func (s *Server) routesMailCampaigns(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/email-campaigns", s.authed("customers", auth.Read, noStorefront(s.listMailCampaigns)))
	handle("POST /v1/email-campaigns", s.authed("customers", auth.Edit, s.saveMailCampaign))
	handle("PUT /v1/email-campaigns/{id}", s.authed("customers", auth.Edit, s.saveMailCampaign))
	handle("DELETE /v1/email-campaigns/{id}", s.authed("customers", auth.Edit, s.deleteMailCampaign))
	handle("POST /v1/email-campaigns/{id}/send", s.authed("customers", auth.Admin, s.sendMailCampaign))
	handle("GET /s/{tenant}/{site}/baja", s.unsubscribe)
}

func (s *Server) mailCampaigns(ctx context.Context, tenant, id string) ([]mailCampaign, error) {
	var out []mailCampaign
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT c.id, c.name, c.group_id, c.subject, c.blocks, c.link, c.status, c.send_at, c.sent_count, c.sent_at, c.created_at,
				COALESCE(o.n,0), COALESCE(o.rev,0)
			FROM email_campaigns c LEFT JOIN LATERAL (SELECT count(*) n, sum(GREATEST(grand_total - refunded_total,0)) rev FROM orders
				WHERE tenant_id=c.tenant_id AND email_campaign_id=c.id AND status IN ('paid','fulfilled','refunded')) o ON true
			WHERE c.tenant_id=$1 AND ($2='' OR c.id=$2) ORDER BY c.created_at DESC`, tenant, id)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (mailCampaign, error) {
			var c mailCampaign
			var b []byte
			err := r.Scan(&c.ID, &c.Name, &c.GroupID, &c.Subject, &b, &c.Link, &c.Status, &c.SendAt, &c.SentCount, &c.SentAt, &c.CreatedAt, &c.Orders, &c.Revenue)
			_ = json.Unmarshal(b, &c.Blocks)
			return c, err
		})
		return err
	})
	return out, err
}

func (s *Server) listMailCampaigns(w http.ResponseWriter, r *http.Request) {
	list, err := s.mailCampaigns(r.Context(), principal(r).Tenant, "")
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if list == nil {
		list = []mailCampaign{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) saveMailCampaign(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var in mailCampaign
	if !decode(w, r, &in) {
		return
	}
	in.Name, in.Subject, in.Link = strings.TrimSpace(in.Name), strings.TrimSpace(in.Subject), strings.TrimSpace(in.Link)
	var errs []apierr.FieldError
	if in.Name == "" || len([]rune(in.Name)) > 80 {
		errs = append(errs, apierr.FieldError{Field: "name", Code: "invalid", Message: "Escribe un nombre de hasta 80 caracteres"})
	}
	if in.Subject == "" || len([]rune(in.Subject)) > 140 {
		errs = append(errs, apierr.FieldError{Field: "subject", Code: "invalid", Message: "Escribe un asunto de hasta 140 caracteres"})
	}
	if len([]rune(in.Blocks.Body)) > 5000 || len([]rune(in.Blocks.Heading)) > 140 || len([]rune(in.Blocks.CTA)) > 40 {
		errs = append(errs, apierr.FieldError{Field: "blocks", Code: "too_long", Message: "Título de hasta 140, texto de hasta 5,000 y botón de hasta 40 caracteres"})
	}
	if in.Link != "" && (!strings.HasPrefix(in.Link, "/") || strings.HasPrefix(in.Link, "//") || len(in.Link) > 200) {
		errs = append(errs, apierr.FieldError{Field: "link", Code: "invalid", Message: "El botón lleva a una página de tu tienda, por ejemplo /c/ofertas"})
	}
	if in.GroupID != "" {
		if _, err := s.Customers.Group(r.Context(), pr.Tenant, in.GroupID); err != nil {
			errs = append(errs, apierr.FieldError{Field: "groupId", Code: "unknown", Message: "El grupo no existe"})
		}
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "Revisa la campaña", errs...)
		return
	}
	in.Blocks.Footer = strings.TrimSpace(in.Blocks.Footer)
	blocks, _ := json.Marshal(in.Blocks)
	id := r.PathValue("id")
	err := s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		if id == "" {
			id = newRandomID("cmp_")
			_, err := tx.Exec(r.Context(), `INSERT INTO email_campaigns (tenant_id, id, name, group_id, subject, blocks, link) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
				pr.Tenant, id, in.Name, in.GroupID, in.Subject, blocks, in.Link)
			return err
		}
		ct, err := tx.Exec(r.Context(), `UPDATE email_campaigns SET name=$3, group_id=$4, subject=$5, blocks=$6, link=$7 WHERE tenant_id=$1 AND id=$2 AND status='draft'`,
			pr.Tenant, id, in.Name, in.GroupID, in.Subject, blocks, in.Link)
		if err == nil && ct.RowsAffected() == 0 {
			return store.ErrNotFound
		}
		return err
	})
	if errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "Campaña no encontrada o ya enviada")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	list, _ := s.mailCampaigns(r.Context(), pr.Tenant, id)
	httpx.JSON(w, r, http.StatusOK, list[0])
}

func (s *Server) deleteMailCampaign(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var n int64
	err := s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(r.Context(), `DELETE FROM email_campaigns WHERE tenant_id=$1 AND id=$2 AND status IN ('draft','scheduled')`, pr.Tenant, r.PathValue("id"))
		n = ct.RowsAffected()
		return err
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if n == 0 {
		apierr.NotFound(w, "Solo se borra una campaña que no se ha enviado")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sendCampaign schedules the campaign (now when no time is given).
func (s *Server) sendMailCampaign(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var in struct {
		At *time.Time `json:"at"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in)
	at := time.Now()
	if in.At != nil {
		at = *in.At
	}
	var n int64
	err := s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(r.Context(), `UPDATE email_campaigns SET status='scheduled', send_at=$3 WHERE tenant_id=$1 AND id=$2 AND status IN ('draft','scheduled')`, pr.Tenant, r.PathValue("id"), at)
		n = ct.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
		return store.Audit(r.Context(), tx, pr.Tenant, pr.Actor(), "campaign.scheduled", r.PathValue("id"), map[string]any{"at": at})
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if n == 0 {
		apierr.NotFound(w, "Campaña no encontrada o ya enviada")
		return
	}
	list, _ := s.mailCampaigns(r.Context(), pr.Tenant, r.PathValue("id"))
	httpx.JSON(w, r, http.StatusAccepted, list[0])
}

func (s *Server) unsubSig(tenant, customer string) string {
	b, err := s.Keys.Blind(tenant, "unsubscribe", customer)
	if err != nil {
		return ""
	}
	return hex.EncodeToString(b[:12])
}

// RunCampaigns sends the due campaigns: each customer of the group with
// consent and an email gets one message (sent once, even after a restart).
func (s *Server) RunCampaigns(ctx context.Context) (int, error) {
	rows, err := s.DB.System.Query(ctx, `WITH due AS (SELECT tenant_id, id FROM email_campaigns WHERE status IN ('scheduled','sending') AND send_at <= now() LIMIT 5 FOR UPDATE SKIP LOCKED)
		UPDATE email_campaigns c SET status='sending' FROM due WHERE c.tenant_id=due.tenant_id AND c.id=due.id RETURNING c.tenant_id, c.id`)
	if err != nil {
		return 0, err
	}
	type due struct{ tenant, id string }
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.tenant, &d.id); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, d)
	}
	rows.Close()
	for _, d := range list {
		if err := s.runCampaign(ctx, d.tenant, d.id); err != nil && s.Log != nil {
			s.Log.Error("campaign", "tenant", d.tenant, "campaign", d.id, "err", err)
		}
	}
	return len(list), nil
}

func (s *Server) runCampaign(ctx context.Context, tenant, id string) error {
	cs, err := s.mailCampaigns(ctx, tenant, id)
	if err != nil || len(cs) == 0 {
		return err
	}
	c := cs[0]
	base := strings.TrimRight(s.Orders.PublicURL, "/") + "/s/" + tenant + "/main"
	link := base + "/index"
	if c.Link != "" {
		link = base + c.Link
	}
	sep := "?"
	if strings.Contains(link, "?") {
		sep = "&"
	}
	link += sep + "cmp=" + url.QueryEscape(c.ID)
	sent := 0
	cursor := ""
	for {
		page, next, _, err := s.Customers.List(ctx, tenant, customers.ListFilter{Group: c.GroupID, Consent: "yes", Limit: 200, Cursor: cursor}, true)
		if err != nil {
			return err
		}
		err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
			for _, cu := range page {
				if cu.Email == "" {
					continue
				}
				unsub := base + "/baja?c=" + url.QueryEscape(cu.ID) + "&t=" + s.unsubSig(tenant, cu.ID)
				msgID, err := s.Notify.EnqueueTx(ctx, tx, tenant, notify.Message{Template: notify.TplCampaign, To: cu.Email, Name: cu.Name, CustomerID: cu.ID,
					DedupeKey: "campaign:" + c.ID + ":" + cu.ID, Data: notify.Data{Vars: map[string]string{"cliente.nombre": firstWord(cu.Name)}, ActionURL: link,
						Override: &notify.Override{Subject: c.Subject, Blocks: c.Blocks}, Unsubscribe: unsub}})
				if err != nil {
					return err
				}
				if msgID != "" {
					sent++
				}
			}
			// Count what really exists, so a resumed run never lowers or doubles it.
			_, err := tx.Exec(ctx, `UPDATE email_campaigns SET sent_count=(SELECT count(*) FROM notification_messages WHERE tenant_id=$1 AND dedupe_key LIKE 'campaign:' || $2 || ':%')
				WHERE tenant_id=$1 AND id=$2`, tenant, id)
			return err
		})
		if err != nil {
			return err
		}
		if next == "" || len(page) == 0 {
			break
		}
		cursor = next
	}
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE email_campaigns SET status='sent', sent_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, id)
		return err
	})
}

func firstWord(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ' '); i > 0 {
		return s[:i]
	}
	return s
}

// unsubscribe turns off marketing email for the customer of a signed link.
func (s *Server) unsubscribe(w http.ResponseWriter, r *http.Request) {
	t, pd, ok := s.shopContext(w, r)
	if !ok {
		return
	}
	cid, sig := r.URL.Query().Get("c"), r.URL.Query().Get("t")
	pd.Title, pd.NoIndex = "Suscripción", true
	msg := "Este enlace no es válido."
	if cid != "" && hmac.Equal([]byte(sig), []byte(s.unsubSig(t.ID, cid))) {
		off := false
		if _, err := s.Customers.Update(r.Context(), t.ID, "customer:"+cid, cid, customers.Input{MarketingConsent: &off}, 0, "unsubscribe_link"); err == nil {
			msg = "Listo: ya no te enviaremos correos de noticias y promociones. Seguirás recibiendo los de tus pedidos."
		}
	}
	s.renderShop(w, "notice", struct {
		pageData
		Heading, Message string
	}{pd, "Suscripción", msg}, false)
}
