package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/csv"
	"encoding/hex"
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
)

// Product feeds for ad platforms. Each store has one secret key in the feed
// URL: the catalogue is public anyway, the key only keeps scrapers from
// pulling the whole thing in one request.

// FeedConfig is how a store publishes its catalogue to ad platforms.
type FeedConfig struct {
	Enabled        bool   `json:"enabled"`
	Key            string `json:"key"`
	GoogleCategory string `json:"googleCategory"` // default Google product category
	Condition      string `json:"condition"`      // new | refurbished | used
	OnlyInStock    bool   `json:"onlyInStock"`
}

func (s *Server) routesFeeds(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/feeds", s.authed("catalog", auth.Edit, s.getFeeds)) // the response carries the feed key
	handle("PUT /v1/feeds", s.authed("catalog", auth.Admin, s.putFeeds))
	handle("POST /v1/feeds/rotate", s.authed("catalog", auth.Admin, s.rotateFeedKey))
	handle("GET /s/{tenant}/{site}/feeds/{file}", s.shopFeed)
}

func newFeedKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) feedConfig(r *http.Request, tenant string) (FeedConfig, error) {
	var c FeedConfig
	_, err := s.DB.GetDoc(r.Context(), tenant, "feeds", "config", &c)
	if c.Condition == "" {
		c.Condition = "new"
	}
	return c, err
}

func (s *Server) feedURLs(r *http.Request, tenant string, c FeedConfig) map[string]string {
	if c.Key == "" {
		return map[string]string{}
	}
	origin := ""
	if t, ok := s.Proj.Tenant(tenant); ok {
		origin, _ = s.origin(r, t.Site())
	}
	base := origin + "/s/" + tenant + "/main/feeds/"
	return map[string]string{"google": base + "google.xml?key=" + c.Key, "meta": base + "meta.csv?key=" + c.Key}
}

func (s *Server) getFeeds(w http.ResponseWriter, r *http.Request) {
	tenant := principal(r).Tenant
	c, err := s.feedConfig(r, tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, r, http.StatusOK, map[string]any{"config": c, "urls": s.feedURLs(r, tenant, c)})
}

func (s *Server) saveFeeds(w http.ResponseWriter, r *http.Request, c FeedConfig) {
	p := principal(r)
	if err := s.DB.SetDoc(r.Context(), p.Tenant, p.Actor(), "feeds", "config", "", map[string]any{
		"enabled": c.Enabled, "key": c.Key, "googleCategory": c.GoogleCategory, "condition": c.Condition, "onlyInStock": c.OnlyInStock}); err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, r, http.StatusOK, map[string]any{"config": c, "urls": s.feedURLs(r, p.Tenant, c)})
}

func (s *Server) putFeeds(w http.ResponseWriter, r *http.Request) {
	var in FeedConfig
	if !decode(w, r, &in) {
		return
	}
	cur, err := s.feedConfig(r, principal(r).Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	in.GoogleCategory = strings.TrimSpace(in.GoogleCategory)
	switch {
	case in.Condition != "" && in.Condition != "new" && in.Condition != "refurbished" && in.Condition != "used":
		apierr.BadRequest(w, "Condición desconocida")
		return
	case len(in.GoogleCategory) > 200:
		apierr.BadRequest(w, "La categoría de Google admite hasta 200 caracteres")
		return
	}
	if in.Condition == "" {
		in.Condition = "new"
	}
	in.Key = cur.Key // the key only changes with "rotate"
	if in.Key == "" {
		in.Key = newFeedKey()
	}
	s.saveFeeds(w, r, in)
}

func (s *Server) rotateFeedKey(w http.ResponseWriter, r *http.Request) {
	cur, err := s.feedConfig(r, principal(r).Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	cur.Key = newFeedKey()
	s.saveFeeds(w, r, cur)
}

type feedItem struct {
	ID, Group, Title, Description, Link, Image, Availability, Price, SalePrice, Brand, Condition, Category string
}

var feedLimiter = &shopLimiter{n: 30}

// shopFeed serves google.xml (Google Merchant Center, RSS 2.0) or meta.csv
// (Meta catalogue). One row per variant, grouped by product.
func (s *Server) shopFeed(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")
	t, ok := s.Proj.Tenant(tenant)
	if !ok || !feedLimiter.allow(tenant+"|"+s.clientIP(r)) {
		http.NotFound(w, r)
		return
	}
	c, err := s.feedConfig(r, tenant)
	key := r.URL.Query().Get("key")
	if err != nil || !c.Enabled || c.Key == "" || subtle.ConstantTimeCompare([]byte(key), []byte(c.Key)) != 1 {
		http.NotFound(w, r) // same answer for "off" and "wrong key"
		return
	}
	file := r.PathValue("file")
	if file != "google.xml" && file != "meta.csv" {
		http.NotFound(w, r)
		return
	}
	origin, base := s.origin(r, t.Site())
	if base == "" && t.Site().Primary == "" {
		base = "/s/" + tenant + "/" + r.PathValue("site")
	}
	root := origin + base
	ch, _ := t.Channel("mx")
	cur := string(ch.Currency)
	if cur == "" {
		cur = "MXN"
	}
	amount := func(n int64) string {
		return strconv.FormatInt(n/100, 10) + "." + leftPad(strconv.FormatInt(n%100, 10)) + " " + cur
	}
	hidden := t.Search().HiddenSet()
	var items []feedItem
	for _, p := range t.Catalog.Products() {
		if p.Status != "active" || hidden[p.ID] {
			continue
		}
		img := ""
		if len(p.Media) > 0 {
			if m, ok := t.Taxo.Media(p.Media[0]); ok {
				if u := viewMedia(tenant, m).URL; u != "" {
					img = origin + u
				}
			}
		}
		for _, v := range p.Variants {
			q, ok := t.Pricing.Resolve("mx", v.SKU, 1)
			if !ok {
				continue
			}
			avail := t.Inventory.Available(v.SKU)
			if c.OnlyInStock && avail <= 0 {
				continue
			}
			it := feedItem{ID: v.SKU, Group: p.ID, Title: strings.TrimSpace(p.Title + " " + v.Title), Description: firstNonEmpty(p.Description, p.Title),
				Link: root + "/p/" + p.Handle, Image: img, Availability: "in_stock", Price: amount(q.Unit.Amount), Brand: p.Brand, Condition: c.Condition, Category: c.GoogleCategory}
			if avail <= 0 {
				it.Availability = "out_of_stock"
			}
			if q.CompareAt != nil && q.CompareAt.Amount > q.Unit.Amount {
				it.Price, it.SalePrice = amount(q.CompareAt.Amount), amount(q.Unit.Amount)
			}
			if vm, ok := t.Taxo.Media(v.MediaID); ok && v.MediaID != "" {
				if u := viewMedia(tenant, vm).URL; u != "" {
					it.Image = origin + u
				}
			}
			items = append(items, it)
		}
	}
	w.Header().Set("Cache-Control", "private, max-age=900")
	w.Header().Set("X-Robots-Tag", "noindex")
	if file == "meta.csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"id", "item_group_id", "title", "description", "availability", "condition", "price", "sale_price", "link", "image_link", "brand", "google_product_category"})
		for _, it := range items {
			_ = cw.Write([]string{it.ID, it.Group, csvSafeCell(it.Title), csvSafeCell(it.Description), strings.ReplaceAll(it.Availability, "_", " "), it.Condition, it.Price, it.SalePrice, it.Link, it.Image, csvSafeCell(it.Brand), it.Category})
		}
		cw.Flush()
		return
	}
	type gItem struct {
		ID           string `xml:"g:id"`
		Group        string `xml:"g:item_group_id"`
		Title        string `xml:"g:title"`
		Description  string `xml:"g:description"`
		Link         string `xml:"g:link"`
		Image        string `xml:"g:image_link,omitempty"`
		Availability string `xml:"g:availability"`
		Price        string `xml:"g:price"`
		SalePrice    string `xml:"g:sale_price,omitempty"`
		Brand        string `xml:"g:brand,omitempty"`
		Condition    string `xml:"g:condition"`
		Category     string `xml:"g:google_product_category,omitempty"`
	}
	type channel struct {
		Title string  `xml:"title"`
		Link  string  `xml:"link"`
		Desc  string  `xml:"description"`
		Items []gItem `xml:"item"`
	}
	doc := struct {
		XMLName xml.Name `xml:"rss"`
		Version string   `xml:"version,attr"`
		G       string   `xml:"xmlns:g,attr"`
		Channel channel  `xml:"channel"`
	}{Version: "2.0", G: "http://base.google.com/ns/1.0", Channel: channel{Title: firstNonEmpty(t.Site().Settings.Name, tenant), Link: root + "/index", Desc: "Catálogo de productos"}}
	for _, it := range items {
		doc.Channel.Items = append(doc.Channel.Items, gItem{it.ID, it.Group, it.Title, it.Description, it.Link, it.Image, it.Availability, it.Price, it.SalePrice, it.Brand, it.Condition, it.Category})
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	enc.Indent("", " ")
	_ = enc.Encode(doc)
}

// csvSafeCell keeps a spreadsheet from running a cell as a formula.
func csvSafeCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
