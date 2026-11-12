// Package help is the panel's own help: one section per task, with the
// screen that does it and the permission it needs. The copilot answers
// "¿cómo hago…?" with these sections and a link to the screen.
package help

import (
	_ "embed"
	"strings"
)

//go:embed ayuda.md
var source string

// Topic is one task of the panel.
type Topic struct {
	Title  string
	Route  string // panel route, after "#"
	Module string // permission needed: module and level
	Level  string
	Body   string
}

// Topics parses the help.
func Topics() []Topic {
	var out []Topic
	for _, sec := range strings.Split(source, "\n## ")[1:] {
		lines := strings.Split(strings.TrimSpace(sec), "\n")
		t := Topic{Title: strings.TrimSpace(lines[0])}
		var body []string
		for _, l := range lines[1:] {
			switch {
			case strings.HasPrefix(l, "ruta: "):
				t.Route = strings.TrimSpace(strings.TrimPrefix(l, "ruta: "))
			case strings.HasPrefix(l, "permiso: "):
				f := strings.Fields(strings.TrimPrefix(l, "permiso: "))
				if len(f) == 2 {
					t.Module, t.Level = f[0], f[1]
				}
			case strings.TrimSpace(l) != "":
				body = append(body, strings.TrimSpace(l))
			}
		}
		t.Body = strings.Join(body, " ")
		out = append(out, t)
	}
	return out
}
