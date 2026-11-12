// Package i18n shows the storefront in more than one language.
//
// The store is written in Spanish. Instead of marking every phrase by hand,
// the package walks the markup (a template source, a rendered page or the
// storefront script), finds the pieces of human text and looks each one up in
// a dictionary. A phrase with no translation stays in Spanish, and a test
// lists the phrases that still lack one, so nothing is forgotten silently.
package i18n

import (
	"html"
	"regexp"
	"strings"
	"unicode"
)

// Default is the language the store is written in.
const Default = "es"

// Lookup returns the translation of a Spanish phrase, if there is one.
type Lookup func(phrase string) (string, bool)

// Options adjust how a page is rewritten.
type Options struct {
	// Href rewrites the target of each link (to keep the language in the URL).
	Href func(string) string
	// FormParam, when set, is added as a hidden "hl" field to forms sent by GET.
	FormParam string
}

var (
	spaces  = regexp.MustCompile(`\s+`)
	numbers = regexp.MustCompile(`[0-9][0-9.,]*[0-9]|[0-9]`)
	attrs   = regexp.MustCompile(`\b(placeholder|aria-label|title|alt)="([^"]*)"`)
	hrefs   = regexp.MustCompile(`\bhref="([^"]*)"`)
)

// phrase translates one run of text, keeping the spaces around it.
func phrase(s string, look Lookup, escape func(string) string) string {
	core := strings.TrimSpace(s)
	if core == "" || !hasLetter(core) {
		return s
	}
	lead := s[:strings.Index(s, core)]
	trail := s[len(lead)+len(core):]
	key := spaces.ReplaceAllString(core, " ")
	if strings.Contains(key, "&") {
		key = html.UnescapeString(key)
	}
	if out, ok := look(key); ok {
		return lead + escape(out) + trail
	}
	// "3 disponibles" is looked up as "# disponibles" and the 3 put back.
	if nums := numbers.FindAllString(key, -1); len(nums) > 0 {
		if out, ok := look(numbers.ReplaceAllString(key, "#")); ok {
			for _, n := range nums {
				out = strings.Replace(out, "#", n, 1)
			}
			return lead + escape(out) + trail
		}
	}
	return s
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// text translates a string that may hold template actions ({{...}}).
func text(s string, look Lookup, collect *[]string) string {
	var b strings.Builder
	for s != "" {
		i := strings.Index(s, "{{")
		if i < 0 {
			b.WriteString(one(s, look, collect))
			break
		}
		b.WriteString(one(s[:i], look, collect))
		j := strings.Index(s[i:], "}}")
		if j < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+j+2])
		s = s[i+j+2:]
	}
	return b.String()
}

func one(s string, look Lookup, collect *[]string) string {
	if collect != nil {
		if core := strings.TrimSpace(s); core != "" && hasLetter(core) {
			*collect = append(*collect, spaces.ReplaceAllString(html.UnescapeString(core), " "))
		}
		return s
	}
	return phrase(s, look, html.EscapeString)
}

// Markup translates the text and the human-readable attributes of an HTML
// page or of a Go template source. Scripts, styles and template actions are
// left exactly as they are.
func Markup(src string, look Lookup, opt Options) string {
	return markup(src, look, opt, nil)
}

// MarkupPhrases lists the pieces of text Markup would look up.
func MarkupPhrases(src string) []string {
	var out []string
	markup(src, nil, Options{}, &out)
	return out
}

func markup(src string, look Lookup, opt Options, collect *[]string) string {
	var b strings.Builder
	b.Grow(len(src) + len(src)/8)
	for i := 0; i < len(src); {
		if strings.HasPrefix(src[i:], "{{") {
			j := strings.Index(src[i:], "}}")
			if j < 0 {
				b.WriteString(src[i:])
				break
			}
			b.WriteString(src[i : i+j+2])
			i += j + 2
			continue
		}
		if src[i] == '<' && i+1 < len(src) && (isAlpha(src[i+1]) || src[i+1] == '/' || src[i+1] == '!') {
			end := tagEnd(src, i)
			tag := src[i:end]
			name := tagName(tag)
			if name == "script" || name == "style" {
				if c := strings.Index(strings.ToLower(src[end:]), "</"+name); c >= 0 {
					end += c
				}
				b.WriteString(src[i:end])
				i = end
				continue
			}
			b.WriteString(rewriteTag(tag, name, look, opt, collect))
			i = end
			continue
		}
		j := i + 1
		for j < len(src) && !(src[j] == '<' && j+1 < len(src) && (isAlpha(src[j+1]) || src[j+1] == '/' || src[j+1] == '!')) && !strings.HasPrefix(src[j:], "{{") {
			j++
		}
		b.WriteString(one(src[i:j], look, collect))
		i = j
	}
	return b.String()
}

func isAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// tagEnd returns the index just past the tag that starts at i.
func tagEnd(src string, i int) int {
	quote := byte(0)
	for j := i + 1; j < len(src); j++ {
		c := src[j]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '{' && strings.HasPrefix(src[j:], "{{"):
			if k := strings.Index(src[j:], "}}"); k >= 0 {
				j += k + 1
			}
		case c == '>':
			return j + 1
		}
	}
	return len(src)
}

func tagName(tag string) string {
	j := 1
	for j < len(tag) && (isAlpha(tag[j]) || (tag[j] >= '0' && tag[j] <= '9')) {
		j++
	}
	return strings.ToLower(tag[1:j])
}

func rewriteTag(tag, name string, look Lookup, opt Options, collect *[]string) string {
	if strings.Contains(tag, `="`) {
		tag = attrs.ReplaceAllStringFunc(tag, func(m string) string {
			eq := strings.Index(m, `="`)
			return m[:eq+2] + text(m[eq+2:len(m)-1], look, collect) + `"`
		})
		if opt.Href != nil && (name == "a" || name == "link") {
			tag = hrefs.ReplaceAllStringFunc(tag, func(m string) string {
				return `href="` + html.EscapeString(opt.Href(html.UnescapeString(m[6:len(m)-1]))) + `"`
			})
		}
	}
	if name == "form" && opt.FormParam != "" && !strings.Contains(strings.ToLower(tag), `method="post"`) && strings.Contains(tag, "action=") {
		tag += `<input type="hidden" name="hl" value="` + html.EscapeString(opt.FormParam) + `">`
	}
	return tag
}

// scriptBreaks are the characters that end a piece of text inside the script:
// the quotes of a string and the brackets of the markup it builds.
const scriptBreaks = "'\"`<>{}$\n"

// Script translates the phrases inside the storefront script. A phrase is
// replaced only when the whole piece between quotes or brackets is in the
// dictionary, so code is never touched.
func Script(src string, look Lookup) string {
	return script(src, look, nil)
}

// ScriptPhrases lists the pieces of text Script would look up.
func ScriptPhrases(src string) []string {
	var out []string
	script(src, nil, &out)
	return out
}

func script(src string, look Lookup, collect *[]string) string {
	var b strings.Builder
	b.Grow(len(src) + len(src)/8)
	for src != "" {
		i := strings.IndexAny(src, scriptBreaks)
		piece := src
		if i >= 0 {
			piece = src[:i]
		}
		if collect != nil {
			if core := strings.TrimSpace(piece); core != "" && hasLetter(core) {
				*collect = append(*collect, spaces.ReplaceAllString(core, " "))
			}
		} else {
			piece = phrase(piece, look, escapeScript)
		}
		b.WriteString(piece)
		if i < 0 {
			break
		}
		b.WriteByte(src[i])
		src = src[i+1:]
	}
	return b.String()
}

var scriptEscaper = strings.NewReplacer(`\`, `\\`, `'`, `\'`, `"`, `\"`, "`", "\\`", "<", `\x3c`, ">", `\x3e`, "$", `\$`, "{", `\{`, "}", `\}`, "\n", " ")

func escapeScript(s string) string { return scriptEscaper.Replace(s) }

// Phrase translates one plain piece of text (not markup), such as a message
// inside an API answer.
// Messages the server builds (delivery dates, promotion progress) are also
// matched against phrases with variable parts; page text never is, so a
// store's own sentence is not half-translated by a pattern.
func Phrase(s string, look Lookup) string {
	out := phrase(s, look, func(x string) string { return x })
	if out != s {
		return out
	}
	core := strings.TrimSpace(s)
	if core == "" {
		return s
	}
	if t, ok := byPattern(spaces.ReplaceAllString(core, " "), look, 0); ok {
		return s[:strings.Index(s, core)] + t + s[strings.Index(s, core)+len(core):]
	}
	return s
}
