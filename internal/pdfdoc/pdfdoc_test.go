package pdfdoc

import (
	"bytes"
	"strings"
	"testing"
)

func TestDocument(t *testing.T) {
	d := New("Cotización #12")
	d.Text(0, "Cotización #12 — Ñandú (año)", 18, true, 0)
	d.Line(18)
	d.Rule()
	for i := 0; i < 80; i++ { // enough lines for a second page
		d.Text(0, "Línea de producto con acentos: áéíóú", 10, false, 0)
		d.TextRight(500, Money(123456, "MXN"), 10, false)
		d.Line(10)
	}
	d.Paragraph(0, strings.Repeat("Condiciones de pago y entrega. ", 40), 9, 0)
	b := d.Bytes()
	if !bytes.HasPrefix(b, []byte("%PDF-1.4")) || !bytes.HasSuffix(b, []byte("%%EOF\n")) {
		t.Fatal("not a PDF")
	}
	if !bytes.Contains(b, []byte("/Count 2")) {
		t.Fatalf("expected two pages")
	}
	// Accents travel as Windows-1252 bytes; parentheses are escaped.
	if !bytes.Contains(b, []byte("Cotizaci\xf3n")) || !bytes.Contains(b, []byte("\\(a\xf1o\\)")) {
		t.Fatal("text not encoded")
	}
	if !bytes.Contains(b, []byte("$1,234.56 MXN")) {
		t.Fatal("money not rendered")
	}
	if !bytes.Contains(b, []byte("xref")) || !bytes.Contains(b, []byte("startxref")) {
		t.Fatal("no cross-reference table")
	}
}
