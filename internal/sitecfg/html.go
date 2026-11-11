package sitecfg

import (
	"encoding/xml"
	"html"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// allowed lists the tags a policy page can contain. Everything else is
// dropped (its text stays), so a pasted document can never carry scripts,
// styles, frames or event handlers into the storefront.
var allowed = map[string]bool{
	"p": true, "br": true, "h2": true, "h3": true, "h4": true, "strong": true, "em": true, "u": true, "s": true,
	"ul": true, "ol": true, "li": true, "a": true, "blockquote": true, "hr": true,
}

// rename maps equivalent tags to the canonical one.
var rename = map[string]string{"b": "strong", "i": "em", "div": "p", "h1": "h2", "strike": "s", "del": "s"}

// dropWithContent lists elements whose content is removed too.
var dropWithContent = map[string]bool{"script": true, "style": true, "iframe": true, "object": true, "embed": true,
	"noscript": true, "template": true, "svg": true, "math": true, "head": true, "title": true, "textarea": true, "select": true}

var void = map[string]bool{"br": true, "hr": true}

var (
	tagRe  = regexp.MustCompile(`^<\s*(/?)\s*([a-zA-Z][a-zA-Z0-9]*)`)
	hrefRe = regexp.MustCompile(`(?i)\shref\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
)

// safeHref returns a cleaned link target, or "" when it is not allowed.
func safeHref(h string) string {
	h = strings.TrimSpace(html.UnescapeString(h))
	if h == "" || len(h) > 1000 || strings.ContainsAny(h, "\x00\r\n\t") {
		return ""
	}
	if strings.HasPrefix(h, "#") || (strings.HasPrefix(h, "/") && !strings.HasPrefix(h, "//")) {
		return h
	}
	u, err := url.Parse(h)
	if err != nil {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		if u.Host != "" {
			return u.String()
		}
	case "mailto", "tel":
		return h
	}
	return ""
}

// SanitizeHTML returns well-formed HTML that only contains allowed tags.
func SanitizeHTML(in string) string {
	var out strings.Builder
	var stack []string
	closeTo := func(name string) {
		idx := -1
		for i := len(stack) - 1; i >= 0; i-- {
			if stack[i] == name {
				idx = i
				break
			}
		}
		if idx < 0 {
			return
		}
		for i := len(stack) - 1; i >= idx; i-- {
			out.WriteString("</" + stack[i] + ">")
		}
		stack = stack[:idx]
	}
	text := func(s string) { out.WriteString(html.EscapeString(html.UnescapeString(s))) }
	for len(in) > 0 {
		lt := strings.IndexByte(in, '<')
		if lt < 0 {
			text(in)
			break
		}
		text(in[:lt])
		in = in[lt:]
		if strings.HasPrefix(in, "<!--") {
			end := strings.Index(in, "-->")
			if end < 0 {
				break
			}
			in = in[end+3:]
			continue
		}
		m := tagRe.FindStringSubmatch(in)
		gt := strings.IndexByte(in, '>')
		if m == nil || gt < 0 {
			// A lone "<": keep it as text.
			out.WriteString("&lt;")
			in = in[1:]
			continue
		}
		raw, closing, name := in[:gt+1], m[1] == "/", strings.ToLower(m[2])
		in = in[gt+1:]
		if dropWithContent[name] {
			if !closing {
				if end := strings.Index(strings.ToLower(in), "</"+name); end >= 0 {
					in = in[end:]
					if g := strings.IndexByte(in, '>'); g >= 0 {
						in = in[g+1:]
					} else {
						in = ""
					}
				} else {
					in = ""
				}
			}
			continue
		}
		if r, ok := rename[name]; ok {
			name = r
		}
		if !allowed[name] {
			continue
		}
		switch {
		case closing:
			if !void[name] {
				closeTo(name)
			}
		case void[name]:
			out.WriteString("<" + name + ">")
		case name == "a":
			href := ""
			if h := hrefRe.FindStringSubmatch(raw); h != nil {
				href = safeHref(h[1] + h[2] + h[3])
			}
			if href == "" {
				continue // no usable target: keep the text only
			}
			closeTo("a") // links do not nest
			out.WriteString(`<a href="` + html.EscapeString(href) + `"`)
			if strings.HasPrefix(href, "http") {
				out.WriteString(` rel="noopener noreferrer"`)
			}
			out.WriteString(">")
			stack = append(stack, "a")
		default:
			if name == "p" || name == "h2" || name == "h3" || name == "h4" {
				closeTo("p") // paragraphs do not nest
			}
			out.WriteString("<" + name + ">")
			stack = append(stack, name)
		}
	}
	for i := len(stack) - 1; i >= 0; i-- {
		out.WriteString("</" + stack[i] + ">")
	}
	return strings.TrimSpace(out.String())
}

var anyTag = regexp.MustCompile(`<[^>]*>`)

// PlainText strips tags from sanitized HTML.
func PlainText(s string) string {
	return strings.TrimSpace(html.UnescapeString(anyTag.ReplaceAllString(s, " ")))
}

// Excerpt returns up to n runes of plain text for meta descriptions.
func Excerpt(s string, n int) string {
	r := []rune(strings.Join(strings.Fields(PlainText(s)), " "))
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return string(r)
}

// ---------- sitemap and robots ----------

// SitemapURL is one entry of sitemap.xml.
type SitemapURL struct {
	Loc     string
	LastMod time.Time // zero = unknown, the tag is omitted (never invented)
}

type xmlURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

type xmlURLSet struct {
	XMLName xml.Name `xml:"urlset"`
	NS      string   `xml:"xmlns,attr"`
	URLs    []xmlURL `xml:"url"`
}

// Sitemap renders sitemap.xml. lastmod comes from the data of each page.
func Sitemap(urls []SitemapURL) []byte {
	set := xmlURLSet{NS: "http://www.sitemaps.org/schemas/sitemap/0.9"}
	for _, u := range urls {
		x := xmlURL{Loc: u.Loc}
		if !u.LastMod.IsZero() {
			x.LastMod = u.LastMod.UTC().Format(time.RFC3339)
		}
		set.URLs = append(set.URLs, x)
	}
	b, _ := xml.MarshalIndent(set, "", " ")
	return append([]byte(xml.Header), b...)
}

// Robots renders robots.txt: the purchase flow is never indexed, the store's
// own rules follow, and the sitemap is always announced.
func Robots(origin, base, extra string) string {
	var b strings.Builder
	b.WriteString("User-agent: *\n")
	for _, p := range []string{"/cart", "/checkout", "/gracias"} {
		b.WriteString("Disallow: " + base + p + "\n")
	}
	b.WriteString("Allow: " + base + "/\n")
	if e := strings.TrimSpace(extra); e != "" {
		b.WriteString("\n" + e + "\n")
	}
	b.WriteString("\nSitemap: " + origin + base + "/sitemap.xml\n")
	return b.String()
}
