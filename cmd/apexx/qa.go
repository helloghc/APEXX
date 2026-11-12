package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/helloghc/apexx/internal/api"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/builder"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/customers"
	"github.com/helloghc/apexx/internal/logistics"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/pricing"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/seed"
	"github.com/helloghc/apexx/internal/sitecfg"
	"github.com/helloghc/apexx/internal/store"
)

// qaData feeds builder sections from the loaded projection.
type qaData struct {
	t *projection.Tenant
}

func (d qaData) Products(limit int) []builder.Card {
	ps, _ := d.t.Catalog.Search("", "", limit)
	out := make([]builder.Card, 0, len(ps))
	price := func(m money.Money) string {
		w, c := m.Amount/100, m.Amount%100
		s := strconv.FormatInt(w, 10)
		for i := len(s) - 3; i > 0; i -= 3 {
			s = s[:i] + "," + s[i:]
		}
		return fmt.Sprintf("$%s.%02d MXN", s, c)
	}
	for _, p := range ps {
		c := builder.Card{Title: p.Title, URL: "p/" + p.Handle, Brand: p.Brand, Tone: builder.ToneOf(p.Title), SoldOut: true}
		if r := []rune(p.Title); len(r) > 0 {
			c.Initial = string(r[0])
		}
		for _, v := range p.Variants {
			if d.t.Inventory.Available(v.SKU) > 0 && c.SoldOut {
				c.SoldOut, c.SKU = false, v.SKU
			}
		}
		if len(p.Variants) > 0 {
			if q, ok := d.t.Pricing.Resolve("mx", p.Variants[0].SKU, 1); ok {
				c.Price = price(q.Unit)
				if q.CompareAt != nil && q.CompareAt.Amount > q.Unit.Amount {
					c.CompareAt = price(*q.CompareAt)
				}
			}
		}
		out = append(out, c)
	}
	return out
}

// cmdQASetup prepares a complete store for manual QA: owner, one member per
// role, catalog with prices and stock, promotions and a published home page.
func cmdQASetup(ctx context.Context, log *slog.Logger, args []string) {
	fs := flag.NewFlagSet("qa-setup", flag.ExitOnError)
	tenant := fs.String("tenant", "qa", "store id")
	domain := fs.String("email-domain", "qa.apexx.test", "domain for the test accounts")
	pass := fs.String("password", "", "password for every test account (12+ chars)")
	n := fs.Int("n", 300, "products")
	theme := fs.String("theme", builder.DefaultThemeID, "theme of the main site: lienzo, voltio, mercadito or balsamo")
	_ = fs.Parse(args)
	if len(*pass) < 12 {
		fmt.Fprintln(os.Stderr, "usa -password con al menos 12 caracteres")
		os.Exit(2)
	}
	db, keys := open(ctx, log)
	defer db.Close()

	if _, err := db.WrappedKey(ctx, *tenant); err == store.ErrNotFound {
		w, err := keys.NewWrappedKey(*tenant)
		must(log, err, "key")
		must(log, db.CreateTenant(ctx, *tenant, "Tienda QA", w), "tenant")
	}
	must(log, loadKeys(ctx, db, keys, *tenant), "tenant key")
	must(log, db.SetChannel(ctx, *tenant, "qa-setup", "mx", seed.Channels()["mx"]), "channel")

	must(log, qaSettings(ctx, db, *tenant), "settings")

	a := &auth.Service{DB: db}
	type acct struct {
		who, email string
		perms      auth.Permissions
		owner      bool
	}
	accounts := []acct{
		{"Dueño (todo)", "dueno", nil, true},
		{"Editor de catálogo", "catalogo", auth.Permissions{"catalog": "edit", "pricing": "edit", "inventory": "edit", "promotions": "read", "orders": "read"}, false},
		{"Atención a pedidos", "pedidos", auth.Permissions{"orders": "edit", "catalog": "read", "inventory": "read"}, false},
		{"Diseñador (builder sin publicar)", "disenador", auth.Permissions{"builder": "edit", "catalog": "read"}, false},
		{"Publicador del sitio", "publicador", auth.Permissions{"builder": "admin", "catalog": "read"}, false},
		{"Lector", "lector", auth.All(auth.Read), false},
	}
	existing, _ := a.Members(ctx, *tenant)
	have := map[string]bool{}
	for _, m := range existing {
		have[m.Email] = true
	}
	for _, x := range accounts {
		email := x.email + "@" + *domain
		if have[email] {
			continue
		}
		_, err := a.CreateMember(ctx, *tenant, "qa-setup", email, x.who, *pass, x.perms, x.owner)
		must(log, err, "member "+email)
	}

	for i := 1; i <= *n; i++ {
		var vs []catalog.Variant
		for j := 1; j <= 2; j++ {
			sku := fmt.Sprintf("QA-%05d-%d", i, j)
			vs = append(vs, catalog.Variant{SKU: sku, Title: fmt.Sprintf("Talla %d", 24+j), WeightGrams: 400 + int64(i%7)*100, Options: map[string]string{"talla": strconv.Itoa(24 + j)}})
			stock := int64(10 + i%40)
			if i%25 == 0 {
				stock = 0 // some sold-out products to test that path
			}
			must(log, db.SetStock(ctx, *tenant, "qa-setup", sku, "cdmx-1", stock), "stock")
			base := int64(19900 + (i%200)*1000)
			cmp := money.New(base+base/5, "MXN")
			must(log, db.SetPrices(ctx, *tenant, "qa-setup", "mx", sku, []pricing.Tier{
				{MinQty: 1, Unit: money.New(base, "MXN"), CompareAt: &cmp}, {MinQty: 10, Unit: money.New(base*95/100, "MXN")}}), "prices")
		}
		brand := []string{"Norte", "Sierra", "Pacífico", "Converse"}[i%4]
		kind := []string{"Tenis", "Playera", "Sudadera", "Calcetas", "Gorra"}[i%5]
		_, err := db.UpsertProduct(ctx, *tenant, "qa-setup", catalog.Product{
			ID: fmt.Sprintf("QA-%05d", i), Handle: fmt.Sprintf("qa-producto-%d", i), Title: fmt.Sprintf("%s %s %d", kind, brand, i),
			Brand: brand, Categories: []string{"deportes"}, Description: "Producto de prueba para QA.", Variants: vs,
		}, 0)
		must(log, err, "product")
	}
	for _, p := range []store.PromotionRow{
		{ID: "qa-10-3000", Kind: "percent_over_threshold", Active: true, Rule: []byte(`{"name":"10% en compras desde $3,000","channels":["mx"],"bps":1000,"min":{"amount":300000,"currency":"MXN"}}`)},
		{ID: "qa-3x2", Kind: "buy_x_pay_y", Active: true, Rule: []byte(`{"name":"3x2 en calcetas","sku":"QA-00003-1","x":3,"y":2}`)},
	} {
		must(log, db.SetPromotion(ctx, *tenant, "qa-setup", p), "promotion")
	}

	proj := projection.New(db, os.Getenv("SYSTEM_DATABASE_URL"), log)
	must(log, proj.Load(ctx), "projection")
	t, _ := proj.Tenant(*tenant)
	lib, err := builder.NewLibrary(builder.DefaultSections()...)
	must(log, err, "sections")
	if _, ok := builder.Themes[*theme]; !ok {
		fmt.Fprintln(os.Stderr, "tema desconocido:", *theme)
		os.Exit(2)
	}
	// "main" uses the chosen theme; one extra site per theme lets QA browse all four.
	sites := []struct{ id, theme string }{{"main", *theme}}
	for _, th := range builder.ThemeList() {
		sites = append(sites, struct{ id, theme string }{th.ID, th.ID})
	}
	for _, sd := range sites {
		per := store.SitePersister{DB: db, Tenant: *tenant, Site: sd.id}
		drafts, ver, revs, rels, found, err := per.LoadSite()
		must(log, err, "load site")
		if !found {
			must(log, per.Ensure(sd.id), "site")
		}
		site := builder.NewSite(sd.id, lib, qaData{t: t})
		site.Restore(drafts, ver, revs, rels)
		site.SetPersister(per)
		if d, _, err := site.Draft("index"); err != nil || d.Theme == nil || d.Theme.ID != sd.theme {
			_, _, err := site.ApplyStarter("index", builder.Starter(sd.theme), "qa-setup")
			must(log, err, "starter "+sd.id)
		}
		_, _, err = site.Publish("qa-setup", "Sitio de QA ("+sd.theme+")")
		must(log, err, "publish "+sd.id)
	}

	sf, err := db.CreateAPIKey(ctx, *tenant, "storefront-qa", "storefront")
	must(log, err, "storefront key")
	fmt.Printf("\nTienda de QA lista: %q (%d productos, 2 promociones, sitio publicado)\n\n", *tenant, *n)
	fmt.Printf("Tienda (comprador):  /s/%s/main/index  (tema %s)\n", *tenant, *theme)
	for _, th := range builder.ThemeList() {
		fmt.Printf("Tema %-10s         /s/%s/%s/index  (%s)\n", th.Name+":", *tenant, th.ID, th.Vertical)
	}
	fmt.Printf("Admin (plataforma):  /admin/\n\nCuentas (contraseña: la que pasaste en -password):\n")
	for _, x := range accounts {
		fmt.Printf("  %-34s %s@%s\n", x.who, x.email, *domain)
	}
	fmt.Printf("\nLlave storefront para pruebas de API: %s\n", sf)
}

// qaSettings gives the QA store a complete configuration: a warehouse with
// pickup, shipping zones and options, IVA regions, identity, policies and menus.
func qaSettings(ctx context.Context, db *store.DB, tenant string) error {
	const actor = "qa-setup"
	loc := logistics.Location{Name: "Bodega CDMX", Kind: "warehouse", Active: true, PickupEnabled: true,
		Address:            logistics.Address{Line1: "Av. Insurgentes Sur 1602", City: "Benito Juárez", State: "CMX", PostalCode: "03940", Country: "MX"},
		Phone:              "55 5555 0100",
		PickupInstructions: "Presenta tu número de pedido y una identificación.",
		Hours: map[string]logistics.Hours{"mon": {Open: "09:00", Close: "18:00"}, "tue": {Open: "09:00", Close: "18:00"}, "wed": {Open: "09:00", Close: "18:00"},
			"thu": {Open: "09:00", Close: "18:00"}, "fri": {Open: "09:00", Close: "18:00"}, "sat": {Open: "10:00", Close: "14:00"}, "sun": {Closed: true}}}
	if err := db.PutDoc(ctx, tenant, actor, "locations", "cdmx-1", loc); err != nil {
		return err
	}
	if _, err := api.InstallShippingDefaults(ctx, db, tenant, actor); err != nil {
		return err
	}
	if _, err := api.InstallTaxDefaults(ctx, db, tenant, actor); err != nil {
		return err
	}
	pickup := logistics.Option{Name: "Recoger en Bodega CDMX", Kind: logistics.KindPickup, LocationID: "cdmx-1", Active: true, Position: 9,
		Rate: logistics.Rate{Type: logistics.RateFlat, Brackets: []logistics.Bracket{}}, Estimate: logistics.EstimateRule{MinDays: 1, MaxDays: 1, Cutoff: "16:00", Weekend: "saturday"}}
	if err := db.PutDoc(ctx, tenant, actor, "options", "recoger-cdmx", pickup); err != nil {
		return err
	}
	if _, err := db.UpdateSettings(ctx, tenant, actor, func(s *sitecfg.Settings) error {
		if s.LegalName != "" {
			return nil
		}
		s.Name, s.LegalName, s.RFC = "Tienda QA", "Comercializadora QA S.A. de C.V.", "CQA200101AB1"
		s.Email, s.Phone = "hola@qa.apexx.test", "55 5555 0100"
		s.Address = loc.Address
		s.Social = map[string]string{"instagram": "https://instagram.com/apexx", "facebook": "https://facebook.com/apexx"}
		return nil
	}); err != nil {
		return err
	}
	have, err := db.Policies(ctx, tenant)
	if err != nil {
		return err
	}
	if len(have) == 0 {
		for _, p := range []sitecfg.Policy{
			{Slug: "devoluciones", Title: "Cambios y devoluciones", Published: true, Body: "<p>Tienes <strong>30 días naturales</strong> a partir de la entrega para devolver tu compra.</p><h2>Cómo devolver</h2><ol><li>Escríbenos con tu número de pedido.</li><li>Te enviamos una guía prepagada.</li><li>Reembolsamos al mismo método de pago en 5 días hábiles.</li></ol>"},
			{Slug: "privacidad", Title: "Aviso de privacidad", Published: true, Body: "<p>Usamos tus datos personales solo para procesar y entregar tus pedidos, conforme a la LFPDPPP.</p><h2>Derechos ARCO</h2><p>Puedes acceder, rectificar, cancelar u oponerte al uso de tus datos escribiendo a nuestro correo de contacto.</p>"},
			{Slug: "terminos", Title: "Términos y condiciones", Published: true, Body: "<p>Al comprar en esta tienda aceptas estos términos.</p><h2>Precios</h2><p>Todos los precios están en pesos mexicanos e incluyen IVA.</p>"},
		} {
			if errs := p.Validate(); len(errs) > 0 {
				return fmt.Errorf("policy %s: %s", p.Slug, errs[0].Message)
			}
			if _, err := db.PutPolicy(ctx, tenant, actor, p); err != nil {
				return err
			}
		}
	}
	menus, err := db.Menus(ctx, tenant)
	if err != nil || len(menus) > 0 {
		return err
	}
	header := sitecfg.Menu{ID: "header", Name: "Menú principal", Items: []sitecfg.MenuItem{
		{Label: "Novedades", Type: "catalog"},
		{Label: "Tenis", Type: "category", Ref: "tenis"},
		{Label: "Ropa", Type: "url", URL: "/c?q=playera", Children: []sitecfg.MenuItem{{Label: "Playeras", Type: "url", URL: "/c?q=playera"}, {Label: "Sudaderas", Type: "url", URL: "/c?q=sudadera"}}},
		{Label: "Accesorios", Type: "url", URL: "/c?q=gorra"},
	}}
	footer := sitecfg.Menu{ID: "footer", Name: "Ayuda", Items: []sitecfg.MenuItem{
		{Label: "Legal", Type: "policy", Ref: "terminos", Children: []sitecfg.MenuItem{
			{Label: "Cambios y devoluciones", Type: "policy", Ref: "devoluciones"},
			{Label: "Aviso de privacidad", Type: "policy", Ref: "privacidad"},
			{Label: "Términos y condiciones", Type: "policy", Ref: "terminos"}}},
	}}
	for _, m := range []sitecfg.Menu{header, footer} {
		if errs := m.Validate(); len(errs) > 0 {
			return fmt.Errorf("menu %s: %s", m.ID, errs[0].Message)
		}
		if err := db.PutDoc(ctx, tenant, actor, "menus", m.ID, map[string]any{"name": m.Name, "items": m.Items}); err != nil {
			return err
		}
	}
	return nil
}

// cmdQAShopper creates (or reuses) a verified store account with a password,
// so a QA walk can sign in as a shopper without reading a code from email.
func cmdQAShopper(ctx context.Context, log *slog.Logger, args []string) {
	fs := flag.NewFlagSet("qa-shopper", flag.ExitOnError)
	tenant := fs.String("tenant", "qa", "store id")
	email := fs.String("email", "", "shopper email")
	name := fs.String("name", "", "shopper name")
	pass := fs.String("password", "", "password (12+ chars)")
	_ = fs.Parse(args)
	if *email == "" || len(*pass) < 12 {
		fmt.Fprintln(os.Stderr, "usa -email y -password con al menos 12 caracteres")
		os.Exit(2)
	}
	db, keys := open(ctx, log)
	defer db.Close()
	must(log, loadKeys(ctx, db, keys, *tenant), "tenant key")
	cust := &customers.Service{DB: db, Keys: keys}
	list, _, _, err := cust.List(ctx, *tenant, customers.ListFilter{Q: *email, Limit: 1}, true)
	must(log, err, "lookup")
	var id string
	if len(list) > 0 {
		id = list[0].ID
	} else {
		c, err := cust.Create(ctx, *tenant, "qa-setup", customers.Input{Email: email, Name: name})
		must(log, err, "create")
		id = c.ID
	}
	_, err = db.System.Exec(ctx, `UPDATE customers SET verified_at=COALESCE(verified_at, now()) WHERE tenant_id=$1 AND id=$2`, *tenant, id)
	must(log, err, "verify")
	must(log, cust.SetPassword(ctx, *tenant, id, *pass, ""), "password")
	log.Info("qa shopper ready", "tenant", *tenant, "customer", id)
}
