package builder

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Op is one RFC 6902 JSON Patch operation. Moving a module on the page is a
// single {"op":"move","from":"/regions/main/3","path":"/regions/main/1"}.
type Op struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	From  string          `json:"from,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

// ApplyPatch applies ops to doc (decoded JSON) and returns the new document.
// It supports add, remove, replace, move, copy and test.
func ApplyPatch(doc any, ops []Op) (any, error) {
	for i, op := range ops {
		var err error
		switch op.Op {
		case "add", "replace", "test":
			var v any
			if err = json.Unmarshal(op.Value, &v); err != nil {
				return nil, fmt.Errorf("op %d: value: %w", i, err)
			}
			switch op.Op {
			case "add":
				doc, err = put(doc, ptr(op.Path), v, true)
			case "replace":
				doc, err = put(doc, ptr(op.Path), v, false)
			case "test":
				var cur any
				if cur, err = get(doc, ptr(op.Path)); err == nil {
					a, _ := json.Marshal(cur)
					b, _ := json.Marshal(v)
					if string(a) != string(b) {
						err = fmt.Errorf("test failed at %s", op.Path)
					}
				}
			}
		case "remove":
			doc, _, err = take(doc, ptr(op.Path))
		case "move", "copy":
			var v any
			if op.Op == "move" {
				if strings.HasPrefix(op.Path+"/", op.From+"/") && op.Path != op.From {
					return nil, fmt.Errorf("op %d: cannot move into itself", i)
				}
				doc, v, err = take(doc, ptr(op.From))
			} else {
				v, err = get(doc, ptr(op.From))
				v = deepCopy(v)
			}
			if err == nil {
				doc, err = put(doc, ptr(op.Path), v, true)
			}
		default:
			err = fmt.Errorf("unsupported op %q", op.Op)
		}
		if err != nil {
			return nil, fmt.Errorf("op %d (%s %s): %w", i, op.Op, op.Path, err)
		}
	}
	return doc, nil
}

func ptr(p string) []string {
	if p == "" {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, s := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
	}
	return parts
}

func index(tok string, n int, allowEnd bool) (int, error) {
	if tok == "-" && allowEnd {
		return n, nil
	}
	i, err := strconv.Atoi(tok)
	if err != nil || i < 0 || i > n || (i == n && !allowEnd) {
		return 0, fmt.Errorf("index %q out of range", tok)
	}
	return i, nil
}

func get(doc any, path []string) (any, error) {
	cur := doc
	for _, tok := range path {
		switch c := cur.(type) {
		case map[string]any:
			v, ok := c[tok]
			if !ok {
				return nil, fmt.Errorf("missing key %q", tok)
			}
			cur = v
		case []any:
			i, err := index(tok, len(c), false)
			if err != nil {
				return nil, err
			}
			cur = c[i]
		default:
			return nil, fmt.Errorf("cannot traverse into %q", tok)
		}
	}
	return cur, nil
}

// put sets path to v. insert=true inserts into arrays (add); false replaces.
func put(doc any, path []string, v any, insert bool) (any, error) {
	if len(path) == 0 {
		return v, nil
	}
	parent, err := get(doc, path[:len(path)-1])
	if err != nil {
		return nil, err
	}
	last := path[len(path)-1]
	switch p := parent.(type) {
	case map[string]any:
		if _, ok := p[last]; !ok && !insert {
			return nil, fmt.Errorf("missing key %q", last)
		}
		p[last] = v
		return doc, nil
	case []any:
		i, err := index(last, len(p), insert)
		if err != nil {
			return nil, err
		}
		var np []any
		if insert {
			np = append(append(append(make([]any, 0, len(p)+1), p[:i]...), v), p[i:]...)
		} else {
			np = p
			np[i] = v
		}
		return put(doc, path[:len(path)-1], np, false)
	}
	return nil, fmt.Errorf("cannot set %q", last)
}

func take(doc any, path []string) (any, any, error) {
	if len(path) == 0 {
		return nil, nil, fmt.Errorf("cannot remove the root")
	}
	parent, err := get(doc, path[:len(path)-1])
	if err != nil {
		return nil, nil, err
	}
	last := path[len(path)-1]
	switch p := parent.(type) {
	case map[string]any:
		v, ok := p[last]
		if !ok {
			return nil, nil, fmt.Errorf("missing key %q", last)
		}
		delete(p, last)
		return doc, v, nil
	case []any:
		i, err := index(last, len(p), false)
		if err != nil {
			return nil, nil, err
		}
		v := p[i]
		np := append(append(make([]any, 0, len(p)-1), p[:i]...), p[i+1:]...)
		doc, err = put(doc, path[:len(path)-1], np, false)
		return doc, v, err
	}
	return nil, nil, fmt.Errorf("cannot remove %q", last)
}

func deepCopy(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}
