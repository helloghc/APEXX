// Package apierr writes every API error in a single format, RFC 9457
// "application/problem+json", with a stable machine-readable code
// (audit finding H4: VTEX uses five different error formats).
package apierr

import (
	"encoding/json"
	"net/http"
)

// Stable error codes. Clients switch on Code, never on human text.
const (
	CodeNotFound        = "not_found"
	CodeInvalidRequest  = "invalid_request"
	CodeValidation      = "validation_failed"
	CodeConflict        = "conflict"
	CodeInsufficientQty = "insufficient_stock"
	CodeInternal        = "internal"
)

// FieldError points at one invalid input field.
type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Problem is an RFC 9457 problem document.
type Problem struct {
	Type   string       `json:"type"`
	Title  string       `json:"title"`
	Status int          `json:"status"`
	Code   string       `json:"code"`
	Detail string       `json:"detail,omitempty"`
	Errors []FieldError `json:"errors,omitempty"`
}

// Write sends p as the response.
func Write(w http.ResponseWriter, p Problem) {
	if p.Type == "" {
		p.Type = "https://docs.apexx.dev/errors/" + p.Code
	}
	if p.Title == "" {
		p.Title = http.StatusText(p.Status)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// NotFound writes a 404 problem.
func NotFound(w http.ResponseWriter, detail string) {
	Write(w, Problem{Status: http.StatusNotFound, Code: CodeNotFound, Detail: detail})
}

// BadRequest writes a 400 problem.
func BadRequest(w http.ResponseWriter, detail string, errs ...FieldError) {
	code := CodeInvalidRequest
	if len(errs) > 0 {
		code = CodeValidation
	}
	Write(w, Problem{Status: http.StatusBadRequest, Code: code, Detail: detail, Errors: errs})
}
