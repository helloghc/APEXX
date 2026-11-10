// Package httpx holds shared HTTP helpers: JSON responses with ETag,
// compression, timing headers, field projection and opaque cursors.
package httpx

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// JSON writes v with a strong ETag. A matching If-None-Match returns 304
// (audit finding H9: VTEX pricing returns no ETag or cache headers).
func JSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "encode error", http.StatusInternalServerError)
		return
	}
	sum := sha256.Sum256(b)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	h := w.Header()
	h.Set("ETag", etag)
	if r.Method == http.MethodGet && r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if h.Get("Content-Type") == "" {
		h.Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// Decode reads a JSON body, rejecting unknown fields and bodies over 1 MiB.
func Decode(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

// Project keeps only the requested top-level fields of v ("fields=id,title").
// Listing responses stay small (audit finding H2: 57 KB per product in VTEX).
func Project(v any, fields []string) any {
	if len(fields) == 0 {
		return v
	}
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return v
	}
	out := make(map[string]json.RawMessage, len(fields))
	for _, f := range fields {
		if raw, ok := m[f]; ok {
			out[f] = raw
		}
	}
	return out
}

// Fields parses the "fields" query parameter.
func Fields(r *http.Request) []string {
	s := strings.TrimSpace(r.URL.Query().Get("fields"))
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// EncodeCursor makes an opaque cursor from the last key returned.
func EncodeCursor(key string) string {
	if key == "" {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}

// DecodeCursor reverses EncodeCursor.
func DecodeCursor(c string) (string, error) {
	if c == "" {
		return "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", fmt.Errorf("invalid cursor")
	}
	return string(b), nil
}

var gzPool = sync.Pool{New: func() any { w, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed); return w }}

type gzWriter struct {
	http.ResponseWriter
	gz *gzip.Writer
}

func (g *gzWriter) Write(b []byte) (int, error) { return g.gz.Write(b) }

func (g *gzWriter) WriteHeader(code int) {
	g.Header().Del("Content-Length")
	g.ResponseWriter.WriteHeader(code)
}

// Middleware adds compression, request timing and a request id.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		w.Header().Set("Vary", "Accept-Encoding")
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			gz := gzPool.Get().(*gzip.Writer)
			gz.Reset(w)
			w.Header().Set("Content-Encoding", "gzip")
			gw := &gzWriter{ResponseWriter: w, gz: gz}
			defer func() { _ = gz.Close(); gzPool.Put(gz) }()
			w = gw
		}
		w.Header().Set("Server-Timing", "app;dur=0")
		tw := &timingWriter{ResponseWriter: w, start: start}
		next.ServeHTTP(tw, r)
	})
}

type timingWriter struct {
	http.ResponseWriter
	start       time.Time
	wroteHeader bool
}

func (t *timingWriter) WriteHeader(code int) {
	if !t.wroteHeader {
		t.wroteHeader = true
		d := float64(time.Since(t.start).Microseconds()) / 1000
		t.Header().Set("Server-Timing", fmt.Sprintf("app;dur=%.3f", d))
	}
	t.ResponseWriter.WriteHeader(code)
}

func (t *timingWriter) Write(b []byte) (int, error) {
	if !t.wroteHeader {
		t.WriteHeader(http.StatusOK)
	}
	return t.ResponseWriter.Write(b)
}
