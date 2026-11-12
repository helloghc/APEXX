package api

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/helloghc/apexx/internal/ai"
)

type askCase struct {
	product string // handle; "" asks the store
	q       string
	want    string // folded words the answer must contain; "" = must say it does not know
}

var askEval = []askCase{
	{"", "¿Cuánto tarda en llegar mi pedido?", "3 a 5 dias habiles"},
	{"", "¿El envío es gratis?", "gratis en compras desde"},
	{"", "¿Cuánto cuesta el envío?", "149"},
	{"", "¿Me mandan la guía de rastreo?", "guia de rastreo"},
	{"", "¿Cuántos días tengo para devolver un producto?", "30 dias"},
	{"", "¿Puedo devolver un traje de baño?", "trajes de bano no tienen devolucion"},
	{"", "¿Cuándo me regresan mi dinero?", "10 dias habiles"},
	{"", "¿Tienen meses sin intereses?", "meses sin intereses"},
	{"", "¿Puedo pagar en efectivo?", "efectivo"},
	{"", "¿Aceptan American Express?", "american express"},
	{"", "¿Cuánto dura la garantía?", "12 meses"},
	{"", "¿Cómo hago válida la garantía?", "garantias tienda mx"},
	{"", "¿Cómo lavo una camisa de lino?", "a mano o en ciclo delicado"},
	{"", "¿Puedo meter el lino a la secadora?", "no uses secadora"},
	{"licuadora", "¿De qué es el vaso?", "vidrio"},
	{"licuadora", "¿Puede triturar hielo?", "tritura hielo"},
	{"licuadora", "¿Cuántas velocidades tiene?", "cinco velocidades"},
	{"licuadora", "¿Se pueden lavar las cuchillas?", "desmontan para lavarlas"},
	{"licuadora", "¿Tiene garantía?", "12 meses"},
	{"camisa", "¿De qué material es?", "lino"},
	{"camisa", "¿Dónde está hecha?", "yucatan"},
	{"camisa", "¿Qué tallas tiene?", "ch m g"},
	{"camisa", "¿Cuánto cuesta?", "cuesta"},
	{"tienda", "¿Cuánto pesa?", "3 2 kg"},
	{"tienda", "¿Aguanta la lluvia?", "agua|impermeable"},
	{"tienda", "¿Es difícil de armar?", "5 minutos"},
	{"tienda", "¿Trae estacas?", "estacas"},
	// Without a source in the store: it must say it does not know.
	{"", "¿Tienen tienda física en Monterrey?", ""},
	{"", "¿Venden boletos para conciertos?", ""},
	{"licuadora", "¿Funciona con 220 volts?", ""},
	{"", "¿Cuál es el horario de atención telefónica?", ""},
	{"camisa", "¿Es resistente al fuego?", ""},
}

// askHeldOut was written after tuning and never used to tune: it measures
// how the answers generalize.
var askHeldOut = []askCase{
	{"", "¿En cuánto tiempo llega a Monterrey?", "5 a 8 dias habiles"},
	{"", "¿Cuál es el monto mínimo para envío gratis?", "999"},
	{"", "¿Puedo devolver ropa interior?", "ropa interior"},
	{"", "¿Me devuelven el dinero a mi tarjeta?", "mismo medio de pago"},
	{"", "¿Qué tarjetas aceptan?", "visa|mastercard"},
	{"", "¿La garantía cubre si se me cae?", "golpes"},
	{"", "¿Cómo plancho el lino?", "humedo"},
	{"licuadora", "¿Qué capacidad tiene el vaso?", "1 5 litros"},
	{"licuadora", "¿Las cuchillas son de acero?", "acero inoxidable"},
	{"tienda", "¿Cuántas personas caben?", "4 personas"},
	{"camisa", "¿Es de manga corta?", "manga larga"},
	{"", "¿Tienen programa de lealtad?", ""},
}

func (e *env) askSetup() {
	pol := func(slug, title, body string) {
		e.must(200, "PUT", "/v1/policies/"+slug, e.adminA, map[string]any{"title": title, "body": body, "published": true})
	}
	pol("envios", "Envíos", "<h2>Tiempos de entrega</h2><p>Enviamos a todo México. Los pedidos llegan en 3 a 5 días hábiles a la Ciudad de México y en 5 a 8 días hábiles al resto del país.</p><h2>Costo</h2><p>El envío es gratis en compras desde $999. En compras menores cuesta $149.</p><h2>Seguimiento</h2><p>Te mandamos la guía de rastreo por correo el mismo día en que sale tu paquete.</p>")
	pol("devoluciones", "Devoluciones", "<h2>Plazo</h2><p>Tienes 30 días naturales desde que recibes tu pedido para devolverlo.</p><h2>Condiciones</h2><p>El producto debe estar sin uso y con etiquetas. La ropa interior y los trajes de baño no tienen devolución por higiene.</p><h2>Reembolso</h2><p>Te regresamos el dinero al mismo medio de pago en un máximo de 10 días hábiles después de recibir la devolución.</p>")
	pol("garantia", "Garantía", "<p>Los aparatos eléctricos tienen garantía de 12 meses contra defectos de fábrica. La garantía no cubre golpes, humedad ni mal uso.</p><p>Para usarla escríbenos a garantias@tienda.mx con tu número de pedido y una foto del defecto.</p>")
	pol("pagos", "Formas de pago", "<p>Aceptamos tarjetas de crédito y débito Visa, Mastercard y American Express.</p><p>Puedes pagar a 3, 6 y 12 meses sin intereses con tarjetas de crédito participantes en compras desde $2,000.</p><p>También puedes pagar en efectivo en tiendas de conveniencia; tu pedido se confirma cuando recibimos el pago.</p>")
	e.must(200, "POST", "/v1/blog", e.adminA, map[string]any{"handle": "cuidado-lino", "title": "Cómo cuidar tus prendas de lino", "status": "published",
		"body": "<p>Lava el lino a mano o en ciclo delicado con agua fría.</p><p>No uses secadora: sécalo a la sombra para que no se encoja.</p><p>Plánchalo húmedo a temperatura media.</p>"})
	prod := func(h, title, desc string, variants ...string) {
		vs := []map[string]any{}
		for _, v := range variants {
			vs = append(vs, map[string]any{"sku": "SKU-" + h + "-" + v, "title": v})
		}
		e.must(201, "POST", "/v1/products", e.adminA, map[string]any{"handle": h, "title": title, "description": desc, "status": "active", "variants": vs})
	}
	prod("licuadora", "Licuadora 600 W", "Vaso de vidrio de 1.5 litros. Cinco velocidades y función pulso. Tritura hielo sin problema. Las cuchillas son de acero inoxidable y se desmontan para lavarlas.", "Única")
	prod("camisa", "Camisa de lino blanca", "Camisa de manga larga 100 % lino. Corte regular. Hecha en Yucatán.", "CH", "M", "G")
	prod("tienda", "Tienda de campaña para 4 personas", "Impermeable con costuras selladas. Se arma en 5 minutos. Pesa 3.2 kg. Incluye bolsa de transporte y estacas.", "Única")
	e.drain()
	_, rv := e.do("POST", "/s/store-a/api/reviews/tienda", "", map[string]any{"rating": 5, "title": "Excelente", "body": "La usé en una tormenta y no entró agua.", "name": "Luis", "email": "luis@example.com"})
	if id, _ := rv["id"].(string); id != "" {
		e.must(200, "POST", "/v1/reviews/"+id+"/moderate", e.adminA, map[string]any{"status": "approved"})
	}
	e.embed()
	if _, err := e.server.RunKnowledge(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	e.drain()
}

func (e *env) ask(product, q string) Answer {
	_, m := e.do("POST", "/s/store-a/api/ask", "", map[string]any{"q": q, "product": product})
	a := Answer{}
	a.Text, _ = m["text"].(string)
	a.Kind, _ = m["kind"].(string)
	for _, s := range m["sources"].([]any) {
		sm := s.(map[string]any)
		c := Citation{}
		c.Title, _ = sm["title"].(string)
		c.URL, _ = sm["url"].(string)
		c.Quote, _ = sm["quote"].(string)
		a.Sources = append(a.Sources, c)
	}
	return a
}

// TestAskEvaluation is the gate of IA-2 (and the "no sé" of IA-3): answers
// with their source, faithful to it, and "no sé" when the store does not say.
func TestAskEvaluation(t *testing.T) {
	e := setup(t)
	e.askSetup()
	if f := os.Getenv("APEXX_ASK_FLOOR"); f != "" {
		old := askFloor
		_, _ = fmt.Sscan(f, &askFloor)
		defer func() { askFloor = old }()
	}
	right, answerable, unknownOK, unanswerable, faithful, quoted := 0, 0, 0, 0, 0, 0
	for _, c := range askEval {
		a := e.ask(c.product, c.q)
		got := foldQ(a.Text)
		if c.want == "" {
			unanswerable++
			if a.Kind == "unknown" {
				unknownOK++
			} else {
				t.Logf("SHOULD NOT KNOW %-45q -> %s", c.q, a.Text)
			}
			continue
		}
		answerable++
		ok := false
		for _, w := range strings.Split(c.want, "|") {
			ok = ok || strings.Contains(got, w)
		}
		if ok {
			right++
		} else {
			t.Logf("WRONG %-45q (%s) -> [%s] %s", c.q, c.product, a.Kind, a.Text)
		}
		// Faithful: a quote is in its source's words; a fact comes from the system.
		if a.Kind == "quote" {
			quoted++
			if len(a.Sources) > 0 && a.Sources[0].Quote == a.Text && a.Sources[0].URL != "" {
				faithful++
			}
		}
	}
	acc := float64(right) / float64(answerable)
	unk := float64(unknownOK) / float64(unanswerable)
	fai := 1.0
	if quoted > 0 {
		fai = float64(faithful) / float64(quoted)
	}
	t.Logf("correctas %d/%d (%.0f%%) · «no sé» cuando no hay fuente %d/%d (%.0f%%) · fidelidad %.0f%%", right, answerable, acc*100, unknownOK, unanswerable, unk*100, fai*100)
	if os.Getenv("APEXX_ASK_STRICT") != "" || true {
		if fai < 0.95 {
			t.Errorf("faithfulness %.2f under 0.95", fai)
		}
		if acc < 0.85 {
			t.Errorf("accuracy %.2f under 0.85", acc)
		}
		if unk < 0.8 {
			t.Errorf("unknown rate %.2f under 0.8", unk)
		}
	}
	// Held-out set: reported, with a lower bar (it was never tuned on).
	hr, hn := 0, 0
	for _, c := range askHeldOut {
		a := e.ask(c.product, c.q)
		got := foldQ(a.Text)
		ok := false
		if c.want == "" {
			ok = a.Kind == "unknown"
		} else {
			for _, w := range strings.Split(c.want, "|") {
				ok = ok || strings.Contains(got, w)
			}
		}
		hn++
		if ok {
			hr++
		} else {
			t.Logf("HELD-OUT MISS %-42q (%s) -> [%s] %s", c.q, c.product, a.Kind, a.Text)
		}
	}
	t.Logf("conjunto apartado: %d/%d (%.0f%%)", hr, hn, 100*float64(hr)/float64(hn))
	if float64(hr)/float64(hn) < 0.7 {
		t.Errorf("held-out accuracy %d/%d under 70%%", hr, hn)
	}
	_ = ai.Words
}
