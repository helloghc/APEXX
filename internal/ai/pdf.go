package ai

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/ledongthuc/pdf"
)

// MaxPDFPages bounds the work of reading an uploaded PDF.
const MaxPDFPages = 60

// ErrPDF means the file is not a PDF with text we can read (a scanned
// image has no text: it would need OCR).
var ErrPDF = errors.New("ai: the PDF has no readable text")

// PDFLines extracts the text of a PDF line by line, top to bottom, page
// after page. A malformed file is an error, never a crash.
func PDFLines(data []byte) (lines []string, err error) {
	defer func() {
		if r := recover(); r != nil {
			lines, err = nil, fmt.Errorf("%w: %v", ErrPDF, r)
		}
	}()
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPDF, err)
	}
	n := r.NumPage()
	if n > MaxPDFPages {
		n = MaxPDFPages
	}
	for i := 1; i <= n; i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		// Characters come in drawing order with their position. Many PDFs
		// (and fonts without width tables) give no widths, so lines are
		// built by height: same Y is the same line, top to bottom; a new
		// text block on the same line (another X) starts a new word.
		type row struct {
			y float64
			b strings.Builder
			x float64
		}
		var rows []*row
		byY := map[int64]*row{}
		for _, t := range p.Content().Text {
			k := int64(math.Round(t.Y * 2)) // half-point tolerance
			rw := byY[k]
			if rw == nil {
				rw = &row{y: t.Y, x: t.X}
				byY[k] = rw
				rows = append(rows, rw)
			} else if t.X != rw.x {
				if rw.b.Len() > 0 && !strings.HasSuffix(rw.b.String(), " ") {
					rw.b.WriteByte(' ')
				}
				rw.x = t.X
			}
			rw.b.WriteString(t.S)
		}
		sort.SliceStable(rows, func(a, b int) bool { return rows[a].y > rows[b].y })
		for _, rw := range rows {
			if s := strings.Join(strings.Fields(rw.b.String()), " "); s != "" {
				lines = append(lines, s)
			}
		}
	}
	if len(lines) == 0 {
		return nil, ErrPDF
	}
	return lines, nil
}
