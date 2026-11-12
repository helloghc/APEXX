package ai

import (
	"html"
	"regexp"
	"strings"
)

// Piece is one chunk of a document, with the context that situates it
// (contextual chunking): "Política de devoluciones › Plazos". The context is
// embedded together with the text, so a chunk that says "tienes 30 días"
// is found by "¿cuánto tiempo tengo para devolver?" even though the chunk
// never says "devolver".
type Piece struct {
	Context string
	Text    string
}

var (
	reHeading = regexp.MustCompile(`(?is)<h[1-4][^>]*>(.*?)</h[1-4]>`)
	reBlock   = regexp.MustCompile(`(?i)</?(p|div|li|ul|ol|br|tr|table|section|article|blockquote)[^>]*>`)
	reTag     = regexp.MustCompile(`<[^>]+>`)
	reSpace   = regexp.MustCompile(`[ \t\r\f\v]+`)
	reSent    = regexp.MustCompile(`([.!?])\s+`)
)

// ChunkHTML splits a document into pieces of about size characters, never
// across a heading, each with title › heading as context. Paragraphs stay
// whole when they fit; long ones are cut at sentence ends.
func ChunkHTML(title, body string, size int) []Piece {
	if size <= 0 {
		size = 700
	}
	// Mark headings so they survive tag stripping.
	body = reHeading.ReplaceAllStringFunc(body, func(m string) string {
		inner := reHeading.FindStringSubmatch(m)[1]
		return "\n\x01" + strings.TrimSpace(reTag.ReplaceAllString(inner, "")) + "\n"
	})
	body = reBlock.ReplaceAllString(body, "\n")
	body = html.UnescapeString(reTag.ReplaceAllString(body, ""))
	var out []Piece
	heading := ""
	var buf []string
	n := 0
	ctx := func() string {
		if heading == "" {
			return title
		}
		return title + " › " + heading
	}
	flush := func() {
		if len(buf) > 0 {
			out = append(out, Piece{Context: ctx(), Text: strings.Join(buf, "\n")})
		}
		buf, n = nil, 0
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(reSpace.ReplaceAllString(line, " "))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "\x01") {
			flush()
			heading = strings.TrimSpace(line[1:])
			continue
		}
		for _, part := range splitLong(line, size) {
			if n > 0 && n+len(part) > size {
				flush()
			}
			buf = append(buf, part)
			n += len(part) + 1
		}
	}
	flush()
	return out
}

// splitLong cuts a paragraph longer than size at sentence ends.
func splitLong(p string, size int) []string {
	if len(p) <= size {
		return []string{p}
	}
	var out []string
	var cur strings.Builder
	for _, s := range Sentences(p) {
		if cur.Len() > 0 && cur.Len()+len(s)+1 > size {
			out = append(out, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte(' ')
		}
		cur.WriteString(s)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// Sentences splits text into sentences (keeping their final mark).
func Sentences(t string) []string {
	t = strings.TrimSpace(t)
	if t == "" {
		return nil
	}
	marked := reSent.ReplaceAllString(t, "$1\x02")
	var out []string
	for _, s := range strings.Split(marked, "\x02") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	// "¿Cuánto tarda?" keeps its opening mark with the sentence it opens.
	for i := 0; i+1 < len(out); i++ {
		if out[i] == "¿" || out[i] == "¡" {
			out[i+1] = out[i] + out[i+1]
			out = append(out[:i], out[i+1:]...)
		}
	}
	return out
}
