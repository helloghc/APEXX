package catalog

import (
	"strings"
	"testing"
)

func TestImportMapsColumnNames(t *testing.T) {
	ctx := ImportContext{
		SKUOwner:     func(string) (string, bool) { return "", false },
		HandleExists: func(string) bool { return false },
		Attribute: func(id string) (AttributeDef, bool) {
			return AttributeDef{ID: id, Name: "Material", Kind: AttrText}, id == "material"
		},
		AttributeByName: func(name string) (AttributeDef, bool) {
			if fold(name) == "material" {
				return AttributeDef{ID: "material", Name: "Material", Kind: AttrText}, true
			}
			return AttributeDef{}, false
		},
	}
	// A spreadsheet with the names people use, without handles.
	csv := "Nombre,Descripción,Código,Precio,Existencias,Marca,Material,Color favorito\n" +
		"Tenis Pacífico,Para correr,TP-1,1299.00,5,Pacífico,Malla,rojo\n" +
		"Playera Básica,Algodón,PB-1,199,10,Urbana,Algodón,azul\n"
	plan, err := ParseCSV([]byte(csv), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Errors) != 0 {
		t.Fatalf("errors: %+v", plan.Errors)
	}
	if len(plan.Products) != 2 {
		t.Fatalf("products: %+v", plan.Products)
	}
	p := plan.Products[0]
	if p.Handle != "tenis-pacifico" || p.Title != "Tenis Pacífico" || p.Brand != "Pacífico" || p.Attributes["material"] != "Malla" {
		t.Fatalf("product: %+v", p)
	}
	v := p.Variants[0]
	if v.SKU != "TP-1" || v.Price == nil || *v.Price != 129900 || v.Stock == nil || *v.Stock != 5 {
		t.Fatalf("variant: %+v", v)
	}
	w := strings.Join(plan.Warnings, " | ")
	for _, want := range []string{`"Nombre" se leyó como "title"`, `"Código" se leyó como "sku"`, `"Material" se leyó como "attr:material"`, `"color favorito" no se reconoce`, "toma el suyo de su nombre"} {
		if !strings.Contains(w, want) {
			t.Errorf("warning missing %q in %s", want, w)
		}
	}
	// Canonical files are read as before, without notes.
	plan, err = ParseCSV([]byte("handle,title,sku,price\nx-1,X,SKU-X,10\n"), ctx)
	if err != nil || len(plan.Warnings) != 0 || plan.Products[0].Handle != "x-1" {
		t.Fatalf("canonical file changed: %+v %v", plan, err)
	}
}
