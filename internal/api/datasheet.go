package api

// Product spec sheets in PDF (plan IA-3/IA-4): the merchant uploads the
// manufacturer's PDF, Apexx reads its text and keeps the text only. The
// text shows on the product page under "Ficha técnica" and answers
// shoppers' questions with "Ficha técnica de <producto>" as the source.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/helloghc/apexx/internal/ai"
	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/httpx"
)

const datasheetKind = "datasheet"

// maxPDF is the largest PDF accepted (spec sheets, quote requests).
const maxPDF = 10 << 20

type datasheet struct {
	File  string    `json:"file"`
	Text  string    `json:"text"`
	Lines int       `json:"lines"`
	At    time.Time `json:"at"`
}

// readPDF reads an uploaded PDF from a multipart field "file" or a raw
// application/pdf body, and returns its lines.
func readPDF(w http.ResponseWriter, r *http.Request) (name string, lines []string, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPDF+1<<16)
	var data []byte
	var err error
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		f, h, ferr := r.FormFile("file")
		if ferr != nil {
			apierr.BadRequest(w, "", apierr.FieldError{Field: "file", Code: "required", Message: "Adjunta un archivo PDF"})
			return "", nil, false
		}
		defer f.Close()
		name = h.Filename
		data, err = io.ReadAll(io.LimitReader(f, maxPDF+1))
	} else {
		name = "documento.pdf"
		data, err = io.ReadAll(io.LimitReader(r.Body, maxPDF+1))
	}
	if err != nil || len(data) > maxPDF {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "file", Code: "too_large", Message: "El PDF puede pesar hasta 10 MB"})
		return "", nil, false
	}
	if !strings.HasPrefix(string(data[:min(len(data), 5)]), "%PDF-") {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "file", Code: "invalid", Message: "El archivo no es un PDF"})
		return "", nil, false
	}
	lines, err = ai.PDFLines(data)
	if errors.Is(err, ai.ErrPDF) || err != nil {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "file", Code: "no_text", Message: "No pudimos leer texto en el PDF. Si es una imagen escaneada, sube una versión con texto."})
		return "", nil, false
	}
	return name, lines, true
}

func (s *Server) putDatasheet(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	t, _ := s.Proj.Tenant(p.Tenant)
	id := r.PathValue("id")
	if _, err := t.Catalog.Get(id); err != nil {
		apierr.NotFound(w, "Producto no encontrado")
		return
	}
	name, lines, ok := readPDF(w, r)
	if !ok {
		return
	}
	text := strings.Join(lines, "\n")
	if r := []rune(text); len(r) > 60000 {
		text = string(r[:60000])
	}
	d := datasheet{File: name, Text: text, Lines: len(lines), At: time.Now().UTC()}
	if err := s.DB.SetDoc(r.Context(), p.Tenant, p.Actor(), datasheetKind, id, "", d); err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, d)
}

func (s *Server) getDatasheet(w http.ResponseWriter, r *http.Request) {
	var d datasheet
	found, err := s.DB.GetDoc(r.Context(), principal(r).Tenant, datasheetKind, r.PathValue("id"), &d)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if !found {
		httpx.JSON(w, r, http.StatusOK, map[string]any{"datasheet": nil}) // none yet: not an error
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"datasheet": d})
}

func (s *Server) deleteDatasheet(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := s.DB.RemoveDoc(r.Context(), p.Tenant, p.Actor(), datasheetKind, r.PathValue("id"), ""); err != nil {
		apierr.NotFound(w, "Este producto no tiene ficha técnica")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// productDatasheet returns the text of a product's spec sheet ("" if none).
func (s *Server) productDatasheet(r *http.Request, tenant, id string) string {
	var d datasheet
	if ok, err := s.DB.GetDoc(r.Context(), tenant, datasheetKind, id, &d); err != nil || !ok {
		return ""
	}
	return d.Text
}

func datasheetsOf(raw []byte) string {
	var d datasheet
	_ = json.Unmarshal(raw, &d)
	return d.Text
}
