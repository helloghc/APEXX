package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/helloghc/apexx/internal/importer"
)

// cmdImport migrates a VTEX store into an Apexx tenant (read-only on VTEX).
// Credentials come only from VTEX_APP_KEY / VTEX_APP_TOKEN.
func cmdImport(ctx context.Context, log *slog.Logger, args []string) {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	tenant := fs.String("tenant", "", "Apexx store id to import into")
	account := fs.String("vtex-account", "", "VTEX account name")
	limit := fs.Int("limit", 0, "import only the first N products (0 = whole catalog)")
	workers := fs.Int("workers", 8, "concurrent products")
	rps := fs.Float64("rps", 10, "maximum VTEX requests per second")
	dry := fs.Bool("dry-run", false, "fetch and map without writing")
	chmap := fs.String("channel-map", "1=mx:MXN", "VTEX trade policy to Apexx channel and currency, e.g. 1=mx:MXN,2=id:IDR")
	_ = fs.Parse(args)

	key, token := os.Getenv("VTEX_APP_KEY"), os.Getenv("VTEX_APP_TOKEN")
	if *tenant == "" || *account == "" || key == "" || token == "" {
		fmt.Fprintln(os.Stderr, "uso: VTEX_APP_KEY=… VTEX_APP_TOKEN=… apexx import -tenant T -vtex-account A [-limit N] [-dry-run]")
		os.Exit(2)
	}
	channels, err := importer.ParseChannelMap(*chmap)
	must(log, err, "channel map")
	db, _ := open(ctx, log)
	defer db.Close()

	c := importer.NewClient(*account, key, token)
	c.RPS = *rps
	im := &importer.Importer{VTEX: c, W: db, Log: log}
	rep, err := im.Run(ctx, importer.Options{Tenant: *tenant, Channels: channels, Limit: *limit, Workers: *workers, DryRun: *dry})
	b, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(b))
	must(log, err, "import")
}
