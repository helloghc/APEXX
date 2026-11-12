// Package pdfdoc writes simple business documents (quotes, invoices) as PDF:
// text in the standard Helvetica fonts with Western European characters,
// horizontal rules and pages that break by themselves. It has no
// dependencies and builds the file directly from the PDF object model.
package pdfdoc

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Page size (US Letter) and margins, in points.
const (
	PageW  = 612.0
	PageH  = 792.0
	Margin = 48.0
)

// Doc is a document being written.
type Doc struct {
	pages []*bytes.Buffer
	cur   *bytes.Buffer
	y     float64 // next baseline from the top
	title string
}

// New starts a document.
func New(title string) *Doc {
	d := &Doc{title: title}
	d.newPage()
	return d
}

func (d *Doc) newPage() {
	d.cur = &bytes.Buffer{}
	d.pages = append(d.pages, d.cur)
	d.y = Margin
}

// ensure starts a new page when h points do not fit.
func (d *Doc) ensure(h float64) {
	if d.y+h > PageH-Margin {
		d.newPage()
	}
}

// Text writes one line at x (from the left margin) with the font size; bold
// uses Helvetica-Bold. Long text is cut to width (0 = to the right margin).
func (d *Doc) Text(x float64, s string, size float64, bold bool, width float64) {
	d.ensure(size * 1.4)
	if width == 0 {
		width = PageW - 2*Margin - x
	}
	s = fit(s, size, bold, width)
	d.textAt(Margin+x, d.y+size, s, size, bold)
}

// TextRight writes text ending at x (from the left margin) on the current line.
func (d *Doc) TextRight(x float64, s string, size float64, bold bool) {
	w := Width(s, size, bold)
	d.textAt(Margin+x-w, d.y+size, s, size, bold)
}

func (d *Doc) textAt(x, baselineFromTop float64, s string, size float64, bold bool) {
	font := "F1"
	if bold {
		font = "F2"
	}
	fmt.Fprintf(d.cur, "BT /%s %.1f Tf %.2f %.2f Td (%s) Tj ET\n", font, size, x, PageH-baselineFromTop, escape(s))
}

// Line moves down one line of the given size.
func (d *Doc) Line(size float64) { d.y += size * 1.45 }

// Space moves down by pt points.
func (d *Doc) Space(pt float64) { d.y += pt }

// Rule draws a horizontal line across the page.
func (d *Doc) Rule() {
	d.ensure(8)
	y := PageH - d.y - 4
	fmt.Fprintf(d.cur, "0.75 G 0.6 w %.2f %.2f m %.2f %.2f l S 0 G\n", Margin, y, PageW-Margin, y)
	d.y += 8
}

// Paragraph writes text wrapped to the width, moving down line by line.
func (d *Doc) Paragraph(x float64, s string, size float64, width float64) {
	if width == 0 {
		width = PageW - 2*Margin - x
	}
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			try := strings.TrimSpace(line + " " + word)
			if Width(try, size, false) > width && line != "" {
				d.Text(x, line, size, false, width)
				d.Line(size)
				line = word
				continue
			}
			line = try
		}
		d.Text(x, line, size, false, width)
		d.Line(size)
	}
}

// Bytes returns the finished PDF.
func (d *Doc) Bytes() []byte {
	var b bytes.Buffer
	var offsets []int
	obj := func(body string) {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}
	b.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	n := len(d.pages)
	// 1 catalog, 2 pages, 3 font, 4 bold font, 5 info, then a page and its content for each page.
	kids := make([]string, n)
	for i := range d.pages {
		kids[i] = fmt.Sprintf("%d 0 R", 6+2*i)
	}
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), n))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>")
	obj(fmt.Sprintf("<< /Title <%s> /Producer (Apexx) >>", utf16hex(d.title)))
	for i, p := range d.pages {
		content := p.String()
		if n > 1 {
			// Page number at the foot.
			label := fmt.Sprintf("Página %d de %d", i+1, n)
			content += fmt.Sprintf("BT /F1 8 Tf %.2f %.2f Td (%s) Tj ET\n", PageW-Margin-Width(label, 8, false), Margin/2, escape(toWin(label)))
		}
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.0f %.0f] /Resources << /Font << /F1 3 0 R /F2 4 0 R >> >> /Contents %d 0 R >>", PageW, PageH, 7+2*i))
		obj(fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content))
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R /Info 5 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return b.Bytes()
}

// utf16hex encodes text for the document information (UTF-16 with BOM).
func utf16hex(s string) string {
	var b strings.Builder
	b.WriteString("FEFF")
	for _, u := range utf16.Encode([]rune(s)) {
		fmt.Fprintf(&b, "%04X", u)
	}
	return b.String()
}

// escape converts to Windows-1252 and escapes the PDF string delimiters.
func escape(s string) string {
	s = toWin(s)
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '(', ')', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\n', '\r', '\t':
			b.WriteByte(' ')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// toWin maps UTF-8 text to Windows-1252 bytes (Latin-1 plus the usual
// typographic marks); anything else becomes "?".
func toWin(s string) string {
	if isASCII(s) {
		return s
	}
	extra := map[rune]byte{'€': 0x80, '‚': 0x82, '„': 0x84, '…': 0x85, '•': 0x95, '–': 0x96, '—': 0x97, '‘': 0x91, '’': 0x92, '“': 0x93, '”': 0x94, '™': 0x99, '·': 0xB7}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x80:
			b.WriteByte(byte(r))
		case r >= 0xA0 && r <= 0xFF:
			b.WriteByte(byte(r))
		default:
			if c, ok := extra[r]; ok {
				b.WriteByte(c)
			} else {
				b.WriteByte('?')
			}
		}
	}
	return b.String()
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// Width is the width of text in points (Helvetica metrics, approximated per
// character class so layout stays simple and deterministic).
func Width(s string, size float64, bold bool) float64 {
	w := 0.0
	for _, r := range s {
		w += charWidth(r, bold)
	}
	return w * size / 1000
}

func charWidth(r rune, bold bool) float64 {
	var w float64
	switch {
	case strings.ContainsRune("iIjl.,;:!|'", r):
		w = 260
	case strings.ContainsRune("ft()[]/-r ", r):
		w = 330
	case strings.ContainsRune("mwMW", r):
		w = 860
	case r >= 'A' && r <= 'Z', r >= 0xC0 && r <= 0xDE:
		w = 690
	case r >= '0' && r <= '9', r == '$':
		w = 556
	default:
		w = 540
	}
	if bold {
		w *= 1.07
	}
	return w
}

// fit cuts s with "…" so it fits the width.
func fit(s string, size float64, bold bool, width float64) string {
	if Width(s, size, bold) <= width {
		return s
	}
	for n := utf8.RuneCountInString(s); n > 0; n-- {
		t := string([]rune(s)[:n]) + "…"
		if Width(t, size, bold) <= width {
			return t
		}
	}
	return ""
}

// Money renders minor units as "$1,234.56 MXN".
func Money(v int64, cur string) string {
	neg := v < 0
	v = int64(math.Abs(float64(v)))
	whole := fmt.Sprintf("%d", v/100)
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	s := fmt.Sprintf("$%s.%02d %s", whole, v%100, cur)
	if neg {
		s = "-" + s
	}
	return s
}
