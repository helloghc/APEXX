package catalog

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// CSV limits.
const (
	MaxCSVBytes = 5 << 20
	MaxCSVRows  = 20000
)

// CSVColumns are the fixed columns of the import/export format, one row per
// variant. Attribute columns follow as "attr:<attribute id>".
var CSVColumns = []string{
	"handle", "title", "description", "status", "brand", "categories", "type", "tags",
	"seo_title", "seo_description",
	"option1_name", "option1_value", "option2_name", "option2_value", "option3_name", "option3_value",
	"sku", "barcode", "weight_grams", "price", "compare_at_price", "stock",
}

// ErrCSVFormat is returned when the file cannot be read as a product CSV at all.
var ErrCSVFormat = errors.New("catalog: invalid csv")

// RowError is a validation problem of one cell. Row is the line number in the
// file (the header is line 1).
type RowError struct {
	Row     int    `json:"row"`
	Column  string `json:"column"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ImportVariant is a parsed variant row. Nil pointers mean "cell empty: keep".
type ImportVariant struct {
	Row         int               `json:"row"`
	SKU         string            `json:"sku"`
	Options     map[string]string `json:"options,omitempty"`
	Barcode     string            `json:"barcode,omitempty"`
	WeightGrams *int64            `json:"weightGrams,omitempty"`
	Price       *int64            `json:"price,omitempty"`
	CompareAt   *int64            `json:"compareAt,omitempty"`
	Stock       *int64            `json:"stock,omitempty"`
}

// ImportProduct is a product assembled from every row that shares its handle.
type ImportProduct struct {
	Row            int               `json:"row"`
	Handle         string            `json:"handle"`
	Title          string            `json:"title"`
	Description    string            `json:"description,omitempty"`
	Status         string            `json:"status,omitempty"`
	Brand          string            `json:"brand,omitempty"`
	Categories     []string          `json:"categories,omitempty"` // paths: "Ropa > Playeras"
	Type           string            `json:"type,omitempty"`
	Tags           []string          `json:"tags,omitempty"`
	SEOTitle       string            `json:"seoTitle,omitempty"`
	SEODescription string            `json:"seoDescription,omitempty"`
	OptionNames    []string          `json:"optionNames,omitempty"`
	Attributes     map[string]string `json:"attributes,omitempty"`
	Variants       []ImportVariant   `json:"variants"`
	Exists         bool              `json:"exists"`  // updates a product already in the catalog
	Invalid        bool              `json:"invalid"` // has row errors; it is skipped when applying
}

// ImportPlan is the result of validating a CSV file.
type ImportPlan struct {
	Columns  []string        `json:"columns"`
	Has      map[string]bool `json:"-"`
	Products []ImportProduct `json:"products"`
	Errors   []RowError      `json:"errors"`
	Warnings []string        `json:"warnings"`
	Rows     int             `json:"rows"`
}

// ImportContext gives the validator what it needs to know about the catalog.
type ImportContext struct {
	SKUOwner     func(sku string) (handle string, ok bool)
	HandleExists func(handle string) bool
	Attribute    func(id string) (AttributeDef, bool)
	// AttributeByName finds an attribute by its name ("Material"), so a
	// column named like an attribute is read as that attribute. Optional.
	AttributeByName func(name string) (AttributeDef, bool)
}

// columnAliases are the names people and other platforms' exports give to
// the import's columns (folded: lowercase, no accents). The importer reads
// them as the canonical column and says so in the preview.
var columnAliases = map[string]string{
	"nombre": "title", "titulo": "title", "producto": "title", "nombre del producto": "title", "name": "title", "product name": "title",
	"descripcion": "description", "body html": "description", "body": "description", "descripcion larga": "description",
	"precio": "price", "precio venta": "price", "precio de venta": "price", "variant price": "price", "precio publico": "price",
	"precio anterior": "compare_at_price", "precio de lista": "compare_at_price", "precio regular": "compare_at_price", "precio antes": "compare_at_price",
	"compare at price": "compare_at_price", "variant compare at price": "compare_at_price",
	"existencias": "stock", "existencia": "stock", "inventario": "stock", "cantidad": "stock", "stock disponible": "stock", "variant inventory qty": "stock", "inventory": "stock", "qty": "stock",
	"codigo": "sku", "clave": "sku", "referencia": "sku", "variant sku": "sku", "codigo interno": "sku", "modelo": "sku",
	"codigo de barras": "barcode", "ean": "barcode", "upc": "barcode", "gtin": "barcode", "variant barcode": "barcode",
	"peso": "weight_grams", "peso g": "weight_grams", "peso gramos": "weight_grams", "peso en gramos": "weight_grams", "variant grams": "weight_grams", "grams": "weight_grams",
	"marca": "brand", "vendor": "brand", "fabricante": "brand",
	"categoria": "categories", "categorias": "categories", "category": "categories", "product category": "categories", "departamento": "categories",
	"tipo": "type", "product type": "type", "tipo de producto": "type",
	"etiquetas": "tags", "palabras clave": "tags",
	"estado": "status", "estatus": "status",
	"url": "handle", "slug": "handle", "url amigable": "handle",
	"titulo seo": "seo_title", "seo title": "seo_title", "meta titulo": "seo_title",
	"descripcion seo": "seo_description", "seo description": "seo_description", "meta descripcion": "seo_description",
}

// mapColumn reads a header as a canonical column: itself, an alias, or an
// attribute named like it. mapped is false when the header was already canonical.
func mapColumn(h string, ctx ImportContext, known map[string]bool) (col string, mapped bool) {
	if known[h] || strings.HasPrefix(h, "attr:") {
		return h, false
	}
	f := strings.Join(strings.Fields(strings.NewReplacer("(", " ", ")", " ", "_", " ", "-", " ", ".", " ").Replace(fold(h))), " ")
	if c, ok := columnAliases[f]; ok {
		return c, true
	}
	if ctx.AttributeByName != nil {
		if a, ok := ctx.AttributeByName(h); ok {
			return "attr:" + a.ID, true
		}
	}
	return h, false
}

// ParseAmount converts "1,299.50", "$1299" or "1299,5" into minor units.
func ParseAmount(s string) (int64, bool) {
	s = strings.ReplaceAll(strings.TrimPrefix(strings.TrimSpace(s), "$"), " ", "")
	if s == "" {
		return 0, false
	}
	if i := strings.LastIndex(s, ","); i >= 0 && !strings.Contains(s, ".") && len(s)-i-1 <= 2 && len(s)-i-1 >= 1 {
		s = s[:i] + "." + s[i+1:]
	}
	s = strings.ReplaceAll(s, ",", "")
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" || len(frac) > 2 || len(whole) > 12 {
		return 0, false
	}
	for _, r := range whole + frac {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	w, _ := strconv.ParseInt(whole, 10, 64)
	f, _ := strconv.ParseInt((frac + "00")[:2], 10, 64)
	return w*100 + f, true
}

// FormatAmount renders minor units as "1299.50".
func FormatAmount(a int64) string {
	return fmt.Sprintf("%d.%02d", a/100, a%100)
}

func splitList(s, sep string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range strings.Split(s, sep) {
		p = strings.TrimSpace(p)
		if p != "" && !seen[fold(p)] {
			seen[fold(p)] = true
			out = append(out, p)
		}
	}
	return out
}

// detectDelimiter picks ';' when the header has more of them than commas
// (spreadsheets in Spanish locales export with semicolons).
func detectDelimiter(data []byte) rune {
	line, _, _ := bytes.Cut(data, []byte("\n"))
	if bytes.Count(line, []byte(";")) > bytes.Count(line, []byte(",")) {
		return ';'
	}
	if bytes.Count(line, []byte("\t")) > bytes.Count(line, []byte(",")) {
		return '\t'
	}
	return ','
}

// ParseCSV reads and validates a product CSV. It never writes anything: the
// plan lists what would be created or updated and every row-level error.
func ParseCSV(data []byte, ctx ImportContext) (*ImportPlan, error) {
	if len(data) > MaxCSVBytes {
		return nil, fmt.Errorf("%w: el archivo pasa de %d MB", ErrCSVFormat, MaxCSVBytes>>20)
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("%w: el archivo está vacío", ErrCSVFormat)
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = detectDelimiter(data)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	r.TrimLeadingSpace = true
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("%w: no se pudo leer el encabezado", ErrCSVFormat)
	}
	plan := &ImportPlan{Has: map[string]bool{}}
	known := map[string]bool{}
	for _, c := range CSVColumns {
		known[c] = true
	}
	col := map[string]int{}
	attrCols := map[string]AttributeDef{}
	for i, h := range header {
		orig := strings.TrimSpace(h)
		h = strings.ToLower(orig)
		if h == "" {
			continue
		}
		if c, mapped := mapColumn(h, ctx, known); mapped {
			if _, dup := col[c]; dup {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("La columna %q se ignora: ya hay otra columna leída como %q", orig, c))
				continue
			}
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("La columna %q se leyó como %q", orig, c))
			h = c
		}
		if _, dup := col[h]; dup {
			return nil, fmt.Errorf("%w: la columna %q está repetida", ErrCSVFormat, h)
		}
		switch {
		case known[h]:
		case strings.HasPrefix(h, "attr:"):
			id := strings.TrimPrefix(h, "attr:")
			a, ok := ctx.Attribute(id)
			if !ok {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("La columna %q se ignora: no existe el atributo %q", h, id))
				continue
			}
			attrCols[h] = a
		default:
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("La columna %q no se reconoce y se ignora", h))
			continue
		}
		col[h] = i
		plan.Has[h] = true
		plan.Columns = append(plan.Columns, h)
	}
	// Without handles, the product name makes them ("Tenis Pacífico" ->
	// tenis-pacifico); variant rows without a name belong to the product above.
	deriveHandle := !plan.Has["handle"] && plan.Has["title"]
	if deriveHandle {
		plan.Has["handle"] = true
		plan.Warnings = append(plan.Warnings, "No hay columna handle: cada producto toma el suyo de su nombre")
	}
	for _, need := range []string{"handle", "sku"} {
		if !plan.Has[need] {
			return nil, fmt.Errorf("%w: falta la columna obligatoria %q", ErrCSVFormat, need)
		}
	}

	byHandle := map[string]int{}
	skuRow := map[string]int{}
	bad := map[string]bool{}
	addErr := func(handle string, row int, column, code, msg string) {
		if len(plan.Errors) < 1000 {
			plan.Errors = append(plan.Errors, RowError{Row: row, Column: column, Code: code, Message: msg})
		}
		if handle != "" {
			bad[handle] = true
		}
	}
	line := 1
	lastTitleHandle := ""
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			addErr("", line, "", "malformed", "La fila no se pudo leer; revisa las comillas")
			continue
		}
		get := func(name string) string {
			i, ok := col[name]
			if !ok || i >= len(rec) {
				return ""
			}
			v := strings.TrimSpace(rec[i])
			// Undo the guard added on export against spreadsheet formulas.
			if len(v) > 1 && v[0] == '\'' && strings.ContainsRune("=+-@", rune(v[1])) {
				v = v[1:]
			}
			return v
		}
		blank := true
		for _, c := range rec {
			blank = blank && strings.TrimSpace(c) == ""
		}
		if blank {
			continue
		}
		plan.Rows++
		if plan.Rows > MaxCSVRows {
			return nil, fmt.Errorf("%w: el archivo pasa de %d filas; divídelo en varios", ErrCSVFormat, MaxCSVRows)
		}
		handle := get("handle")
		if deriveHandle {
			if t := get("title"); t != "" {
				lastTitleHandle = Slug(t)
			}
			handle = lastTitleHandle
		}
		if !ValidHandle(handle) {
			addErr("", line, "handle", "invalid", "handle es obligatorio y solo admite minúsculas, números y guiones (por ejemplo tenis-pacifico)")
			continue
		}
		idx, seen := byHandle[handle]
		if !seen {
			idx = len(plan.Products)
			byHandle[handle] = idx
			plan.Products = append(plan.Products, ImportProduct{Row: line, Handle: handle, Exists: ctx.HandleExists(handle), Attributes: map[string]string{}})
		}
		p := &plan.Products[idx]
		setOnce := func(dst *string, name string, maxLen int) {
			v := get(name)
			if v == "" || *dst != "" {
				return
			}
			if len(v) > maxLen {
				addErr(handle, line, name, "too_long", fmt.Sprintf("%s no puede pasar de %d caracteres", name, maxLen))
				return
			}
			*dst = v
		}
		setOnce(&p.Title, "title", 200)
		setOnce(&p.Description, "description", 20000)
		setOnce(&p.Brand, "brand", 120)
		setOnce(&p.Type, "type", 60)
		setOnce(&p.SEOTitle, "seo_title", 120)
		setOnce(&p.SEODescription, "seo_description", 320)
		if v := strings.ToLower(get("status")); v != "" && p.Status == "" {
			switch v {
			case "active", "draft", "archived":
				p.Status = v
			case "activo":
				p.Status = "active"
			case "borrador":
				p.Status = "draft"
			case "archivado":
				p.Status = "archived"
			default:
				addErr(handle, line, "status", "invalid", "status debe ser active, draft o archived")
			}
		}
		if v := get("categories"); v != "" && p.Categories == nil {
			for _, path := range splitList(v, "|") {
				parts := splitList(path, ">")
				if len(parts) == 0 || len(parts) > 6 {
					addErr(handle, line, "categories", "invalid", "Cada categoría es una ruta de hasta 6 niveles: Ropa > Playeras")
					continue
				}
				p.Categories = append(p.Categories, strings.Join(parts, " > "))
			}
		}
		if v := get("tags"); v != "" && p.Tags == nil {
			p.Tags = splitList(v, ",")
			if len(p.Tags) > 50 {
				addErr(handle, line, "tags", "too_many", "Un producto admite hasta 50 etiquetas")
			}
		}
		for h, a := range attrCols {
			raw := get(h)
			if raw == "" || p.Attributes[a.ID] != "" {
				continue
			}
			v, msg := ValidateAttributeValue(a, raw)
			if msg != "" {
				addErr(handle, line, h, "invalid", msg)
				continue
			}
			p.Attributes[a.ID] = v
		}

		v := ImportVariant{Row: line, SKU: get("sku"), Barcode: get("barcode")}
		if v.SKU == "" || len(v.SKU) > 80 || strings.ContainsAny(v.SKU, " \t/|") {
			addErr(handle, line, "sku", "invalid", "sku es obligatorio, sin espacios ni diagonales, hasta 80 caracteres")
		} else if first, dup := skuRow[v.SKU]; dup {
			addErr(handle, line, "sku", "duplicate", fmt.Sprintf("El SKU %s ya aparece en la fila %d", v.SKU, first))
		} else {
			skuRow[v.SKU] = line
			if owner, ok := ctx.SKUOwner(v.SKU); ok && owner != handle {
				addErr(handle, line, "sku", "taken", fmt.Sprintf("El SKU %s ya pertenece al producto %s", v.SKU, owner))
			}
		}
		if len(v.Barcode) > 60 {
			addErr(handle, line, "barcode", "too_long", "barcode no puede pasar de 60 caracteres")
		}
		var names []string
		for n := 1; n <= MaxOptions; n++ {
			name, val := get(fmt.Sprintf("option%d_name", n)), get(fmt.Sprintf("option%d_value", n))
			if name == "" && val == "" {
				continue
			}
			if name == "" || val == "" {
				addErr(handle, line, fmt.Sprintf("option%d_value", n), "invalid", fmt.Sprintf("La opción %d necesita nombre y valor", n))
				continue
			}
			if len(name) > 40 || len(val) > 60 {
				addErr(handle, line, fmt.Sprintf("option%d_value", n), "too_long", "Los nombres de opción admiten 40 caracteres y los valores 60")
				continue
			}
			if v.Options == nil {
				v.Options = map[string]string{}
			}
			if _, dup := v.Options[name]; dup {
				addErr(handle, line, fmt.Sprintf("option%d_name", n), "duplicate", fmt.Sprintf("La opción %q está repetida en la fila", name))
				continue
			}
			v.Options[name] = val
			names = append(names, name)
		}
		if len(p.Variants) == 0 {
			p.OptionNames = names
		} else if strings.Join(names, "\x1f") != strings.Join(p.OptionNames, "\x1f") {
			addErr(handle, line, "option1_name", "inconsistent", "Todas las filas de un producto deben usar las mismas opciones, en el mismo orden")
		} else {
			for _, other := range p.Variants {
				same := len(names) > 0
				for _, n := range names {
					same = same && fold(other.Options[n]) == fold(v.Options[n])
				}
				if same {
					addErr(handle, line, "option1_value", "duplicate", fmt.Sprintf("La combinación de opciones repite la de la fila %d", other.Row))
					break
				}
				if len(names) == 0 {
					addErr(handle, line, "option1_name", "required", "Un producto con varias filas necesita opciones para distinguir sus variantes")
					break
				}
			}
		}
		intCell := func(name string, max int64) *int64 {
			raw := get(name)
			if raw == "" {
				return nil
			}
			n, err := strconv.ParseInt(strings.ReplaceAll(raw, ",", ""), 10, 64)
			if err != nil || n < 0 || n > max {
				addErr(handle, line, name, "invalid", fmt.Sprintf("%s debe ser un entero entre 0 y %d", name, max))
				return nil
			}
			return &n
		}
		v.WeightGrams = intCell("weight_grams", 10_000_000)
		v.Stock = intCell("stock", 1_000_000_000)
		moneyCell := func(name string) *int64 {
			raw := get(name)
			if raw == "" {
				return nil
			}
			n, ok := ParseAmount(raw)
			if !ok {
				addErr(handle, line, name, "invalid", fmt.Sprintf("%s debe ser un monto como 1299.50", name))
				return nil
			}
			return &n
		}
		v.Price = moneyCell("price")
		v.CompareAt = moneyCell("compare_at_price")
		if v.Price != nil && v.CompareAt != nil && *v.CompareAt < *v.Price {
			addErr(handle, line, "compare_at_price", "invalid", "compare_at_price (precio anterior) no puede ser menor que price")
		}
		if v.CompareAt != nil && v.Price == nil && !p.Exists {
			addErr(handle, line, "price", "required", "Falta price para usar compare_at_price")
		}
		p.Variants = append(p.Variants, v)
	}
	if plan.Rows == 0 {
		return nil, fmt.Errorf("%w: el archivo solo tiene encabezado", ErrCSVFormat)
	}
	for i := range plan.Products {
		p := &plan.Products[i]
		if !p.Exists && p.Title == "" {
			addErr(p.Handle, p.Row, "title", "required", "title es obligatorio para un producto nuevo")
		}
		if len(p.Variants) > MaxVariants {
			addErr(p.Handle, p.Row, "sku", "too_many", fmt.Sprintf("Un producto admite hasta %d variantes", MaxVariants))
		}
		p.Invalid = bad[p.Handle]
	}
	return plan, nil
}

// ExportInfo is what the exporter needs besides the product itself.
type ExportInfo struct {
	Brand      string
	Categories []string         // full paths
	Price      map[string]int64 // by SKU
	CompareAt  map[string]int64
	Stock      map[string]int64
}

// ExportHeader returns the CSV header for the given attribute definitions.
func ExportHeader(attrs []AttributeDef) []string {
	h := append([]string{}, CSVColumns...)
	for _, a := range attrs {
		h = append(h, "attr:"+a.ID)
	}
	return h
}

// guard neutralizes cells a spreadsheet would run as a formula.
func guard(s string) string {
	if s != "" && strings.ContainsRune("=+-@", rune(s[0])) {
		return "'" + s
	}
	return s
}

// ExportRows renders p as CSV rows, one per variant. Product-level cells are
// written on the first row only, which is also how ParseCSV reads them.
func ExportRows(p *Product, info ExportInfo, attrs []AttributeDef) [][]string {
	variants := p.Variants
	if len(variants) == 0 {
		variants = []Variant{{}}
	}
	rows := make([][]string, 0, len(variants))
	for i, v := range variants {
		row := make([]string, 0, len(CSVColumns)+len(attrs))
		if i == 0 {
			row = append(row, p.Handle, guard(p.Title), guard(p.Description), p.Status, guard(info.Brand),
				guard(strings.Join(info.Categories, " | ")), guard(p.Type), guard(strings.Join(p.Tags, ", ")),
				guard(p.SEOTitle), guard(p.SEODescription))
		} else {
			row = append(row, p.Handle, "", "", "", "", "", "", "", "", "")
		}
		for n := 0; n < MaxOptions; n++ {
			if n < len(p.Options) {
				row = append(row, guard(p.Options[n].Name), guard(v.Options[p.Options[n].Name]))
			} else {
				row = append(row, "", "")
			}
		}
		price, cmp, stock := "", "", ""
		if a, ok := info.Price[v.SKU]; ok {
			price = FormatAmount(a)
		}
		if a, ok := info.CompareAt[v.SKU]; ok {
			cmp = FormatAmount(a)
		}
		if a, ok := info.Stock[v.SKU]; ok {
			stock = strconv.FormatInt(a, 10)
		}
		row = append(row, guard(v.SKU), guard(v.Barcode), strconv.FormatInt(v.WeightGrams, 10), price, cmp, stock)
		for _, a := range attrs {
			if i == 0 {
				row = append(row, guard(p.Attributes[a.ID]))
			} else {
				row = append(row, "")
			}
		}
		rows = append(rows, row)
	}
	return rows
}
