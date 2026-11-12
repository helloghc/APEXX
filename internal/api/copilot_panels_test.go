package api

import (
	"testing"
	"time"

	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/promo"
	"github.com/helloghc/apexx/internal/reports"
)

func TestParseSegment(t *testing.T) {
	cases := []struct {
		q    string
		want map[string]any // field -> value
		op   map[string]string
	}{
		{"clientes que compraron más de 3 veces y gastaron más de $2,000", map[string]any{"orders_count": int64(4), "total_spent": int64(200000)}, map[string]string{"orders_count": "gte", "total_spent": "gte"}},
		{"no compran desde hace 3 meses y aceptan correos", map[string]any{"days_since_last_order": int64(90), "marketing_consent": true}, map[string]string{"days_since_last_order": "gte"}},
		{"compraron en los últimos 30 días, sin cuenta", map[string]any{"days_since_last_order": int64(30), "has_account": false}, map[string]string{"days_since_last_order": "lte"}},
		{"al menos 2 pedidos con la etiqueta mayoreo", map[string]any{"orders_count": int64(2), "tag": "mayoreo"}, nil},
		{"compraron solo una vez", map[string]any{"orders_count": 1}, map[string]string{"orders_count": "eq"}},
	}
	for _, c := range cases {
		r, why := parseSegment(c.q)
		got := map[string]any{}
		ops := map[string]string{}
		for _, cd := range r.Conditions {
			got[cd.Field], ops[cd.Field] = cd.Value, cd.Op
		}
		if len(got) != len(c.want) || len(why) != len(c.want) {
			t.Errorf("%q -> %v (%v)", c.q, got, why)
			continue
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Errorf("%q: %s = %v, want %v", c.q, k, got[k], v)
			}
		}
		for k, v := range c.op {
			if ops[k] != v {
				t.Errorf("%q: %s op %s, want %s", c.q, k, ops[k], v)
			}
		}
	}
	if r, _ := parseSegment("los mejores"); len(r.Conditions) != 0 {
		t.Fatalf("nothing to understand, got %v", r)
	}
}

func TestClassifyReturn(t *testing.T) {
	for text, want := range map[string]string{
		"Me quedó muy chica la talla":       "size",
		"Llegó rota la taza":                "damaged",
		"Me mandaron otro producto":         "wrong_item",
		"El material no es como en la foto": "not_as_described",
		"Ya no lo necesito, me arrepentí":   "changed_mind",
		"Llegó una semana tarde":            "late",
		"Lo quiero devolver":                "other",
	} {
		if code, _, _ := classifyReturn(text); code != want {
			t.Errorf("%q -> %s, want %s", text, code, want)
		}
	}
}

func TestParsePromotion(t *testing.T) {
	ten := testTenantWithCategory(t, "Calcetas")
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	p, why := parsePromotion(ten, "MXN", "3x2 en calcetas", now)
	if p.Type != promo.TypeBuyXGetY || p.BuyQty != 2 || p.GetQty != 1 || p.GetBps != 10000 || len(p.Target.Categories) != 1 || p.Status != promo.StatusDraft {
		t.Fatalf("3x2: %+v %v", p, why)
	}
	p, _ = parsePromotion(ten, "MXN", "20% en calcetas hasta el 30 de noviembre", now)
	if p.Type != promo.TypeItemPercent || p.Bps != 2000 || p.Conditions.EndsAt == nil || p.Conditions.EndsAt.In(reports.Zone).Day() != 30 || p.Conditions.EndsAt.In(reports.Zone).Hour() != 23 {
		t.Fatalf("20%%: %+v", p)
	}
	p, _ = parsePromotion(ten, "MXN", "Envío gratis desde $1,500", now)
	if p.Type != promo.TypeFreeShipping || p.Conditions.MinSubtotal != 150000 {
		t.Fatalf("free shipping: %+v", p)
	}
	p, _ = parsePromotion(ten, "MXN", "15% en toda la tienda con el cupón BUENFIN", now)
	if p.Type != promo.TypeOrderPercent || p.Bps != 1500 || !p.Conditions.RequiresCoupon {
		t.Fatalf("order percent with coupon: %+v", p)
	}
	if p, _ := parsePromotion(ten, "MXN", "algo bonito", now); p.Type != "" {
		t.Fatalf("nothing to understand, got %+v", p)
	}
}

func testTenantWithCategory(t *testing.T, name string) *projection.Tenant {
	tx := catalog.NewTaxonomy()
	tx.SetCategory(catalog.Category{ID: "cat_1", Handle: "calcetas", Name: name, Active: true})
	return &projection.Tenant{ID: "t", Catalog: catalog.New(nil), Taxo: tx}
}

func TestUselessAlt(t *testing.T) {
	for alt, want := range map[string]bool{"IMG_4021.JPG": true, "foto": true, "DSC-0091": true, "producto.png": true, "3f9a8c7d-1e2b-4c5d-9e8f-0a1b2c3d4e5f": true,
		"Tenis rojos para correr, vista lateral": false, "Taza blanca de cerámica": false, "": true} {
		if got := uselessAlt(alt); got != want {
			t.Errorf("%q -> %v, want %v", alt, got, want)
		}
	}
}
