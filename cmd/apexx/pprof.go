package main

import (
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"time"
)

// startProfiler serves Go's profiler when APEXX_PPROF names a loopback
// address (for example 127.0.0.1:6060). It is off by default, never shares
// the public listener, and refuses any address that is not loopback, so it
// can only be reached from the machine itself.
func startProfiler(log *slog.Logger) {
	addr := os.Getenv("APEXX_PPROF")
	if addr == "" {
		return
	}
	host, _, err := net.SplitHostPort(addr)
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
		log.Warn("APEXX_PPROF ignored: use a loopback address such as 127.0.0.1:6060", "addr", addr)
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Info("profiler listening (loopback only)", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Warn("profiler stopped", "err", err)
		}
	}()
}
