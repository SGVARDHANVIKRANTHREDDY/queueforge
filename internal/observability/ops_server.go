package observability

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewOpsHandler returns the private operations endpoints shared by the API and worker processes:
//
//	/metrics  Prometheus metrics for THIS process
//	/healthz  liveness (process is up)
//	/readyz   readiness (ready() returns nil, typically a DB ping)
//
// It must be served on a private listener (QF_METRICS_ADDR, loopback by default), never on the
// public API port: metrics leak tenant-adjacent operational detail and are unauthenticated.
func NewOpsHandler(ready func(ctx context.Context) error) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if ready != nil {
			if err := ready(ctx); err != nil {
				http.Error(w, "not ready", http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

// NewOpsServer wraps NewOpsHandler in an http.Server with sane timeouts.
func NewOpsServer(addr string, ready func(ctx context.Context) error) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           NewOpsHandler(ready),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
	}
}
