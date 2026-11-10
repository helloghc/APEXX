package api

import (
	"io"
	"log/slog"
	"testing"
)

// TestRoutesRegister catches conflicting route patterns (net/http panics on them).
func TestRoutesRegister(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("routes conflict: %v", r)
		}
	}()
	s := &Server{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	_ = s.Routes()
}
