// Package metrics collects the self-observability data the dashboard shows.
//
// Everything here comes from the standard library. There is no external
// telemetry, no time series database, and no sampling: a ring buffer of recent
// request latencies is enough to answer "is this fast?" honestly.
package metrics

import (
	"net/http"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const ringSize = 1024

// Collector accumulates request timings and process facts.
type Collector struct {
	mu       sync.Mutex
	ring     [ringSize]float64 // milliseconds
	n        int               // total observations
	requests uint64
	statuses map[int]uint64
	routes   map[string]*routeStat

	started time.Time
}

type routeStat struct {
	Count uint64
	Total float64
}

// New returns a Collector started at the current instant.
func New() *Collector {
	return &Collector{
		statuses: map[int]uint64{},
		routes:   map[string]*routeStat{},
		started:  time.Now(),
	}
}

// Observe records one completed request.
func (c *Collector) Observe(route string, status int, d time.Duration) {
	ms := float64(d) / float64(time.Millisecond)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ring[c.n%ringSize] = ms
	c.n++
	c.requests++
	c.statuses[status]++
	rs := c.routes[route]
	if rs == nil {
		rs = &routeStat{}
		c.routes[route] = rs
	}
	rs.Count++
	rs.Total += ms
}

// Snapshot is the serialisable view handed to the dashboard.
type Snapshot struct {
	Backend    string             `json:"backend"`
	GoVersion  string             `json:"goVersion"`
	Deps       string             `json:"deps"`
	OS         string             `json:"os"`
	Arch       string             `json:"arch"`
	PID        int                `json:"pid"`
	UptimeSec  float64            `json:"uptimeSec"`
	Requests   uint64             `json:"requests"`
	Statuses   map[int]uint64     `json:"statuses"`
	P50        float64            `json:"p50"`
	P95        float64            `json:"p95"`
	P99        float64            `json:"p99"`
	Mean       float64            `json:"mean"`
	Max        float64            `json:"max"`
	Routes     map[string]float64 `json:"routes"`
	Goroutines int                `json:"goroutines"`
	HeapMB     float64            `json:"heapMb"`
	SysMB      float64            `json:"sysMb"`
	GCCount    uint32             `json:"gcCount"`
	NumCPU     int                `json:"numCpu"`
}

// Snapshot computes current metrics.
func (c *Collector) Snapshot() Snapshot {
	c.mu.Lock()
	count := c.n
	if count > ringSize {
		count = ringSize
	}
	lat := make([]float64, count)
	copy(lat, c.ring[:count])
	statuses := make(map[int]uint64, len(c.statuses))
	for k, v := range c.statuses {
		statuses[k] = v
	}
	routes := make(map[string]float64, len(c.routes))
	for k, v := range c.routes {
		if v.Count > 0 {
			routes[k] = round2(v.Total / float64(v.Count))
		}
	}
	requests := c.requests
	started := c.started
	c.mu.Unlock()

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	s := Snapshot{
		Backend:    "Go",
		GoVersion:  runtime.Version(),
		Deps:       "standard library only",
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		UptimeSec:  round2(time.Since(started).Seconds()),
		Requests:   requests,
		Statuses:   statuses,
		Routes:     routes,
		Goroutines: runtime.NumGoroutine(),
		HeapMB:     round2(float64(mem.HeapAlloc) / (1 << 20)),
		SysMB:      round2(float64(mem.Sys) / (1 << 20)),
		GCCount:    mem.NumGC,
		NumCPU:     runtime.NumCPU(),
	}

	if len(lat) > 0 {
		sorted := append([]float64(nil), lat...)
		sort.Float64s(sorted)
		var sum float64
		for _, v := range sorted {
			sum += v
		}
		s.Mean = round2(sum / float64(len(sorted)))
		s.P50 = round2(pct(sorted, 0.50))
		s.P95 = round2(pct(sorted, 0.95))
		s.P99 = round2(pct(sorted, 0.99))
		s.Max = round2(sorted[len(sorted)-1])
	}
	return s
}

func pct(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(p * float64(len(sorted)-1))
	return sorted[i]
}

func round2(f float64) float64 {
	return float64(int64(f*100+0.5)) / 100
}

// statusWriter captures the status code for Observe.
type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status = code
		w.wrote = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.status = http.StatusOK
		w.wrote = true
	}
	return w.ResponseWriter.Write(b)
}

// Flush forwards to the underlying writer so SSE keeps streaming through the
// middleware. Without this, wrapping the writer would silently buffer events.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Middleware times every request and records it under the matched route.
func (c *Collector) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)

		// SSE connections are long-lived; timing them would swamp the ring
		// buffer with values measuring how long the browser stayed connected.
		if r.URL.Path == "/api/events" {
			return
		}
		route := r.Method + " " + routeLabel(r.URL.Path)
		c.Observe(route, sw.status, time.Since(start))
	})
}

// routeLabel collapses variable path segments so the route table stays small.
// Without this, every document ID becomes its own row and the table measures
// nothing useful.
func routeLabel(p string) string {
	switch {
	case p == "/":
		return "/"
	case strings.HasPrefix(p, "/d/"):
		return "/d/*"
	case strings.HasPrefix(p, "/api/docs/"):
		// Keep the sub-resource visible: /raw and /backlinks have very
		// different cost profiles from a plain document fetch.
		rest := strings.TrimPrefix(p, "/api/docs/")
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			return "/api/docs/*" + rest[i:]
		}
		return "/api/docs/*"
	case strings.HasPrefix(p, "/assets/"):
		return "/assets/*"
	default:
		return p
	}
}
