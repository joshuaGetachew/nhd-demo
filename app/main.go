// Command app is a small HTTP server that exposes Prometheus metrics.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests, by path and status code.",
		},
		[]string{"path", "code"},
	)
	requestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency in seconds, by path.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"path"},
	)
)

// statusRecorder captures the status code a handler writes.
type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

// instrument records request count and latency for a handler.
// path is passed in explicitly so labels stay low-cardinality.
func instrument(path string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		h(rec, r)
		requestDuration.WithLabelValues(path).Observe(time.Since(start).Seconds())
		requestsTotal.WithLabelValues(path, strconv.Itoa(rec.code)).Inc()
	}
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	host, _ := os.Hostname()
	fmt.Fprintf(w, "Hello from %s\n", host)
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "ok")
}

func newMux(reg *prometheus.Registry) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", instrument("/", handleRoot))
	mux.HandleFunc("/healthz", instrument("/healthz", handleHealthz))
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	return mux
}

func newRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		requestsTotal,
		requestDuration,
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
	)
	return reg
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port

	srv := &http.Server{
		Addr:              addr,
		Handler:           newMux(newRegistry()),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("listening on %s", addr)
	log.Fatal(srv.ListenAndServe())
}
