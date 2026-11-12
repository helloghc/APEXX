// Command apexx runs the Apexx platform.
//
//	apexx migrate                 apply migrations and create DB roles (ADMIN_DATABASE_URL)
//	apexx tenant -id S -name N -owner-email E -owner-password P
//	                              create a store with its owner, keys and default channel
//	apexx seed -tenant S -n 2000  load demo catalog, prices, stock and promotions
//	apexx import -tenant S -vtex-account A [-limit N] [-dry-run]
//	                              import catalog, prices and stock from VTEX (read-only on VTEX)
//	apexx analytics -tenant S [-rebuild] [-demo-orders N -demo-days D -demo-sessions K]
//	                              rebuild analytics from orders, or load a synthetic sales history (demos/QA)
//	apexx serve                   run the API, storefront, editor and background workers
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/helloghc/apexx/internal/api"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/builder"
	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/crypto"
	"github.com/helloghc/apexx/internal/customers"
	"github.com/helloghc/apexx/internal/media"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/notify"
	"github.com/helloghc/apexx/internal/orders"
	"github.com/helloghc/apexx/internal/payments"
	"github.com/helloghc/apexx/internal/pricing"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/reports"
	"github.com/helloghc/apexx/internal/resilience"
	"github.com/helloghc/apexx/internal/saleschannels"
	"github.com/helloghc/apexx/internal/seed"
	"github.com/helloghc/apexx/internal/store"
	"github.com/helloghc/apexx/internal/webhooks"
	"github.com/helloghc/apexx/internal/webui"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func must(log *slog.Logger, err error, msg string) {
	if err != nil {
		log.Error(msg, "err", err)
		os.Exit(1)
	}
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "uso: apexx migrate | tenant | seed | import | qa-setup | qa-shopper | rotate-master-key | serve")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch os.Args[1] {
	case "migrate":
		applied, err := store.Migrate(ctx, os.Getenv("ADMIN_DATABASE_URL"), os.Getenv("APP_DB_PASSWORD"), os.Getenv("SYSTEM_DB_PASSWORD"))
		must(log, err, "migrate")
		log.Info("migrated", "applied", applied)
	case "tenant":
		cmdTenant(ctx, log, os.Args[2:])
	case "seed":
		cmdSeed(ctx, log, os.Args[2:])
	case "serve":
		cmdServe(ctx, log)
	case "import":
		cmdImport(ctx, log, os.Args[2:])
	case "qa-setup":
		cmdQASetup(ctx, log, os.Args[2:])
	case "qa-shopper":
		cmdQAShopper(ctx, log, os.Args[2:])
	case "analytics":
		cmdAnalytics(ctx, log, os.Args[2:])
	case "rotate-master-key":
		cmdRotate(ctx, log)
	default:
		fmt.Fprintln(os.Stderr, "comando desconocido:", os.Args[1])
		os.Exit(2)
	}
}

func open(ctx context.Context, log *slog.Logger) (*store.DB, *crypto.Keyring) {
	db, err := store.Open(ctx, os.Getenv("DATABASE_URL"), os.Getenv("SYSTEM_DATABASE_URL"))
	must(log, err, "database")
	keys, err := crypto.NewKeyring(os.Getenv("APEXX_MASTER_KEY"))
	must(log, err, "master key")
	return db, keys
}

func loadKeys(ctx context.Context, db *store.DB, keys *crypto.Keyring, tenant string) error {
	w, err := db.WrappedKey(ctx, tenant)
	if err != nil {
		return err
	}
	return keys.Load(tenant, w)
}

func cmdTenant(ctx context.Context, log *slog.Logger, args []string) {
	fs := flag.NewFlagSet("tenant", flag.ExitOnError)
	id := fs.String("id", "", "store id (a-z, 0-9, -)")
	name := fs.String("name", "", "store name")
	email := fs.String("owner-email", "", "owner email")
	pass := fs.String("owner-password", "", "owner password (12+ chars)")
	_ = fs.Parse(args)
	db, keys := open(ctx, log)
	defer db.Close()
	wrapped, err := keys.NewWrappedKey(*id)
	must(log, err, "key")
	must(log, db.CreateTenant(ctx, *id, *name, wrapped), "create tenant")
	a := &auth.Service{DB: db}
	_, err = a.CreateMember(ctx, *id, "system", *email, "Dueño", *pass, nil, true)
	must(log, err, "owner")
	must(log, db.SetChannel(ctx, *id, "system", "mx", seed.Channels()["mx"]), "channel")
	sf, err := db.CreateAPIKey(ctx, *id, "storefront", "storefront")
	must(log, err, "storefront key")
	adm, err := db.CreateAPIKey(ctx, *id, "admin", "admin")
	must(log, err, "admin key")
	fmt.Printf("Tienda %q creada.\nDueño: %s\nLlave storefront (pública): %s\nLlave admin (secreta, guárdala): %s\n", *id, *email, sf, adm)
}

func cmdSeed(ctx context.Context, log *slog.Logger, args []string) {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	tenant := fs.String("tenant", "", "store id")
	n := fs.Int("n", 2000, "products")
	_ = fs.Parse(args)
	db, _ := open(ctx, log)
	defer db.Close()
	start := time.Now()
	for i := 1; i <= *n; i++ {
		var vs []catalog.Variant
		for j := 1; j <= 2; j++ {
			sku := fmt.Sprintf("SKU-%06d-%d", i, j)
			vs = append(vs, catalog.Variant{SKU: sku, Title: fmt.Sprintf("Talla %d", 24+j), WeightGrams: 400 + int64(i%7)*100,
				Options: map[string]string{"talla": strconv.Itoa(24 + j)}})
			must(log, db.SetStock(ctx, *tenant, "seed", sku, "cdmx-1", int64(10+i%40)), "stock")
			base := int64(19900 + (i%200)*1000)
			cmp := money.New(base+base/5, "MXN")
			must(log, db.SetPrices(ctx, *tenant, "seed", "mx", sku, []pricing.Tier{
				{MinQty: 1, Unit: money.New(base, "MXN"), CompareAt: &cmp},
				{MinQty: 10, Unit: money.New(base*95/100, "MXN")},
			}), "prices")
		}
		_, err := db.UpsertProduct(ctx, *tenant, "seed", catalog.Product{
			ID: fmt.Sprintf("P-%06d", i), Handle: fmt.Sprintf("producto-%d", i), Title: fmt.Sprintf("Producto %d", i),
			Brand: []string{"Norte", "Sierra", "Pacífico"}[i%3], Categories: []string{"deportes"}, Variants: vs,
		}, 0)
		must(log, err, "product")
	}
	for _, p := range []store.PromotionRow{
		{ID: "promo-10-3000", Kind: "percent_over_threshold", Active: true, Rule: []byte(`{"name":"10% en compras desde $3,000","channels":["mx"],"bps":1000,"min":{"amount":300000,"currency":"MXN"}}`)},
		{ID: "3x2-calcetas", Kind: "buy_x_pay_y", Active: true, Rule: []byte(`{"name":"3x2 en calcetas","sku":"SKU-000002-1","x":3,"y":2}`)},
	} {
		must(log, db.SetPromotion(ctx, *tenant, "seed", p), "promotion")
	}
	log.Info("seeded", "tenant", *tenant, "products", *n, "took", time.Since(start).String())
}

func cmdServe(ctx context.Context, log *slog.Logger) {
	db, keys := open(ctx, log)
	defer db.Close()
	tenants, err := db.Tenants(ctx)
	must(log, err, "tenants")
	for _, t := range tenants {
		must(log, loadKeys(ctx, db, keys, t), "tenant key "+t)
	}

	proj := projection.New(db, os.Getenv("SYSTEM_DATABASE_URL"), log)
	start := time.Now()
	must(log, proj.Load(ctx), "projection load")
	log.Info("projections loaded", "tenants", len(tenants), "took", time.Since(start).String())

	lib, err := builder.NewLibrary(builder.DefaultSections()...)
	must(log, err, "sections")
	api.DefaultHome = seed.HomeTemplate

	fee, _ := strconv.ParseInt(env("APEXX_PLATFORM_FEE_BPS", "0"), 10, 64)
	pa := &api.PaymentAccounts{
		DB: db, Keys: keys, PlatformKey: os.Getenv("STRIPE_SECRET_KEY"), PlatformWebhookSecret: os.Getenv("STRIPE_WEBHOOK_SECRET"),
		PlatformPublishableKey: os.Getenv("STRIPE_PUBLISHABLE_KEY"),
		PlatformFeeBps:         fee, Breakers: &resilience.Breakers{Threshold: 5, Cooldown: 30 * time.Second},
	}
	if env("APEXX_PAYMENTS", "stripe") == "sandbox" {
		pa.Sandbox = payments.NewSandbox()
		log.Warn("payments running in SANDBOX mode: no real charges")
	}
	publicURL := env("APEXX_PUBLIC_URL", "http://localhost:8080")
	mail := &notify.Service{DB: db, Keys: keys, AllowPrivate: env("APEXX_SMTP_ALLOW_PRIVATE", "false") == "true"}
	cust := &customers.Service{DB: db, Keys: keys, Notify: mail, PublicURL: publicURL,
		Simulate: func(tenant, channel string, items []customers.CartItem) ([]customers.CartLine, int64, string, bool) {
			t, ok := proj.Tenant(tenant)
			if !ok {
				return nil, 0, "", false
			}
			req := cart.Request{Channel: channel}
			for _, it := range items {
				req.Items = append(req.Items, cart.Item{SKU: it.SKU, Qty: it.Qty})
			}
			res, err := t.Simulate(req)
			if err != nil {
				return nil, 0, "", false
			}
			lines := make([]customers.CartLine, 0, len(res.Lines))
			for _, l := range res.Lines {
				lines = append(lines, customers.CartLine{SKU: l.SKU, Title: l.Title, Qty: l.Qty, Unit: l.Unit.Amount, Total: l.Total.Amount})
			}
			return lines, res.Totals.Items.Amount, string(res.Totals.Items.Currency), true
		}}
	ord := &orders.Service{DB: db, Keys: keys, Projector: proj, Accounts: pa.Resolve, Notify: mail, PublicURL: publicURL, Log: log}
	resilience.Every(ctx, log, "mailer", 2*time.Second, func(ctx context.Context) error {
		_, err := mail.Send(ctx)
		return err
	})
	resilience.Every(ctx, log, "abandoned-carts", time.Minute, func(ctx context.Context) error {
		_, err := cust.RemindDue(ctx)
		return err
	})
	resilience.Every(ctx, log, "refund-reconciler", time.Minute, func(ctx context.Context) error {
		_, err := ord.ReconcileRefunds(ctx, 2*time.Minute)
		return err
	})
	resilience.Every(ctx, log, "quote-expiry", time.Minute, func(ctx context.Context) error {
		_, err := ord.ExpireQuotes(ctx)
		return err
	})
	resilience.Every(ctx, log, "company-due-reminders", 15*time.Minute, func(ctx context.Context) error {
		_, err := ord.RemindCompanyDues(ctx)
		return err
	})
	resilience.Every(ctx, log, "company-autopay", 15*time.Minute, func(ctx context.Context) error {
		_, err := ord.RunAutopay(ctx)
		return err
	})
	resilience.Every(ctx, log, "purchase-request-expiry", time.Minute, func(ctx context.Context) error {
		_, err := ord.ExpirePurchaseRequests(ctx)
		return err
	})
	resilience.Every(ctx, log, "seller-payouts", 10*time.Minute, func(ctx context.Context) error {
		_, err := ord.RunPayouts(ctx)
		return err
	})
	resilience.Every(ctx, log, "shopper-purge", time.Hour, cust.Purge)

	// Background workers, each supervised and restarted on failure.
	resilience.Supervise(ctx, log, "projector", func(ctx context.Context) error { proj.Run(ctx); return nil })
	resilience.Every(ctx, log, "reservation-expiry", 30*time.Second, func(ctx context.Context) error {
		_, err := db.ExpireReservations(ctx)
		return err
	})
	resilience.Every(ctx, log, "payment-reconciler", time.Minute, func(ctx context.Context) error {
		_, err := ord.Reconcile(ctx, 2*time.Minute)
		return err
	})
	resilience.Every(ctx, log, "invoicer", 15*time.Second, func(ctx context.Context) error {
		_, err := ord.IssueInvoices(ctx)
		return err
	})
	resilience.Every(ctx, log, "session-purge", time.Hour, (&auth.Service{DB: db}).PurgeSessions)
	resilience.Every(ctx, log, "tenant-keys", 30*time.Second, func(ctx context.Context) error {
		ts, err := db.Tenants(ctx)
		if err != nil {
			return err
		}
		for _, t := range ts {
			if err := loadKeys(ctx, db, keys, t); err != nil {
				return err
			}
		}
		return nil
	})

	wh := &webhooks.Service{DB: db, Keys: keys, Proj: proj, AllowPrivate: env("APEXX_WEBHOOK_ALLOW_PRIVATE", "false") == "true"}
	resilience.Every(ctx, log, "webhook-dispatcher", time.Second, func(ctx context.Context) error {
		_, err := wh.Dispatch(ctx)
		return err
	})
	resilience.Every(ctx, log, "webhook-sender", time.Second, func(ctx context.Context) error {
		_, err := wh.Send(ctx)
		return err
	})
	// Keep 30 days of outbox for the event feed and replays; never prune past
	// what the projector has applied.
	resilience.Every(ctx, log, "outbox-prune", time.Hour, func(ctx context.Context) error {
		return db.PruneOutbox(ctx, proj.Stats().LastID, 30*24*time.Hour)
	})

	// Analytics: order facts and sales rollups follow every order change; raw
	// storefront events are rolled up twice a minute and purged after 90 days.
	rep := &reports.Service{DB: db, Keys: keys, Log: log}
	var ordersDirty atomic.Bool
	ordersDirty.Store(true)
	proj.On(store.TopicOrder, func(_, _ string) { ordersDirty.Store(true) })
	lastFullSync := time.Time{}
	resilience.Every(ctx, log, "analytics-orders", 2*time.Second, func(ctx context.Context) error {
		// Order events wake the sync at once; a sweep every 30 s covers any missed signal.
		if !ordersDirty.Swap(false) && time.Since(lastFullSync) < 30*time.Second {
			return nil
		}
		lastFullSync = time.Now()
		_, err := rep.SyncOrders(ctx)
		if err != nil {
			ordersDirty.Store(true)
		}
		return err
	})
	resilience.Every(ctx, log, "analytics-traffic", 30*time.Second, func(ctx context.Context) error {
		_, err := rep.RollupTraffic(ctx)
		return err
	})
	resilience.Every(ctx, log, "analytics-retention", 6*time.Hour, func(ctx context.Context) error {
		_, err := rep.Purge(ctx)
		return err
	})
	// Media library files. Local disk is right for one node or a shared
	// volume; see internal/media/gcs.go for the Cloud Storage back end.
	if kind := env("APEXX_MEDIA_STORAGE", "local"); kind != "local" {
		must(log, fmt.Errorf("APEXX_MEDIA_STORAGE=%s is not available in this build; use local", kind), "media storage")
	}
	mediaStore, err := media.NewLocal(env("APEXX_MEDIA_DIR", "data/media"))
	must(log, err, "media directory")
	// Drafts with a publication date go live on time.
	resilience.Every(ctx, log, "scheduled-publish", 20*time.Second, func(ctx context.Context) error {
		_, err := db.PublishDue(ctx)
		return err
	})

	srv := &api.Server{
		Reports: rep,
		DB:      db, Proj: proj, Auth: &auth.Service{DB: db, Keys: keys}, Orders: ord, Keys: keys, Library: lib, Log: log,
		Admin: webui.Handler(), Payments: pa, Webhooks: wh, Customers: cust, Notify: mail, Bulkhead: resilience.NewBulkhead(256),
		SecureCookies: env("APEXX_SECURE_COOKIES", "true") == "true",
		TrustProxy:    env("APEXX_TRUST_PROXY", "false") == "true",
		MetricsToken:  os.Getenv("APEXX_METRICS_TOKEN"),
		Media:         mediaStore,
	}
	mail.Phrases = srv.NotifyPhrases
	// Third-party warehouses get their paid orders, with retries.
	// Operators can point Mercado Libre and Amazon at a local stand-in (QA only).
	if h := os.Getenv("APEXX_SALESCHANNELS_TEST_HOST"); h != "" {
		srv.ChannelEndpoints = saleschannels.Endpoints{MeliAPI: h, MeliAuth: h, AmazonAPI: h, AmazonAuth: h, AmazonMarketplace: saleschannels.Default.AmazonMarketplace}
		log.Warn("sales channels point to a test host", "host", h)
	}
	resilience.Every(ctx, log, "warehouse-requests", 30*time.Second, func(ctx context.Context) error {
		_, err := srv.RunWarehouseRequests(ctx)
		return err
	})
	// Purchases reported to ad platforms from the server.
	resilience.Every(ctx, log, "ad-conversions", 15*time.Second, func(ctx context.Context) error {
		_, err := srv.RunConversions(ctx)
		return err
	})
	// Mercado Libre and Amazon: listings in step and orders imported.
	resilience.Every(ctx, log, "sales-channels", 20*time.Second, func(ctx context.Context) error {
		_, err := srv.RunSalesChannels(ctx)
		return err
	})
	// Email campaigns due to go out.
	resilience.Every(ctx, log, "campaigns", 30*time.Second, func(ctx context.Context) error {
		_, err := srv.RunCampaigns(ctx)
		return err
	})
	// Bulk operations: stock and price jobs in chunks.
	resilience.Every(ctx, log, "bulk-jobs", 2*time.Second, func(ctx context.Context) error {
		_, err := srv.RunBulkJobs(ctx)
		return err
	})
	// Automations: "when X happens to an order, do Y", with retries.
	resilience.Every(ctx, log, "automations", 5*time.Second, func(ctx context.Context) error {
		_, err := srv.RunAutomations(ctx)
		return err
	})
	// Carriers: parcels with a bought label are tracked until delivered.
	resilience.Every(ctx, log, "carrier-tracking", 15*time.Minute, func(ctx context.Context) error {
		_, err := srv.RunTracking(ctx)
		return err
	})
	// Searches are counted in memory and saved in batches.
	// Subscriptions: deliveries that are due are charged and ordered.
	resilience.Every(ctx, log, "subscriptions", time.Minute, func(ctx context.Context) error {
		_, err := ord.RenewSubscriptions(ctx)
		return err
	})
	resilience.Every(ctx, log, "search-terms", 15*time.Second, srv.FlushSearchTerms)
	// Recommendations and learning search: rebuilt from orders, visits and clicks.
	resilience.Every(ctx, log, "learning", 20*time.Second, func(ctx context.Context) error {
		_, err := srv.RunLearning(ctx)
		return err
	})
	resilience.Every(ctx, log, "catalog-sync", time.Minute, srv.SyncCatalogs)
	hs := &http.Server{
		Addr: env("APEXX_ADDR", ":8080"), Handler: srv.Routes(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second,
		MaxHeaderBytes: 1 << 16,
	}
	go func() {
		log.Info("listening", "addr", hs.Addr)
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = hs.Shutdown(sctx)
}

// cmdRotate re-wraps every tenant key from APEXX_MASTER_KEY to
// APEXX_NEW_MASTER_KEY in one transaction. Deploy the new key afterwards.
func cmdRotate(ctx context.Context, log *slog.Logger) {
	db, err := store.Open(ctx, os.Getenv("DATABASE_URL"), os.Getenv("SYSTEM_DATABASE_URL"))
	must(log, err, "database")
	defer db.Close()
	oldKR, err := crypto.NewKeyring(os.Getenv("APEXX_MASTER_KEY"))
	must(log, err, "old master key")
	newKR, err := crypto.NewKeyring(os.Getenv("APEXX_NEW_MASTER_KEY"))
	must(log, err, "new master key")
	n, err := db.RotateTenantKeys(ctx, func(tenant string, wrapped []byte) ([]byte, error) {
		return crypto.Rewrap(oldKR, newKR, tenant, wrapped)
	})
	must(log, err, "rotate")
	log.Info("master key rotated; now set APEXX_MASTER_KEY to the new value and restart", "tenants", n)
}
