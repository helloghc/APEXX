package reports

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Table is the flat form of a report, for CSV export.
type Table struct {
	Header []string
	Rows   [][]string
}

// decimal renders minor units as a plain decimal: 129950 → "1299.50".
func decimal(v int64) string {
	neg := ""
	if v < 0 {
		neg, v = "-", -v
	}
	return fmt.Sprintf("%s%d.%02d", neg, v/100, v%100)
}

func n(v int64) string { return strconv.FormatInt(v, 10) }

func pct(num, den int64) string {
	if den == 0 {
		return "0.00"
	}
	return strconv.FormatFloat(float64(num)*100/float64(den), 'f', 2, 64)
}

// safe neutralises spreadsheet formulas in text that came from store data.
func safe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// WriteCSV writes t as UTF-8 CSV with a BOM, so Excel opens accents correctly.
func WriteCSV(w io.Writer, t Table) error {
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	cw.UseCRLF = true
	if err := cw.Write(t.Header); err != nil {
		return err
	}
	if err := cw.WriteAll(t.Rows); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

// Table of the sales report: one row per bucket.
func (r SalesReport) Table() Table {
	t := Table{Header: []string{"periodo", "moneda", "pedidos", "unidades", "ventas_brutas", "descuentos", "envio", "reembolsos", "ventas_netas", "iva_incluido", "ticket_promedio", "pedidos_clientes_nuevos", "pedidos_clientes_recurrentes"}}
	row := func(b string, s Sales) []string {
		return []string{b, r.Currency, n(s.Orders), n(s.Units), decimal(s.Gross), decimal(s.Discounts), decimal(s.Shipping), decimal(s.Refunds), decimal(s.Net), decimal(s.Tax), decimal(s.AOV), n(s.NewCustomers), n(s.ReturningCustomers)}
	}
	for _, p := range r.Series {
		t.Rows = append(t.Rows, row(p.Bucket, p.Sales))
	}
	t.Rows = append(t.Rows, row("TOTAL", r.Totals))
	return t
}

// Table of the products report.
func (r ProductsReport) Table() Table {
	t := Table{Header: []string{"clave", "nombre", "marca", "moneda", "pedidos", "unidades", "ingresos", "participacion_pct", "vistas", "visitantes", "visitantes_que_agregaron"}}
	for _, p := range r.Rows {
		t.Rows = append(t.Rows, []string{safe(p.Key), safe(p.Name), safe(p.Brand), r.Currency, n(p.Orders), n(p.Units), decimal(p.Revenue), pct(p.Revenue, r.Totals.Revenue), n(p.Views), n(p.Visitors), n(p.CartVisitors)})
	}
	return t
}

// Table of the customers report: the top customers of the period.
func (r CustomersReport) Table() Table {
	t := Table{Header: []string{"cliente", "tipo", "pedidos", "moneda", "gastado_neto", "primer_pedido", "ultimo_pedido", "ultimo_numero_de_pedido"}}
	for _, c := range r.Top {
		kind := "recurrente"
		if c.New {
			kind = "nuevo"
		}
		t.Rows = append(t.Rows, []string{c.Customer, kind, n(c.Orders), r.Currency, decimal(c.Spent), c.First.In(Zone).Format("2006-01-02 15:04"), c.Last.In(Zone).Format("2006-01-02 15:04"), n(c.LastNumber)})
	}
	return t
}

// Table of the funnel report: one row per bucket.
func (r FunnelReport) Table() Table {
	t := Table{Header: []string{"periodo", "sesiones", "vieron_producto", "agregaron_al_carrito", "iniciaron_pago", "compraron", "conversion_pct", "paginas_vistas"}}
	row := func(b string, x Traffic) []string {
		return []string{b, n(x.Sessions), n(x.Product), n(x.Cart), n(x.Checkout), n(x.Purchase), pct(x.Purchase, x.Sessions), n(x.PageViews)}
	}
	for _, p := range r.Series {
		t.Rows = append(t.Rows, row(p.Bucket, p.Traffic))
	}
	t.Rows = append(t.Rows, row("TOTAL", r.Totals))
	return t
}

// Table of the promotions report.
func (r PromotionsReport) Table() Table {
	t := Table{Header: []string{"promocion", "nombre", "activa", "moneda", "pedidos", "descuento_otorgado", "ventas_de_esos_pedidos", "descuento_promedio", "pct_de_pedidos"}}
	for _, p := range r.Rows {
		avg := int64(0)
		if p.Orders > 0 {
			avg = p.Discount / p.Orders
		}
		act := "no"
		if p.Active {
			act = "sí"
		}
		t.Rows = append(t.Rows, []string{safe(p.ID), safe(p.Name), act, r.Currency, n(p.Orders), decimal(p.Discount), decimal(p.Revenue), decimal(avg), pct(p.Orders, r.Sales.Orders)})
	}
	return t
}

// Table of the inventory report.
func (r InventoryReport) Table() Table {
	t := Table{Header: []string{"sku", "producto", "existencias", "reservadas", "disponibles", "moneda", "precio", "valor_a_precio_de_venta", "vendidas_30_dias", "dias_de_cobertura", "estado"}}
	state := map[string]string{"ok": "con existencias", "low": "pocas existencias", "out": "agotado"}
	for _, x := range r.Rows {
		cover := ""
		if x.CoverDays >= 0 {
			cover = n(x.CoverDays)
		}
		t.Rows = append(t.Rows, []string{safe(x.SKU), safe(x.Title), n(x.OnHand), n(x.Reserved), n(x.Available), r.Currency, decimal(x.Price), decimal(x.Value), n(x.Sold30), cover, state[x.State]})
	}
	return t
}

// Table of activity entries.
func AuditTable(items []AuditEntry) Table {
	t := Table{Header: []string{"registro", "fecha", "actor", "nombre", "accion", "objeto", "datos", "hash"}}
	for _, e := range items {
		t.Rows = append(t.Rows, []string{n(e.Seq), e.At.In(Zone).Format("2006-01-02 15:04:05"), safe(e.Actor), safe(e.ActorName), safe(e.Action), safe(e.Target), safe(string(e.Data)), e.Hash})
	}
	return t
}
