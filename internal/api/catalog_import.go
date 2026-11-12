package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/store"
)

const importBatch = 25

// importSummary is what the preview shows before anything is written.
type importSummary struct {
	Rows     int            `json:"rows"`
	Products int            `json:"products"`
	Variants int            `json:"variants"`
	Creates  int            `json:"creates"`
	Updates  int            `json:"updates"`
	Invalid  int            `json:"invalid"` // products with errors; they are skipped
	Columns  []string       `json:"columns"`
	Warnings []string       `json:"warnings"`
	Sample   []importSample `json:"sample"`
	Location string         `json:"location"` // where stock will be written
}

type importSample struct {
	Row      int    `json:"row"`
	Handle   string `json:"handle"`
	Title    string `json:"title"`
	Variants int    `json:"variants"`
	Exists   bool   `json:"exists"`
	Invalid  bool   `json:"invalid"`
}

// importError is a row of the error report (validation or apply).
type importError struct {
	Row     int    `json:"row"`
	Column  string `json:"column"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Handle  string `json:"handle,omitempty"`
}

func importContext(t *projection.Tenant) catalog.ImportContext {
	return catalog.ImportContext{
		SKUOwner: func(sku string) (string, bool) {
			id, ok := t.Catalog.ProductOfSKU(sku)
			if !ok {
				return "", false
			}
			p, err := t.Catalog.Get(id)
			if err != nil {
				return "", false
			}
			return p.Handle, true
		},
		HandleExists: func(h string) bool { _, ok := t.Catalog.IDByHandle(h); return ok },
		Attribute:    t.Taxo.Attribute,
	}
}

func (s *Server) importLocation(ctx context.Context, tenant string) string {
	locs, err := s.DB.Locations(ctx, tenant)
	if err != nil || len(locs) == 0 {
		return "principal"
	}
	return locs[0]
}

func summarize(plan *catalog.ImportPlan, location string) (importSummary, []importError) {
	sum := importSummary{Rows: plan.Rows, Products: len(plan.Products), Columns: plan.Columns, Warnings: plan.Warnings, Location: location}
	if sum.Warnings == nil {
		sum.Warnings = []string{}
	}
	for _, p := range plan.Products {
		sum.Variants += len(p.Variants)
		switch {
		case p.Invalid:
			sum.Invalid++
		case p.Exists:
			sum.Updates++
		default:
			sum.Creates++
		}
		if len(sum.Sample) < 50 {
			sum.Sample = append(sum.Sample, importSample{Row: p.Row, Handle: p.Handle, Title: p.Title, Variants: len(p.Variants), Exists: p.Exists, Invalid: p.Invalid})
		}
	}
	errs := make([]importError, len(plan.Errors))
	for i, e := range plan.Errors {
		errs[i] = importError{Row: e.Row, Column: e.Column, Code: e.Code, Message: e.Message}
	}
	return sum, errs
}

// createImport validates a CSV and stores it as a preview. Nothing in the
// catalog changes until the preview is applied.
func (s *Server) createImport(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	tooLarge := func() {
		apierr.Write(w, apierr.Problem{Status: http.StatusRequestEntityTooLarge, Code: "file_too_large", Detail: "El CSV pasa de 5 MB. Divídelo en varios archivos."})
	}
	r.Body = http.MaxBytesReader(w, r.Body, catalog.MaxCSVBytes+1<<20)
	var data []byte
	name := "productos.csv"
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(catalog.MaxCSVBytes); err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				tooLarge()
				return
			}
			apierr.BadRequest(w, "Envía el archivo en el campo file")
			return
		}
		defer func() { _ = r.MultipartForm.RemoveAll() }()
		f, hdr, err := r.FormFile("file")
		if err != nil {
			apierr.BadRequest(w, "", fe("file", "required", "Falta el archivo CSV"))
			return
		}
		defer f.Close()
		name = filepath.Base(strings.ReplaceAll(hdr.Filename, "\\", "/"))
		data, _ = io.ReadAll(io.LimitReader(f, catalog.MaxCSVBytes+1))
	} else {
		var err error
		if data, err = io.ReadAll(r.Body); err != nil {
			tooLarge()
			return
		}
	}
	if len(data) > catalog.MaxCSVBytes {
		tooLarge()
		return
	}
	if len(name) > 200 {
		name = name[:200]
	}
	plan, err := catalog.ParseCSV(data, importContext(t))
	if err != nil {
		msg := strings.TrimPrefix(err.Error(), catalog.ErrCSVFormat.Error()+": ")
		apierr.Write(w, apierr.Problem{Status: http.StatusUnprocessableEntity, Code: "invalid_csv", Detail: "No se pudo leer el CSV: " + msg})
		return
	}
	pr := principal(r)
	sum, errs := summarize(plan, s.importLocation(r.Context(), pr.Tenant))
	sb, _ := json.Marshal(sum)
	eb, _ := json.Marshal(errs)
	job := store.ImportJob{ID: store.NewID("imp_"), Filename: name, Source: data, Summary: sb, Errors: eb, Total: sum.Creates + sum.Updates}
	if err := s.DB.CreateImport(r.Context(), pr.Tenant, pr.Actor(), job); err != nil {
		internalError(w, s.Log, err)
		return
	}
	saved, err := s.DB.GetImport(r.Context(), pr.Tenant, job.ID, false)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, saved)
}

func (s *Server) getImport(w http.ResponseWriter, r *http.Request) {
	j, err := s.DB.GetImport(r.Context(), principal(r).Tenant, r.PathValue("id"), false)
	if errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "La importación no existe")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) listImports(w http.ResponseWriter, r *http.Request) {
	js, err := s.DB.ListImports(r.Context(), principal(r).Tenant, 20)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if js == nil {
		js = []store.ImportJob{}
	}
	for i := range js {
		js[i].Errors = json.RawMessage("[]") // the list stays small; details come from GET /{id}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": js})
}

// applyImport starts applying a preview in the background, in batches, and
// returns at once; progress is read with GET /v1/catalog/imports/{id}.
func (s *Server) applyImport(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	pr := principal(r)
	id := r.PathValue("id")
	job, err := s.DB.GetImport(r.Context(), pr.Tenant, id, true)
	if errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "La importación no existe")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	// Validate again: the catalog may have changed since the preview.
	plan, err := catalog.ParseCSV(job.Source, importContext(t))
	if err != nil {
		apierr.Write(w, apierr.Problem{Status: http.StatusUnprocessableEntity, Code: "invalid_csv", Detail: "El CSV ya no se puede leer"})
		return
	}
	var todo []catalog.ImportProduct
	for _, p := range plan.Products {
		if !p.Invalid {
			todo = append(todo, p)
		}
	}
	if len(todo) == 0 {
		apierr.Write(w, apierr.Problem{Status: http.StatusUnprocessableEntity, Code: "nothing_to_import", Detail: "Ningún producto del archivo es válido. Corrige los errores y súbelo de nuevo."})
		return
	}
	if err := s.DB.ClaimImport(r.Context(), pr.Tenant, pr.Actor(), id, len(todo)); errors.Is(err, store.ErrConflict) {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "import_not_pending", Detail: "Esta importación ya se aplicó o se está aplicando"})
		return
	} else if err != nil {
		internalError(w, s.Log, err)
		return
	}
	_, verrs := summarize(plan, "")
	opt := store.ImportOptions{Channel: defaultChannel, Currency: "MXN", Location: s.importLocation(r.Context(), pr.Tenant)}
	if ch, found := t.Channel(defaultChannel); found && ch.Currency != "" {
		opt.Currency = string(ch.Currency)
	}
	go s.runImport(pr.Tenant, pr.Actor(), id, todo, verrs, opt)
	job, _ = s.DB.GetImport(r.Context(), pr.Tenant, id, false)
	httpx.JSON(w, r, http.StatusAccepted, job)
}

// runImport applies products one transaction each, reporting progress after
// every batch. A product that fails does not stop the rest.
func (s *Server) runImport(tenant, actor, id string, todo []catalog.ImportProduct, errs []importError, opt store.ImportOptions) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	defer func() {
		if rec := recover(); rec != nil {
			s.Log.Error("import panicked", "import", id, "panic", rec)
			_ = s.DB.ImportProgress(context.Background(), tenant, id, 0, 0, 0, len(todo), errs, "failed")
		}
	}()
	created, updated, failed := 0, 0, 0
	// Batches of importBatch products, one transaction each, every product
	// in its own savepoint: a bad product fails alone, as before, with one
	// commit per batch instead of one per product.
	for lo := 0; lo < len(todo); lo += importBatch {
		hi := min(lo+importBatch, len(todo))
		outs := s.DB.ApplyImportBatch(ctx, tenant, actor, todo[lo:hi], opt)
		for k, o := range outs {
			p := todo[lo+k]
			var ae *store.ApplyError
			switch {
			case o.Err == nil && o.Created:
				created++
			case o.Err == nil:
				updated++
			default:
				failed++
				msg := "No se pudo guardar el producto; intenta de nuevo"
				if errors.As(o.Err, &ae) {
					msg = ae.Message
				} else {
					s.Log.Error("import product", "import", id, "row", p.Row, "err", o.Err)
				}
				if len(errs) < 1000 {
					errs = append(errs, importError{Row: p.Row, Column: "", Code: "apply_failed", Message: msg, Handle: p.Handle})
				}
			}
		}
		status := ""
		if hi == len(todo) {
			status = "done"
			if created+updated == 0 {
				status = "failed"
			}
		}
		if err := s.DB.ImportProgress(ctx, tenant, id, hi, created, updated, failed, errs, status); err != nil {
			s.Log.Error("import progress", "import", id, "err", err)
		}
		s.settle(ctx)
	}
}

func (s *Server) importTemplate(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	attrs := t.Taxo.Attributes()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="plantilla-productos.csv"`)
	_, _ = w.Write([]byte("\xef\xbb\xbf"))
	cw := csv.NewWriter(w)
	_ = cw.Write(catalog.ExportHeader(attrs))
	pad := func(row []string) []string {
		for len(row) < len(catalog.CSVColumns)+len(attrs) {
			row = append(row, "")
		}
		return row
	}
	_ = cw.Write(pad([]string{"playera-basica", "Playera básica", "Algodón peinado, corte regular.", "draft", "Mi marca", "Ropa > Playeras", "Playera", "verano, básicos",
		"", "", "Color", "Negro", "Talla", "M", "", "", "PLAYERA-NEGRO-M", "7501234567890", "180", "299.00", "349.00", "25"}))
	_ = cw.Write(pad([]string{"playera-basica", "", "", "", "", "", "", "", "", "", "Color", "Negro", "Talla", "G", "", "", "PLAYERA-NEGRO-G", "", "190", "299.00", "349.00", "12"}))
	cw.Flush()
}

// exportCSV streams the filtered catalog in the same format the import reads.
func (s *Server) exportCSV(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	f := filterFromQuery(r)
	if errs := f.validate(); len(errs) > 0 {
		apierr.BadRequest(w, "Filtros inválidos", errs...)
		return
	}
	ps := f.apply(t)
	if ids := strings.TrimSpace(r.URL.Query().Get("ids")); ids != "" {
		want := map[string]bool{}
		for _, id := range strings.Split(ids, ",") {
			want[id] = true
		}
		kept := ps[:0]
		for _, p := range ps {
			if want[p.ID] {
				kept = append(kept, p)
			}
		}
		ps = kept
	}
	attrs := t.Taxo.Attributes()
	location := s.importLocation(r.Context(), t.ID)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="productos-`+time.Now().Format("2006-01-02")+`.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("\xef\xbb\xbf")) // Excel reads UTF-8 only with a BOM
	cw := csv.NewWriter(w)
	_ = cw.Write(catalog.ExportHeader(attrs))
	for _, p := range ps {
		info := catalog.ExportInfo{Brand: p.Brand, Price: map[string]int64{}, CompareAt: map[string]int64{}, Stock: map[string]int64{}}
		if b, found := t.Taxo.Brand(p.BrandID); found {
			info.Brand = b.Name
		}
		for _, raw := range p.Categories {
			id, found := t.Taxo.ResolveCategory(raw)
			if !found {
				info.Categories = append(info.Categories, raw)
				continue
			}
			var names []string
			for _, c := range t.Taxo.Path(id) {
				names = append(names, c.Name)
			}
			info.Categories = append(info.Categories, strings.Join(names, " > "))
		}
		for _, v := range p.Variants {
			for _, tier := range t.Pricing.Tiers(defaultChannel, v.SKU) {
				if tier.MinQty == 1 {
					info.Price[v.SKU] = tier.Unit.Amount
					if tier.CompareAt != nil {
						info.CompareAt[v.SKU] = tier.CompareAt.Amount
					}
				}
			}
			// Stock of the location the import writes to, so a round trip changes nothing.
			if snap, found := t.Inventory.Get(v.SKU); found {
				for _, l := range snap.Levels {
					if l.LocationID == location {
						info.Stock[v.SKU] = l.OnHand
					}
				}
			}
		}
		_ = cw.WriteAll(catalog.ExportRows(p, info, attrs))
	}
	cw.Flush()
}
