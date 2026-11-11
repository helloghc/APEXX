package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/helloghc/apexx/internal/reports"
)

// cmdAnalytics rebuilds a store's analytics from its orders and raw events,
// optionally after loading a synthetic sales history for demos and QA.
func cmdAnalytics(ctx context.Context, log *slog.Logger, args []string) {
	fs := flag.NewFlagSet("analytics", flag.ExitOnError)
	tenant := fs.String("tenant", "", "store id")
	demoOrders := fs.Int("demo-orders", 0, "create this many synthetic orders (demo/QA stores only)")
	demoDays := fs.Int("demo-days", 90, "spread the synthetic orders over the last N days")
	demoSessions := fs.Int("demo-sessions", 12, "synthetic storefront sessions per order")
	seed := fs.Int64("seed", time.Now().UnixNano()%1e6, "random seed of the synthetic history")
	_ = fs.Parse(args)
	if *tenant == "" {
		fmt.Fprintln(os.Stderr, "usa -tenant")
		os.Exit(2)
	}
	db, keys := open(ctx, log)
	defer db.Close()
	must(log, loadKeys(ctx, db, keys, *tenant), "tenant key")
	rep := &reports.Service{DB: db, Keys: keys, Log: log}
	start := time.Now()
	if *demoOrders > 0 {
		must(log, rep.SeedDemo(ctx, *tenant, "system:demo", reports.DemoOptions{Orders: *demoOrders, Days: *demoDays, Sessions: *demoSessions, Seed: *seed}), "demo")
		log.Info("demo history created", "orders", *demoOrders, "took", time.Since(start).String())
	}
	n, err := rep.Rebuild(ctx, *tenant)
	must(log, err, "rebuild")
	log.Info("analytics rebuilt", "tenant", *tenant, "orders", n, "took", time.Since(start).String())
}
