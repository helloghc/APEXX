package api

import (
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/store"
)

// Company documents: PDFs, images and office files of a company, encrypted
// with the store's key before they are written. The store and the company's
// administrators upload, list, download and delete them.

const maxCompanyFile = 10 << 20

var companyFileTypes = map[string]string{
	"application/pdf": "pdf", "image/png": "png", "image/jpeg": "jpg",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": "docx",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":       "xlsx",
	"text/csv": "csv", "text/plain": "txt",
}

type companyFile struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	ContentType string    `json:"contentType"`
	Bytes       int64     `json:"bytes"`
	UploadedBy  string    `json:"uploadedBy"`
	CreatedAt   time.Time `json:"createdAt"`
}

func (s *Server) routesCompanyFiles(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/companies/{id}/files", s.authed("customers", auth.Read, noStorefront(s.listCompanyFiles)))
	handle("POST /v1/companies/{id}/files", s.authed("customers", auth.Edit, s.uploadCompanyFile))
	handle("GET /v1/companies/{id}/files/{file}", s.authed("customers", auth.Read, noStorefront(s.downloadCompanyFile)))
	handle("DELETE /v1/companies/{id}/files/{file}", s.authed("customers", auth.Edit, s.deleteCompanyFile))
	handle("GET /s/{tenant}/api/account/company/files", s.shopCompanyFiles)
	handle("POST /s/{tenant}/api/account/company/files", s.shopUploadCompanyFile)
	handle("GET /s/{tenant}/api/account/company/files/{file}", s.shopDownloadCompanyFile)
}

// sniffDoc detects the type from the content (never from the name).
func sniffDoc(b []byte, name string) (string, bool) {
	ct := http.DetectContentType(b)
	switch {
	case strings.HasPrefix(ct, "application/pdf"):
		return "application/pdf", true
	case ct == "image/png", ct == "image/jpeg":
		return ct, true
	case ct == "application/zip":
		// Office files are zip archives; the extension tells which one.
		switch strings.ToLower(filepath.Ext(name)) {
		case ".docx":
			return "application/vnd.openxmlformats-officedocument.wordprocessingml.document", true
		case ".xlsx":
			return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", true
		}
	case strings.HasPrefix(ct, "text/plain"):
		if strings.EqualFold(filepath.Ext(name), ".csv") {
			return "text/csv", true
		}
		return "text/plain", true
	}
	return "", false
}

func (s *Server) listFiles(r *http.Request, tenant, company string) ([]companyFile, error) {
	var out []companyFile
	err := s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT id, name, content_type, bytes, uploaded_by, created_at FROM company_files WHERE tenant_id=$1 AND company_id=$2 ORDER BY created_at DESC LIMIT 200`, tenant, company)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (companyFile, error) {
			var f companyFile
			return f, row.Scan(&f.ID, &f.Name, &f.ContentType, &f.Bytes, &f.UploadedBy, &f.CreatedAt)
		})
		return err
	})
	if out == nil {
		out = []companyFile{}
	}
	return out, err
}

func (s *Server) storeCompanyFile(w http.ResponseWriter, r *http.Request, tenant, company, by string) {
	if s.Media == nil {
		apierr.Write(w, apierr.Problem{Status: http.StatusServiceUnavailable, Code: "storage_unavailable", Detail: "El almacenamiento de archivos no está configurado"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxCompanyFile+1<<20)
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		apierr.Write(w, apierr.Problem{Status: http.StatusRequestEntityTooLarge, Code: "file_too_large", Detail: "El archivo pasa de 10 MB"})
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	f, hdr, err := r.FormFile("file")
	if err != nil {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "file", Code: "required", Message: "Falta el archivo"})
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxCompanyFile+1))
	if err != nil || len(data) == 0 || len(data) > maxCompanyFile {
		apierr.Write(w, apierr.Problem{Status: http.StatusRequestEntityTooLarge, Code: "file_too_large", Detail: "El archivo está vacío o pasa de 10 MB"})
		return
	}
	name := filepath.Base(strings.ReplaceAll(hdr.Filename, "\\", "/"))
	if len(name) > 160 {
		name = name[len(name)-160:]
	}
	ct, ok := sniffDoc(data, name)
	if !ok {
		apierr.Write(w, apierr.Problem{Status: http.StatusUnsupportedMediaType, Code: "unsupported_file", Detail: "Sube un PDF, imagen, Word, Excel o CSV"})
		return
	}
	id := store.NewID("cfl_")
	enc, err := s.Keys.Encrypt(tenant, data, "companyfile:"+id)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	var count int
	err = s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM company_files WHERE tenant_id=$1 AND company_id=$2`, tenant, company).Scan(&count); err != nil {
			return err
		}
		if count >= 100 {
			return errTooManyFiles
		}
		if err := s.Media.Put(r.Context(), tenant, id+"/data.bin", enc, "application/octet-stream"); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO company_files (tenant_id, id, company_id, name, content_type, bytes, uploaded_by) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			tenant, id, company, name, ct, len(data), by); err != nil {
			return err
		}
		return store.Audit(r.Context(), tx, tenant, by, "company.file_upload", company, map[string]any{"file": id, "name": name, "bytes": len(data)})
	})
	if errors.Is(err, errTooManyFiles) {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "limit", Detail: "Una empresa puede tener hasta 100 documentos"})
		return
	}
	if err != nil {
		_ = s.Media.DeletePrefix(r.Context(), tenant, id)
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, companyFile{ID: id, Name: name, ContentType: ct, Bytes: int64(len(data)), UploadedBy: by, CreatedAt: time.Now().UTC()})
}

var errTooManyFiles = errors.New("too many files")

func (s *Server) sendCompanyFile(w http.ResponseWriter, r *http.Request, tenant, company, id string) {
	var f companyFile
	err := s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT id, name, content_type, bytes FROM company_files WHERE tenant_id=$1 AND company_id=$2 AND id=$3`, tenant, company, id).Scan(&f.ID, &f.Name, &f.ContentType, &f.Bytes)
	})
	if err != nil || s.Media == nil {
		apierr.NotFound(w, "Documento no encontrado")
		return
	}
	obj, err := s.Media.Open(r.Context(), tenant, f.ID+"/data.bin")
	if err != nil {
		apierr.NotFound(w, "Documento no encontrado")
		return
	}
	enc, err := io.ReadAll(obj)
	_ = obj.Close()
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	plain, err := s.Keys.Decrypt(tenant, enc, "companyfile:"+f.ID)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.Header().Set("Content-Type", f.ContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+asciiName(f.Name)+`"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(plain)
}

func asciiName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 32 && r < 127 && r != '"' && r != '\\' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func (s *Server) listCompanyFiles(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !s.companyExists(w, r, p.Tenant, r.PathValue("id")) {
		return
	}
	list, err := s.listFiles(r, p.Tenant, r.PathValue("id"))
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) uploadCompanyFile(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !s.companyExists(w, r, p.Tenant, r.PathValue("id")) {
		return
	}
	by := p.Name
	if by == "" {
		by = p.Actor()
	}
	s.storeCompanyFile(w, r, p.Tenant, r.PathValue("id"), by)
}

func (s *Server) downloadCompanyFile(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	s.sendCompanyFile(w, r, p.Tenant, r.PathValue("id"), r.PathValue("file"))
}

func (s *Server) deleteCompanyFile(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	err := s.DB.Tx(r.Context(), p.Tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(r.Context(), `DELETE FROM company_files WHERE tenant_id=$1 AND company_id=$2 AND id=$3`, p.Tenant, r.PathValue("id"), r.PathValue("file"))
		if err == nil && ct.RowsAffected() == 0 {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}
		return store.Audit(r.Context(), tx, p.Tenant, p.Actor(), "company.file_delete", r.PathValue("id"), map[string]any{"file": r.PathValue("file")})
	})
	if errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "Documento no encontrado")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	_ = s.Media.DeletePrefix(r.Context(), p.Tenant, r.PathValue("file"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) shopCompanyFiles(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.roleAdmin(w, r, false)
	if !ok {
		return
	}
	list, err := s.listFiles(r, r.PathValue("tenant"), c.ID)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) shopUploadCompanyFile(w http.ResponseWriter, r *http.Request) {
	c, email, ok := s.roleAdmin(w, r, true)
	if !ok {
		return
	}
	s.storeCompanyFile(w, r, r.PathValue("tenant"), c.ID, email)
}

func (s *Server) shopDownloadCompanyFile(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.roleAdmin(w, r, false)
	if !ok {
		return
	}
	s.sendCompanyFile(w, r, r.PathValue("tenant"), c.ID, r.PathValue("file"))
}
