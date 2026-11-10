package api

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/helloghc/apexx/internal/resilience"
)

var buckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5}

type routeStats struct {
	count map[int]int64
	hist  []int64
	sum   float64
	total int64
}

type metrics struct {
	mu     sync.Mutex
	routes map[string]*routeStats
}

func newMetrics() *metrics { return &metrics{routes: map[string]*routeStats{}} }

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (s *statusWriter) WriteHeader(c int) {
	if s.code == 0 {
		s.code = c
	}
	s.ResponseWriter.WriteHeader(c)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.code == 0 {
		s.code = 200
	}
	return s.ResponseWriter.Write(b)
}

func (m *metrics) wrap(route string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		h.ServeHTTP(sw, r)
		d := time.Since(start).Seconds()
		m.mu.Lock()
		rs := m.routes[route]
		if rs == nil {
			rs = &routeStats{count: map[int]int64{}, hist: make([]int64, len(buckets))}
			m.routes[route] = rs
		}
		rs.count[sw.code]++
		rs.total++
		rs.sum += d
		for i, b := range buckets {
			if d <= b {
				rs.hist[i]++
			}
		}
		m.mu.Unlock()
	})
}

// serveMetrics writes Prometheus text format.
func (s *Server) serveMetrics(w http.ResponseWriter, _ *http.Request) {
	var b strings.Builder
	s.metrics.mu.Lock()
	names := make([]string, 0, len(s.metrics.routes))
	for n := range s.metrics.routes {
		names = append(names, n)
	}
	sort.Strings(names)
	b.WriteString("# TYPE apexx_http_requests_total counter\n")
	for _, n := range names {
		rs := s.metrics.routes[n]
		for code, c := range rs.count {
			fmt.Fprintf(&b, "apexx_http_requests_total{route=%q,code=\"%d\"} %d\n", n, code, c)
		}
	}
	b.WriteString("# TYPE apexx_http_request_seconds histogram\n")
	for _, n := range names {
		rs := s.metrics.routes[n]
		for i, le := range buckets {
			fmt.Fprintf(&b, "apexx_http_request_seconds_bucket{route=%q,le=\"%g\"} %d\n", n, le, rs.hist[i])
		}
		fmt.Fprintf(&b, "apexx_http_request_seconds_bucket{route=%q,le=\"+Inf\"} %d\n", n, rs.total)
		fmt.Fprintf(&b, "apexx_http_request_seconds_sum{route=%q} %f\n", n, rs.sum)
		fmt.Fprintf(&b, "apexx_http_request_seconds_count{route=%q} %d\n", n, rs.total)
	}
	s.metrics.mu.Unlock()
	st := s.Proj.Stats()
	fmt.Fprintf(&b, "# TYPE apexx_projection_events_total counter\napexx_projection_events_total %d\n", st.Applied)
	fmt.Fprintf(&b, "# TYPE apexx_projection_lag_ms gauge\napexx_projection_lag_ms{kind=\"last\"} %f\napexx_projection_lag_ms{kind=\"max\"} %f\napexx_projection_lag_ms{kind=\"avg\"} %f\n", st.LastLagMs, st.MaxLagMs, st.AvgLagMs)
	fmt.Fprintf(&b, "apexx_projection_errors_total %d\napexx_tenants_loaded %d\n", st.Errors, st.Tenants)
	fmt.Fprintf(&b, "apexx_panics_total %d\napexx_bulkhead_rejects_total %d\napexx_worker_restarts_total %d\napexx_breaker_open_total %d\n",
		resilience.Panics.Load(), resilience.BulkheadRejects.Load(), resilience.WorkerRestarts.Load(), resilience.BreakerOpenTotal.Load())
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = w.Write([]byte(b.String()))
}
