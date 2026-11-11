package catalog

import (
	"bytes"
	"encoding/csv"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestGenerateVariantsMatrix(t *testing.T) {
	opts := []Option{{Name: "Color", Values: []string{"Rojo", "Azul"}}, {Name: "Talla", Values: []string{"S", "M", "L"}}}
	vs, err := GenerateVariants("playera-básica", opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 6 {
		t.Fatalf("got %d variants, want 6", len(vs))
	}
	if vs[0].Title != "Rojo / S" || vs[0].SKU != "PLAYERA-BASICA-ROJO-S" || vs[5].Title != "Azul / L" {
		t.Fatalf("order or naming: %+v … %+v", vs[0], vs[5])
	}
	seen := map[string]bool{}
	for _, v := range vs {
		if seen[v.SKU] {
			t.Fatalf("duplicate SKU %s", v.SKU)
		}
		seen[v.SKU] = true
		if len(v.Options) != 2 {
			t.Fatalf("options of %s: %v", v.SKU, v.Options)
		}
	}
	if err := ValidateVariantOptions(opts, vs); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateVariantsKeepsExistingWork(t *testing.T) {
	opts := []Option{{Name: "Talla", Values: []string{"S", "M"}}}
	existing := []Variant{{SKU: "MI-SKU-S", Title: "S", Options: map[string]string{"Talla": "S"}, Barcode: "750100", WeightGrams: 320, MediaID: "m_1"}}
	opts[0].Values = append(opts[0].Values, "L")
	vs, err := GenerateVariants("playera", opts, existing)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 3 || vs[0].SKU != "MI-SKU-S" || vs[0].Barcode != "750100" || vs[0].WeightGrams != 320 || vs[0].MediaID != "m_1" {
		t.Fatalf("existing variant was not kept: %+v", vs)
	}
	if vs[1].SKU != "PLAYERA-M" || vs[2].SKU != "PLAYERA-L" {
		t.Fatalf("new SKUs: %s %s", vs[1].SKU, vs[2].SKU)
	}
	// Removing a value drops its variant; a clashing SKU gets a suffix.
	vs, _ = GenerateVariants("playera", []Option{{Name: "Talla", Values: []string{"M"}}}, []Variant{{SKU: "PLAYERA-M", Options: map[string]string{"Talla": "S"}}})
	if len(vs) != 1 || vs[0].SKU != "PLAYERA-M-2" {
		t.Fatalf("clash: %+v", vs)
	}
}

func TestGenerateVariantsDefaultAndLimits(t *testing.T) {
	vs, err := GenerateVariants("Taza", nil, nil)
	if err != nil || len(vs) != 1 || vs[0].SKU != "TAZA" || vs[0].Title != "Única" {
		t.Fatalf("default variant: %+v %v", vs, err)
	}
	// From a single default variant to options: the weight carries over.
	vs, _ = GenerateVariants("Taza", []Option{{Name: "Color", Values: []string{"Blanca", "Negra"}}}, []Variant{{SKU: "TAZA", WeightGrams: 350}})
	if len(vs) != 2 || vs[0].WeightGrams != 350 {
		t.Fatalf("weight seed: %+v", vs)
	}
	for name, opts := range map[string][]Option{
		"too many options": {{Name: "A", Values: []string{"1"}}, {Name: "B", Values: []string{"1"}}, {Name: "C", Values: []string{"1"}}, {Name: "D", Values: []string{"1"}}},
		"duplicate name":   {{Name: "Talla", Values: []string{"S"}}, {Name: "talla", Values: []string{"M"}}},
		"duplicate value":  {{Name: "Talla", Values: []string{"S", "s"}}},
		"empty value":      {{Name: "Talla", Values: []string{"S", " "}}},
		"no values":        {{Name: "Talla"}},
	} {
		var me *MatrixError
		if _, err := GenerateVariants("x", opts, nil); !errors.As(err, &me) {
			t.Fatalf("%s: expected MatrixError, got %v", name, err)
		}
	}
	big := make([]string, 20)
	for i := range big {
		big[i] = string(rune('a'+i)) + "x"
	}
	_, err = GenerateVariants("x", []Option{{Name: "A", Values: big}, {Name: "B", Values: big}}, nil)
	var me *MatrixError
	if !errors.As(err, &me) || me.Code != "too_many_variants" {
		t.Fatalf("400 variants should be rejected: %v", err)
	}
}

func TestValidateVariantOptions(t *testing.T) {
	opts := []Option{{Name: "Talla", Values: []string{"S", "M"}}}
	bad := [][]Variant{
		{{SKU: "A", Options: map[string]string{"Talla": "XL"}}},
		{{SKU: "A", Options: map[string]string{}}},
		{{SKU: "A", Options: map[string]string{"Talla": "S"}}, {SKU: "B", Options: map[string]string{"Talla": "S"}}},
	}
	for i, vs := range bad {
		if ValidateVariantOptions(opts, vs) == nil {
			t.Fatalf("case %d accepted", i)
		}
	}
}

func importCtx() ImportContext {
	attrs := map[string]AttributeDef{
		"material": {ID: "material", Name: "Material", Kind: AttrSelect, Options: []string{"Algodón", "Poliéster"}},
		"peso":     {ID: "peso", Name: "Peso", Kind: AttrNumber},
	}
	return ImportContext{
		SKUOwner: func(sku string) (string, bool) {
			if sku == "TOMADO-1" {
				return "otro-producto", true
			}
			return "", false
		},
		HandleExists: func(h string) bool { return h == "ya-existe" },
		Attribute:    func(id string) (AttributeDef, bool) { a, ok := attrs[id]; return a, ok },
	}
}

func TestParseCSVValid(t *testing.T) {
	src := "\xef\xbb\xbfhandle,title,status,brand,categories,tags,option1_name,option1_value,sku,price,compare_at_price,stock,weight_grams,attr:material,columna_rara\n" +
		"playera-azul,Playera azul,active,Norte,Ropa > Playeras | Ofertas,\"verano, algodón\",Talla,S,PA-S,\"1,299.50\",1500,10,200,algodón,x\n" +
		"playera-azul,,,,,,Talla,M,PA-M,1299.5,,0,200,,\n" +
		"\n" +
		"ya-existe,,,,,,,,YE-1,99,,,,,\n"
	plan, err := ParseCSV([]byte(src), importCtx())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", plan.Errors)
	}
	if plan.Rows != 3 || len(plan.Products) != 2 {
		t.Fatalf("rows=%d products=%d", plan.Rows, len(plan.Products))
	}
	p := plan.Products[0]
	if p.Title != "Playera azul" || p.Brand != "Norte" || len(p.Categories) != 2 || p.Categories[0] != "Ropa > Playeras" || len(p.Tags) != 2 {
		t.Fatalf("product fields: %+v", p)
	}
	if len(p.Variants) != 2 || *p.Variants[0].Price != 129950 || *p.Variants[0].CompareAt != 150000 || *p.Variants[1].Price != 129950 || *p.Variants[1].Stock != 0 {
		t.Fatalf("variants: %+v", p.Variants)
	}
	if p.Attributes["material"] != "Algodón" {
		t.Fatalf("select attribute should be stored in its canonical form: %q", p.Attributes["material"])
	}
	if p.Exists || !plan.Products[1].Exists {
		t.Fatal("create/update detection")
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "columna_rara") {
		t.Fatalf("warnings: %v", plan.Warnings)
	}
}

func TestParseCSVRowErrors(t *testing.T) {
	src := "handle;title;status;option1_name;option1_value;sku;price;compare_at_price;stock;attr:peso\n" +
		"Mal Handle;X;active;;;A-1;10;;1;\n" + // 2 invalid handle
		"nuevo;;active;;;N-1;10;;1;\n" + // 3 new product without title (reported on its first row)
		"tenis;Tenis;publicado;Talla;25;T-25;abc;;-3;pesado\n" + // 4 status, price, stock, number attribute
		"tenis;;;Talla;25;T-25;100;;1;\n" + // 5 duplicate SKU
		"tenis;;;Color;Rojo;T-26;100;50;1;\n" + // 6 inconsistent options, compare < price
		"gorra;Gorra;active;Talla;U;TOMADO-1;100;;1;\n" + // 7 SKU of another product
		"bolsa;Bolsa;active;Talla;;B-1;100;;1;\n" // 8 option without value
	plan, err := ParseCSV([]byte(src), importCtx())
	if err != nil {
		t.Fatal(err)
	}
	want := map[int][]string{
		2: {"handle"},
		3: {"title"},
		4: {"status", "price", "stock", "attr:peso"},
		5: {"sku"},
		6: {"option1_name", "compare_at_price"},
		7: {"sku"},
		8: {"option1_value"},
	}
	got := map[int][]string{}
	for _, e := range plan.Errors {
		got[e.Row] = append(got[e.Row], e.Column)
		if e.Message == "" || e.Code == "" {
			t.Fatalf("error without message or code: %+v", e)
		}
	}
	for row, cols := range want {
		for _, c := range cols {
			if !contains(got[row], c) {
				t.Errorf("row %d: missing error on %q (got %v)", row, c, got[row])
			}
		}
	}
	for _, p := range plan.Products {
		if !p.Invalid {
			t.Errorf("product %s should be marked invalid", p.Handle)
		}
	}
}

func TestParseCSVFatal(t *testing.T) {
	for name, src := range map[string]string{
		"empty":         "",
		"header only":   "handle,sku\n",
		"missing sku":   "handle,title\na,b\n",
		"repeated cols": "handle,sku,sku\na,b,c\n",
	} {
		if _, err := ParseCSV([]byte(src), importCtx()); !errors.Is(err, ErrCSVFormat) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAmounts(t *testing.T) {
	for in, want := range map[string]int64{"1299.50": 129950, "$1,299.5": 129950, "1299,5": 129950, "12": 1200, "0.01": 1, "1 000": 100000} {
		if got, ok := ParseAmount(in); !ok || got != want {
			t.Errorf("%q = %d,%v want %d", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "abc", "-5", "1.234", "1..2"} {
		if _, ok := ParseAmount(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
	if FormatAmount(129950) != "1299.50" || FormatAmount(5) != "0.05" {
		t.Fatal("FormatAmount")
	}
}

func TestExportRoundTrip(t *testing.T) {
	attrs := []AttributeDef{{ID: "material", Name: "Material", Kind: AttrSelect, Options: []string{"Algodón", "Poliéster"}}}
	p := &Product{
		Handle: "playera", Title: "=Playera \"premium\", azul", Description: "Línea 1\nLínea 2", Status: "draft", Type: "Ropa", Tags: []string{"verano", "nuevo"},
		Options:    []Option{{Name: "Talla", Values: []string{"S", "M"}}},
		Attributes: map[string]string{"material": "Algodón"},
		Variants: []Variant{
			{SKU: "P-S", Options: map[string]string{"Talla": "S"}, Barcode: "750", WeightGrams: 200},
			{SKU: "P-M", Options: map[string]string{"Talla": "M"}, WeightGrams: 210},
		},
	}
	info := ExportInfo{Brand: "Norte", Categories: []string{"Ropa > Playeras"}, Price: map[string]int64{"P-S": 19900, "P-M": 19900}, CompareAt: map[string]int64{"P-S": 25000}, Stock: map[string]int64{"P-S": 4, "P-M": 0}}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write(ExportHeader(attrs))
	_ = w.WriteAll(ExportRows(p, info, attrs))
	if !strings.Contains(buf.String(), "'=Playera") {
		t.Fatal("formula cells must be neutralized on export")
	}
	ctx := importCtx()
	plan, err := ParseCSV(buf.Bytes(), ctx)
	if err != nil || len(plan.Errors) != 0 {
		t.Fatalf("round trip: %v %+v", err, plan)
	}
	g := plan.Products[0]
	if g.Title != p.Title || g.Description != p.Description || g.Status != "draft" || g.Brand != "Norte" || g.Categories[0] != "Ropa > Playeras" ||
		len(g.Variants) != 2 || *g.Variants[0].Price != 19900 || *g.Variants[0].CompareAt != 25000 || *g.Variants[1].Stock != 0 ||
		g.Variants[0].Barcode != "750" || *g.Variants[1].WeightGrams != 210 || g.Attributes["material"] != "Algodón" || g.Variants[1].Options["Talla"] != "M" {
		t.Fatalf("round trip lost data: %+v", g)
	}
}

// ---------- facets ----------

func facetFixture() (*Store, *Taxonomy, OfferFunc) {
	tx := NewTaxonomy()
	tx.SetCategory(Category{ID: "c_ropa", Handle: "ropa", Name: "Ropa", Active: true})
	tx.SetCategory(Category{ID: "c_play", ParentID: "c_ropa", Handle: "playeras", Name: "Playeras", Active: true, Position: 0})
	tx.SetCategory(Category{ID: "c_pant", ParentID: "c_ropa", Handle: "pantalones", Name: "Pantalones", Active: true, Position: 1})
	tx.SetCategory(Category{ID: "c_cal", Handle: "calzado", Name: "Calzado", Active: true, Position: 1})
	tx.SetBrand(Brand{ID: "b_norte", Handle: "norte", Name: "Norte", Active: true})
	tx.SetBrand(Brand{ID: "b_sierra", Handle: "sierra", Name: "Sierra", Active: true})
	tx.SetAttribute(AttributeDef{ID: "material", Name: "Material", Kind: AttrSelect, Options: []string{"Algodón", "Lino"}, Filterable: true})
	tx.SetAttribute(AttributeDef{ID: "interno", Name: "Nota interna", Kind: AttrText})
	s := New(nil)
	prices := map[string]int64{}
	stock := map[string]bool{}
	add := func(id, brandID, brandName, cat, material string, price int64, inStock bool, age int, tags ...string) {
		s.Upsert(Product{ID: id, Handle: strings.ToLower(id), Title: "Producto " + id, BrandID: brandID, Brand: brandName, Categories: []string{cat},
			Attributes: map[string]string{"material": material, "interno": "x"}, Tags: tags, Status: "active",
			Variants: []Variant{{SKU: id + "-1"}}, CreatedAt: time.Unix(int64(1000+age), 0), UpdatedAt: time.Unix(2000, 0)})
		prices[id], stock[id] = price, inStock
	}
	add("P1", "b_norte", "Norte", "c_play", "Algodón", 10000, true, 1, "verano")
	add("P2", "b_norte", "Norte", "c_play", "Lino", 20000, true, 2)
	add("P3", "b_sierra", "Sierra", "c_play", "Algodón", 30000, false, 3, "verano")
	add("P4", "b_sierra", "Sierra", "c_pant", "Algodón", 40000, true, 4)
	add("P5", "", "Pacífico", "Calzado", "", 50000, true, 5) // legacy: brand as text, category by name
	s.Upsert(Product{ID: "P6", Handle: "p6", Title: "Borrador", BrandID: "b_norte", Categories: []string{"c_play"}, Status: "draft"})
	offer := func(p *Product) Offer {
		return Offer{Price: prices[p.ID], HasPrice: prices[p.ID] > 0, InStock: stock[p.ID]}
	}
	return s, tx, offer
}

func counts(vs []FacetValue) map[string]int {
	m := map[string]int{}
	for _, v := range vs {
		m[v.Value] = v.Count
	}
	return m
}

func ids(ps []*Product) string {
	var out []string
	for _, p := range ps {
		out = append(out, p.ID)
	}
	return strings.Join(out, ",")
}

func TestBrowseFacetCounts(t *testing.T) {
	s, tx, offer := facetFixture()
	all := Browse(s.Products(), tx, offer, BrowseQuery{})
	if all.Total != 5 {
		t.Fatalf("total %d: drafts must not be listed", all.Total)
	}
	if c := counts(all.Categories); c["ropa"] != 4 || c["calzado"] != 1 {
		t.Fatalf("root categories (a product counts in its ancestors; legacy name resolves): %v", c)
	}
	if c := counts(all.Brands); c["norte"] != 2 || c["sierra"] != 2 || c["pacifico"] != 1 {
		t.Fatalf("brands: %v", c)
	}
	if len(all.Attrs) != 1 || all.Attrs[0].ID != "material" {
		t.Fatalf("only filterable attributes become facets: %+v", all.Attrs)
	}
	if c := counts(all.Attrs[0].Values); c["Algodón"] != 3 || c["Lino"] != 1 {
		t.Fatalf("material: %v", c)
	}
	if all.PriceFloor != 10000 || all.PriceCeil != 50000 {
		t.Fatalf("price range %d-%d", all.PriceFloor, all.PriceCeil)
	}

	// Category page: only its subtree; child categories with counts.
	ropa := Browse(s.Products(), tx, offer, BrowseQuery{CategoryID: "c_ropa"})
	if ropa.Total != 4 {
		t.Fatalf("ropa total %d", ropa.Total)
	}
	if c := counts(ropa.Categories); c["playeras"] != 3 || c["pantalones"] != 1 {
		t.Fatalf("children: %v", c)
	}

	// Disjunctive counts: selecting Norte narrows the results and the other
	// facets, but the brand facet still shows what Sierra would add.
	norte := Browse(s.Products(), tx, offer, BrowseQuery{CategoryID: "c_ropa", Brands: []string{"norte"}})
	if ids(norte.Items) != "P1,P2" {
		t.Fatalf("norte items: %s", ids(norte.Items))
	}
	if c := counts(norte.Brands); c["norte"] != 2 || c["sierra"] != 2 {
		t.Fatalf("brand facet must ignore its own selection: %v", c)
	}
	if c := counts(norte.Attrs[0].Values); c["Algodón"] != 1 || c["Lino"] != 1 {
		t.Fatalf("material under Norte: %v", c)
	}
	if !norte.Brands[0].Selected || norte.Brands[1].Selected {
		t.Fatalf("selected flags: %+v", norte.Brands)
	}

	// Two facets together, plus price.
	lo, hi := int64(15000), int64(35000)
	f := Browse(s.Products(), tx, offer, BrowseQuery{Attrs: map[string][]string{"material": {"Algodón"}}, PriceMin: &lo, PriceMax: &hi})
	if ids(f.Items) != "P3" {
		t.Fatalf("algodón 150-350: %s", ids(f.Items))
	}
	if c := counts(f.Attrs[0].Values); c["Algodón"] != 1 || c["Lino"] != 1 {
		t.Fatalf("material within price: %v", c)
	}
	if f.PriceFloor != 10000 || f.PriceCeil != 40000 {
		t.Fatalf("price bounds ignore the price filter: %d-%d", f.PriceFloor, f.PriceCeil)
	}
}

func TestBrowseSortAndPaging(t *testing.T) {
	s, tx, offer := facetFixture()
	ps := s.Products()
	if got := ids(Browse(ps, tx, offer, BrowseQuery{Sort: SortPriceDesc}).Items); got != "P5,P4,P3,P2,P1" {
		t.Fatalf("price desc: %s", got)
	}
	if got := ids(Browse(ps, tx, offer, BrowseQuery{Sort: SortPriceAsc, Limit: 2, Offset: 2}).Items); got != "P3,P4" {
		t.Fatalf("price asc page 2: %s", got)
	}
	if got := ids(Browse(ps, tx, offer, BrowseQuery{Sort: SortNewest, Limit: 3}).Items); got != "P5,P4,P3" {
		t.Fatalf("newest: %s", got)
	}
	// Relevance without a query: what can be bought now goes first.
	if got := ids(Browse(ps, tx, offer, BrowseQuery{}).Items); got != "P1,P2,P4,P5,P3" {
		t.Fatalf("relevance: %s", got)
	}
	if r := Browse(ps, tx, offer, BrowseQuery{Q: "producto p4"}); r.Total != 1 || r.Items[0].ID != "P4" {
		t.Fatalf("search: %s", ids(r.Items))
	}
}

func TestCollections(t *testing.T) {
	s, tx, offer := facetFixture()
	ps := s.Products()
	manual := &Collection{ID: "col1", Kind: CollectionManual, ProductIDs: []string{"P4", "P1", "P6"}}
	if got := ids(Browse(ps, tx, offer, BrowseQuery{Collection: manual}).Items); got != "P4,P1" {
		t.Fatalf("manual keeps its order and skips drafts: %s", got)
	}
	max := int64(30000)
	all := &Collection{Kind: CollectionRules, Rules: CollectionRuleSet{Match: "all", BrandIDs: []string{"b_sierra"}, PriceMax: &max}}
	if got := ids(Browse(ps, tx, offer, BrowseQuery{Collection: all}).Items); got != "P3" {
		t.Fatalf("rules all: %s", got)
	}
	anyOf := &Collection{Kind: CollectionRules, Rules: CollectionRuleSet{Match: "any", Tags: []string{"Verano"}, CategoryIDs: []string{"c_cal"}}}
	if got := ids(Browse(ps, tx, offer, BrowseQuery{Collection: anyOf, Sort: SortPriceAsc}).Items); got != "P1,P3,P5" {
		t.Fatalf("rules any: %s", got)
	}
	byCat := &Collection{Kind: CollectionRules, Rules: CollectionRuleSet{CategoryIDs: []string{"c_ropa"}}}
	if r := Browse(ps, tx, offer, BrowseQuery{Collection: byCat}); r.Total != 4 {
		t.Fatalf("category rule includes descendants: %d", r.Total)
	}
	if r := Browse(ps, tx, offer, BrowseQuery{Collection: &Collection{Kind: CollectionRules}}); r.Total != 0 {
		t.Fatal("a rule collection without rules is empty, not the whole catalog")
	}
}

func TestTaxonomyAndStore(t *testing.T) {
	s, tx, _ := facetFixture()
	if p := tx.Path("c_play"); len(p) != 2 || p[0].Handle != "ropa" {
		t.Fatalf("path: %+v", p)
	}
	if cs := tx.Categories(); cs[0].ID != "c_ropa" || cs[1].ID != "c_play" || cs[2].ID != "c_pant" || cs[3].ID != "c_cal" {
		t.Fatalf("depth-first order: %+v", cs)
	}
	tx.DeleteCategory("c_pant")
	if len(tx.Children("c_ropa")) != 1 {
		t.Fatal("delete category")
	}
	if Slug("  Tenis Pacífico #2 ") != "tenis-pacifico-2" || !ValidHandle("a-b-1") || ValidHandle("A b") || ValidHandle("-a") {
		t.Fatal("slug")
	}
	if _, ok := s.ByHandle("p6"); ok {
		t.Fatal("drafts are not visible by handle")
	}
	s.Delete("P1")
	if _, err := s.Get("P1"); err == nil || s.Count() != 5 {
		t.Fatal("delete product")
	}
	if _, ok := s.ProductOfSKU("P1-1"); ok {
		t.Fatal("SKU index after delete")
	}
	for raw, want := range map[string]string{"sí": "true", "No": "false", "x": ""} {
		if got, _ := ValidateAttributeValue(AttributeDef{Name: "B", Kind: AttrBoolean}, raw); got != want {
			t.Errorf("boolean %q = %q", raw, got)
		}
	}
	if got, _ := ValidateAttributeValue(AttributeDef{Name: "N", Kind: AttrNumber}, "12,50"); got != "12.5" {
		t.Errorf("number: %q", got)
	}
}
