package resilience

import (
	"sort"
	"sync"
	"time"
)

// WorkerStatus is the health of one periodic background worker on this node.
type WorkerStatus struct {
	Name        string    `json:"name"`
	IntervalSec float64   `json:"intervalSec"`
	Runs        int64     `json:"runs"`
	Failures    int64     `json:"failures"`
	LastRun     time.Time `json:"lastRun"`
	LastOK      time.Time `json:"lastOk"`
	LastMs      float64   `json:"lastMs"`
	LastError   string    `json:"lastError,omitempty"`
}

var (
	workersMu sync.Mutex
	workers   = map[string]*WorkerStatus{}
)

func recordRun(name string, interval time.Duration, start time.Time, err error) {
	workersMu.Lock()
	defer workersMu.Unlock()
	w := workers[name]
	if w == nil {
		w = &WorkerStatus{Name: name}
		workers[name] = w
	}
	w.IntervalSec = interval.Seconds()
	w.Runs++
	w.LastRun = start
	w.LastMs = float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		w.Failures++
		// Error text can carry row data; keep only a bounded prefix for operators.
		msg := err.Error()
		if len(msg) > 200 {
			msg = msg[:200]
		}
		w.LastError = msg
		return
	}
	w.LastOK = start
	w.LastError = ""
}

// Workers returns the status of every periodic worker, by name.
func Workers() []WorkerStatus {
	workersMu.Lock()
	defer workersMu.Unlock()
	out := make([]WorkerStatus, 0, len(workers))
	for _, w := range workers {
		out = append(out, *w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
