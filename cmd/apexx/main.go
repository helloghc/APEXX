// Command apexx runs the Apexx commerce API as a single binary
// (modular monolith: each module can later become its own service).
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/helloghc/apexx/internal/api"
	"github.com/helloghc/apexx/internal/builder"
	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/events"
	"github.com/helloghc/apexx/internal/inventory"
	"github.com/helloghc/apexx/internal/pricing"
	"github.com/helloghc/apexx/internal/seed"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	n := flag.Int("seed", 18500, "demo products to load")
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	bus := events.NewBus()
	cat := catalog.New(bus)
	inv := inventory.New(bus)
	pr := pricing.New()
	start := time.Now()
	seed.Load(*n, cat, inv, pr)
	log.Info("seeded", "products", cat.Count(), "took", time.Since(start).String())

	lib, err := builder.NewLibrary(builder.DefaultSections()...)
	if err != nil {
		log.Error("sections", "err", err)
		os.Exit(1)
	}
	demo := builder.NewSite("demo", lib, api.CatalogData{Catalog: cat, Pricing: pr, Channel: "mx"}, seed.HomeTemplate())
	if _, _, err := demo.Publish("seed", "initial"); err != nil {
		log.Error("publish", "err", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr: *addr,
		Handler: (&api.Server{
			Catalog: cat, Inventory: inv,
			Cart:    &cart.Service{Catalog: cat, Inventory: inv, Pricing: pr, Promos: seed.Promotions(), Channels: seed.Channels()},
			Library: lib, Sites: map[string]*builder.Site{"demo": demo},
		}).Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		log.Info("listening", "addr", *addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
}
