// Command apexx runs the Apexx platform.
//
//	apexx migrate                 apply migrations and create DB roles (ADMIN_DATABASE_URL)
//	apexx tenant -id S -name N -owner-email E -owner-password P
//	                              create a store with its owner, keys and default channel
//	apexx seed -tenant S -n 2000  load demo catalog, prices, stock and promotions
//	apexx import -tenant S -vtex-account A [-limit N] [-dry-run]
//	                              import catalog, prices and stock from VTEX (read-only on VTEX)
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
	"syscall"
	"time"

	"github.com/helloghc/apexx/internal/api"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/builder"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/crypto"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/orders"
	"github.com/helloghc/apexx/internal/payments"
	"github.com/helloghc/apexx/internal/pricing"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/resilience"
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
		fmt.Fprintln(os.Stderr, "uso: apexx migrate | tenant | seed | import | serve")
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
		PlatformFeeBps: fee, Breakers: &resilience.Breakers{Threshold: 5, Cooldown: 30 * time.Second},
	}
	if env("APEXX_PAYMENTS", "stripe") == "sandbox" {
		pa.Sandbox = payments.NewSandbox()
		log.Warn("payments running in SANDBOX mode: no real charges")
	}
	ord := &orders.Service{DB: db, Keys: keys, Projector: proj, Accounts: pa.Resolve}

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

	srv := &api.Server{
		DB: db, Proj: proj, Auth: &auth.Service{DB: db}, Orders: ord, Keys: keys, Library: lib, Log: log,
		Admin: webui.Handler(), Payments: pa, Webhooks: wh, Bulkhead: resilience.NewBulkhead(256),
		SecureCookies: env("APEXX_SECURE_COOKIES", "true") == "true",
		TrustProxy:    env("APEXX_TRUST_PROXY", "false") == "true",
		MetricsToken:  os.Getenv("APEXX_METRICS_TOKEN"),
	}
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
