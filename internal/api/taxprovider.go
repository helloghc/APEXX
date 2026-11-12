package api

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/logistics"
	"github.com/helloghc/apexx/internal/taxprovider"
)

// TaxService is a store's external tax engine (an app speaking Apexx's
// signed contract). Off, or not answering in time: Apexx's own tax rules.
type TaxService struct {
	Active   bool   `json:"active"`
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
}

var (
	taxCache      = &taxprovider.Cache{}
	taxHTTPClient *http.Client // tests
	taxCfgMu      sync.Mutex
	taxCfg        = map[string]taxCfgEntry{}
)

type taxCfgEntry struct {
	cfg   TaxService
	until time.Time
}

func (s *Server) routesTaxService(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/settings/tax-service", s.authed("settings", auth.Read, s.getTaxService))
	handle("PUT /v1/settings/tax-service", s.authed("settings", auth.Admin, s.putTaxService))
}

// taxService reads the store's setting, cached 30 s (the cart asks often).
func (s *Server) taxService(tenant string) TaxService {
	taxCfgMu.Lock()
	e, ok := taxCfg[tenant]
	taxCfgMu.Unlock()
	if ok && time.Now().Before(e.until) {
		return e.cfg
	}
	var c TaxService
	_, _ = s.DB.GetDoc(context.Background(), tenant, "tax_service", "config", &c)
	taxCfgMu.Lock()
	taxCfg[tenant] = taxCfgEntry{c, time.Now().Add(30 * time.Second)}
	taxCfgMu.Unlock()
	return c
}

func (s *Server) taxSecret(tenant string) []byte {
	b, err := s.Keys.Blind(tenant, "tax-signing", "service")
	if err != nil {
		return nil
	}
	return []byte(hex.EncodeToString(b))
}

// ExternalTax answers the cart of a store (installed in the projector).
func (s *Server) ExternalTax(tenant, country, postal, currency string, exclusive bool, parts []logistics.TaxPart) (logistics.TaxResult, bool) {
	cfg := s.taxService(tenant)
	if !cfg.Active || cfg.Endpoint == "" {
		return logistics.TaxResult{}, false
	}
	byClass := map[string]int64{}
	var order []string
	for _, p := range parts {
		if _, ok := byClass[p.Class]; !ok {
			order = append(order, p.Class)
		}
		byClass[p.Class] += p.Gross
	}
	req := taxprovider.Request{Country: firstNonEmpty(country, "MX"), PostalCode: postal, Currency: currency, Inclusive: !exclusive}
	for _, c := range order {
		req.Parts = append(req.Parts, taxprovider.Part{Class: c, Amount: byClass[c]})
	}
	res, ok := taxCache.Get(context.Background(), tenant+"|"+cfg.Endpoint, taxprovider.Client{Endpoint: cfg.Endpoint, Secret: s.taxSecret(tenant), HTTP: taxHTTPClient}, req, 800*time.Millisecond)
	if !ok {
		return logistics.TaxResult{}, false
	}
	out := logistics.TaxResult{RegionName: firstNonEmpty(cfg.Name, "Servicio de impuestos"), Reason: "Calculado por " + firstNonEmpty(cfg.Name, "el servicio de impuestos"), Inclusive: !exclusive}
	for _, l := range res.Lines {
		amount := byClass[l.Class]
		line := logistics.TaxLine{Class: l.Class, Label: firstNonEmpty(l.Label, "Impuesto"), RateBps: l.RateBps, Tax: l.Tax}
		if exclusive {
			line.Net, line.Gross = amount, amount+l.Tax
		} else {
			if l.Tax > amount {
				return logistics.TaxResult{}, false // nonsense: more tax than the price
			}
			line.Gross, line.Net = amount, amount-l.Tax
		}
		out.Lines = append(out.Lines, line)
		out.Total += l.Tax
	}
	return out, true
}

func (s *Server) getTaxService(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var c TaxService
	_, _ = s.DB.GetDoc(r.Context(), pr.Tenant, "tax_service", "config", &c)
	out := map[string]any{"active": c.Active, "name": c.Name, "endpoint": c.Endpoint}
	if pr.Permissions.Allows("settings", "admin") {
		out["secret"] = string(s.taxSecret(pr.Tenant))
		out["contract"] = "POST {endpoint}/tax con JSON {country, postalCode, currency, pricesIncludeTax, parts:[{class, amount}]}; responde {lines:[{class, label, rateBps, tax}]}. Firma: X-Apexx-Signature = hex(HMAC-SHA256(secreto, timestamp + \".\" + cuerpo))."
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

func (s *Server) putTaxService(w http.ResponseWriter, r *http.Request) {
	var in TaxService
	if !decode(w, r, &in) {
		return
	}
	in.Name, in.Endpoint = strings.TrimSpace(in.Name), strings.TrimSpace(in.Endpoint)
	if u, err := url.Parse(in.Endpoint); in.Endpoint != "" && (err != nil || u.Scheme != "https" || u.Host == "" || len(in.Endpoint) > 300) {
		apierr.BadRequest(w, "Revisa los campos marcados", apierr.FieldError{Field: "endpoint", Code: "invalid", Message: "Escribe la dirección https del servicio de impuestos"})
		return
	}
	if in.Active && in.Endpoint == "" {
		apierr.BadRequest(w, "Revisa los campos marcados", apierr.FieldError{Field: "endpoint", Code: "required", Message: "Para activarlo escribe la dirección del servicio"})
		return
	}
	pr := principal(r)
	if err := s.DB.SetDoc(r.Context(), pr.Tenant, pr.Actor(), "tax_service", "config", "", in); err != nil {
		internalError(w, s.Log, err)
		return
	}
	taxCfgMu.Lock()
	delete(taxCfg, pr.Tenant) // this node sees it at once; others within 30 s
	taxCfgMu.Unlock()
	httpx.JSON(w, r, http.StatusOK, in)
}
