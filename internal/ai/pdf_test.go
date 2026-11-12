package ai

import (
	"strings"
	"testing"

	"github.com/helloghc/apexx/internal/pdfdoc"
)

func TestPDFLines(t *testing.T) {
	d := pdfdoc.New("Ficha técnica")
	d.Text(40, "Taladro percutor 650 W", 14, true, 500)
	d.Line(14)
	d.Text(40, "Potencia: 650 W", 10, false, 500)
	d.Line(10)
	d.Text(40, "Velocidad: 0 a 3000 rpm", 10, false, 500)
	d.Line(10)
	lines, err := PDFLines(d.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	// One line per printed line, in order: not just the text somewhere.
	want := []string{"Taladro percutor 650 W", "Potencia: 650 W", "Velocidad: 0 a 3000 rpm"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("lines = %q, want %q", lines, want)
	}
	if _, err := PDFLines([]byte("%PDF-1.4 esto no es un pdf")); err == nil {
		t.Fatal("garbage accepted")
	}
}
