package main

import (
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"html"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/helloghc/apexx/internal/api"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/builder"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/customers"
	"github.com/helloghc/apexx/internal/logistics"
	"github.com/helloghc/apexx/internal/media"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/orders"
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
		if len(p.Media) > 0 {
			if m, ok := d.t.Taxo.Media(p.Media[0]); ok {
				c.Image = cardImageHTML(d.t.ID, m)
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

	// Realistic demo catalog: apparel & accessories whose stock photos are
	// brand-neutral (verified — no visible logos). Each carries its own free
	// Unsplash photo id. Cycled with a colour suffix for larger stores.
	demoCat := []struct{ title, brand, img string }{
		{"Chamarra de piel sintética", "Norte", "1551028719-00167b16eac5"},
		{"Sudadera con capucha gris", "Sierra", "1556821840-3a63f95609a7"},
		{"Chamarra rompevientos ligera", "Pacífico", "1591047139829-d91aecb6caea"},
		{"Playera de algodón orgánico", "Sierra", "1620799140408-edc6dcb6d633"},
		{"Suéter tejido oversize", "Norte", "1434389677669-e08b4cac3105"},
		{"Gorra clásica negra", "Pacífico", "1521369909029-2afed882baee"},
		{"Short de mezclilla", "Sierra", "1591195853828-11db59a44f6b"},
		{"Jeans slim de mezclilla", "Pacífico", "1506629082955-511b1aa562c8"},
		{"Mochila urbana 20L", "Norte", "1553062407-98eeb64c6a62"},
		{"Gorra de lona blanca", "Sierra", "1588850561407-ed78c282e89b"},
		{"Camisa de mezclilla", "Pacífico", "1596755094514-f87e34085b2c"},
		{"Playera cuello redondo", "Norte", "1618517351616-38fb9c5210c6"},
		{"Pashmina de lana", "Sierra", "1445205170230-053b83016050"},
		{"Playera blanca premium", "Pacífico", "1521572163474-6864f9cf17ab"},
		{"Paquete de básicos", "Norte", "1489987707025-afc232f7ea0f"},
		{"Playera estampada lisa", "Sierra", "1620799139507-2a76f79a2f4d"},
	}
	demoColors := []string{"", "Negro", "Azul marino", "Gris jaspe", "Verde olivo", "Vino"}
	imgStore, imgErr := media.NewLocal(env("APEXX_MEDIA_DIR", "data/media"))
	if imgErr != nil {
		log.Warn("media store; products will have no photos", "err", imgErr)
	}
	imgIDs := make([]string, len(demoCat))
	if imgStore != nil {
		cache := map[string]string{}
		for k := range demoCat {
			u := "https://images.unsplash.com/photo-" + demoCat[k].img + "?w=1000&h=1000&fit=crop&q=72"
			if id, ok := cache[u]; ok {
				imgIDs[k] = id
				continue
			}
			id, err := seedImage(ctx, imgStore, db, *tenant, u, demoCat[k].title)
			if err != nil {
				log.Warn("foto de producto", "err", err)
				continue
			}
			imgIDs[k] = id
			cache[u] = id
		}
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
		dp := demoCat[(i-1)%len(demoCat)]
		title := dp.title
		if c := demoColors[((i-1)/len(demoCat))%len(demoColors)]; c != "" {
			title = dp.title + " · " + c
		}
		var pm []string
		if id := imgIDs[(i-1)%len(demoCat)]; id != "" {
			pm = []string{id}
		}
		_, err := db.UpsertProduct(ctx, *tenant, "qa-setup", catalog.Product{
			ID: fmt.Sprintf("QA-%05d", i), Handle: fmt.Sprintf("qa-producto-%d", i), Title: title,
			Brand: dp.brand, Categories: []string{"deportes"}, Description: "Tejido ligero y transpirable, ideal para entrenar. Envío a todo México.", Variants: vs, Media: pm,
		}, 0)
		must(log, err, "product")
		// UpsertProduct does not write the media column (added in a later
		// migration), so link the cover photo to the product directly.
		if len(pm) > 0 {
			_, err := db.System.Exec(ctx, `UPDATE products SET media=$1 WHERE tenant_id=$2 AND id=$3`, pm, *tenant, fmt.Sprintf("QA-%05d", i))
			must(log, err, "product media")
		}
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
	qaRoles(ctx, log, a, &orders.Service{DB: db, Keys: keys, Projector: proj, Log: log}, *tenant, *domain, *pass, *n)
}

// qaRoles adds the roles that live outside the team screen, so QA can test
// every portal from the first command: a sales rep who serves a B2B company,
// a marketplace seller with its own person, and a company buyer who signs in
// to the store with a code.
func qaRoles(ctx context.Context, log *slog.Logger, a *auth.Service, ord *orders.Service, tenant, domain, pass string, n int) {
	members, _ := a.Members(ctx, tenant)
	byEmail := map[string]auth.Member{}
	for _, m := range members {
		byEmail[m.Email] = m
	}
	repEmail := "representante@" + domain
	rep, ok := byEmail[repEmail]
	if !ok {
		var err error
		rep, err = a.CreateMember(ctx, tenant, "qa-setup", repEmail, "Representante de ventas", pass, auth.Permissions{"customers": "edit", "orders": "edit", "catalog": "read"}, false)
		must(log, err, "member "+repEmail)
	}
	sellerEmail := "vendedor@" + domain
	if _, ok := byEmail[sellerEmail]; !ok {
		sel, err := ord.SaveSeller(ctx, tenant, "qa-setup", orders.Seller{Name: "Vendedor QA", Email: sellerEmail, CommissionBps: 1500, Status: "active"})
		must(log, err, "seller")
		var ids []string // the first products, skipping the one in the 3x2 promotion
		for i := 1; i <= n && len(ids) < 5; i++ {
			if i != 3 {
				ids = append(ids, fmt.Sprintf("QA-%05d", i))
			}
		}
		if len(ids) > 0 {
			must(log, ord.SetSellerProducts(ctx, tenant, "qa-setup", sel.ID, ids), "seller products")
		}
		_, token, err := a.Invite(ctx, tenant, "qa-setup", sellerEmail, "Vendedor QA", auth.Permissions{}, sel.ID)
		must(log, err, "seller invite")
		_, _, err = a.AcceptInvitation(ctx, token, "Vendedor QA", pass)
		must(log, err, "seller person")
	}
	cos, _ := ord.Companies(ctx, tenant)
	found := false
	for _, c := range cos {
		found = found || c.Name == "Ferretería QA"
	}
	if !found {
		_, err := ord.SaveCompany(ctx, tenant, "qa-setup", orders.Company{Name: "Ferretería QA", Terms: "net30", CreditLimit: 5000000, Status: "active", SalesRep: rep.ID, RepAccess: true},
			[]string{"compras@" + domain})
		must(log, err, "company")
	}
	fmt.Printf("\nRoles fuera del equipo:\n")
	fmt.Printf("  %-34s %s (entra al panel; atiende a Ferretería QA)\n", "Representante de ventas", repEmail)
	fmt.Printf("  %-34s %s (portal de vendedor, 5 productos)\n", "Vendedor del marketplace", sellerEmail)
	fmt.Printf("  %-34s compras@%s (entra a la tienda con código; crédito a 30 días, $50,000)\n", "Comprador B2B", domain)
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

// cmdDemoUser creates the restricted "visitor" account used by the public demo
// panel. The visitor can play with catalog, pricing, promotions and the store
// designer, but cannot touch team, settings or payments — and is confined to
// the demo tenant by isolation. Idempotent: if it exists, it is left as is.
func cmdDemoUser(ctx context.Context, log *slog.Logger, args []string) {
	fs := flag.NewFlagSet("demo-user", flag.ExitOnError)
	tenant := fs.String("tenant", "demo", "demo tenant id")
	email := fs.String("email", "visitante@demo.shulada.test", "demo visitor email")
	pass := fs.String("password", "", "password for the demo visitor (12+ chars)")
	_ = fs.Parse(args)
	if len(*pass) < 12 {
		fmt.Fprintln(os.Stderr, "usa -password con al menos 12 caracteres")
		os.Exit(2)
	}
	db, keys := open(ctx, log)
	defer db.Close()
	must(log, loadKeys(ctx, db, keys, *tenant), "tenant key")

	a := &auth.Service{DB: db}
	email2 := strings.ToLower(strings.TrimSpace(*email))
	members, _ := a.Members(ctx, *tenant)
	for _, m := range members {
		if strings.ToLower(m.Email) == email2 {
			fmt.Printf("Visitante de demo ya existe: %s (tenant %s)\n", email2, *tenant)
			return
		}
	}
	// Full access so prospects can try every function in the panel. This is safe
	// because the demo lives in its own isolated database, resets on a schedule
	// and runs payments in sandbox: the worst case is a disposable mess. We stop
	// short of "owner" so the store itself can't be deleted or transferred.
	perms := auth.All(auth.Admin)
	if _, err := a.CreateMember(ctx, *tenant, "demo-setup", email2, "Visitante (demo)", *pass, perms, false); err != nil {
		must(log, err, "create demo user")
	}
	fmt.Printf("Visitante de demo creado: %s (tenant %s) — rol restringido\n", email2, *tenant)
}

// seedImage downloads one image, processes it into renditions, stores them in
// the media library (so the platform serves and caches them — no hotlinking)
// and returns the media id. Identical bytes are de-duplicated by SHA.
func seedImage(ctx context.Context, ms media.Storage, db *store.DB, tenant, url, alt string) (string, error) {
	cl := &http.Client{Timeout: 20 * time.Second}
	resp, err := cl.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("imagen %s: http %d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	if dup, found, err := db.MediaBySHA(ctx, tenant, sum[:]); err == nil && found {
		return dup.ID, nil
	}
	proc, err := media.Process(data)
	if err != nil {
		return "", err
	}
	id := store.NewID("m_")
	if err := ms.Put(ctx, tenant, id+"/original."+proc.Ext, data, proc.ContentType); err != nil {
		return "", err
	}
	m := catalog.Media{ID: id, Filename: "producto." + proc.Ext, ContentType: proc.ContentType,
		Bytes: int64(len(data)), Width: proc.Width, Height: proc.Height, Alt: alt, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	for _, rd := range proc.Renditions {
		key := fmt.Sprintf("%s/%d.%s", id, rd.Width, rd.Ext)
		if err := ms.Put(ctx, tenant, key, rd.Data, rd.ContentType); err != nil {
			return "", err
		}
		m.Variants = append(m.Variants, catalog.MediaVariant{Width: rd.Width, Height: rd.Height, Format: rd.Format, Key: key, Bytes: int64(len(rd.Data))})
	}
	if err := db.InsertMedia(ctx, tenant, "qa-setup", m, sum[:]); err != nil {
		return "", err
	}
	return id, nil
}

// cardImageHTML builds the storefront card image markup for a home section,
// pointing at the files the platform serves at /media/<tenant>/<key>.
func cardImageHTML(tenant string, m catalog.Media) template.HTML {
	var src string
	w, h := m.Width, m.Height
	var parts []string
	for _, v := range m.Variants {
		if v.Format == "webp" {
			continue
		}
		u := "/media/" + tenant + "/" + v.Key
		parts = append(parts, fmt.Sprintf("%s %dw", u, v.Width))
		src, w, h = u, v.Width, v.Height
	}
	if src == "" {
		return ""
	}
	return template.HTML(fmt.Sprintf(
		`<img class="pcard-media ph" src="%s" srcset="%s" sizes="(max-width:700px) 50vw, 240px" width="%d" height="%d" alt="%s" loading="lazy" decoding="async">`,
		html.EscapeString(src), html.EscapeString(strings.Join(parts, ", ")), w, h, html.EscapeString(m.Alt)))
}
