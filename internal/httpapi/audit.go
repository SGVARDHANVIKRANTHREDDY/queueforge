package httpapi

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/auth"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// auditMiddleware records every successful state-changing control-plane request (cancel, replay,
// schedule changes, ...) with the acting API key, the route, the target resource and the request id.
//
// Not audited: reads, and POST /v1/jobs (enqueue is the hot path and already leaves a durable job
// row; auditing it would double the write load for no new information).
func (a *API) auditMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			return
		}
		route := ""
		if rc := chi.RouteContext(r.Context()); rc != nil {
			route = rc.RoutePattern()
		}
		if r.Method == http.MethodPost && route == "/v1/jobs" {
			return
		}
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		if status >= 400 {
			return // only record actions that actually happened
		}
		p, ok := auth.GetPrincipal(r.Context())
		if !ok {
			return
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		tid := p.TenantID
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := a.auditRepo.Record(ctx, postgres.AuditEntry{
			TenantID: &tid, ActorType: "api_key", ActorID: p.APIKeyID.String(),
			Action: r.Method + " " + route, TargetID: chi.URLParam(r, "id"),
			StatusCode: status, RequestID: middleware.GetReqID(r.Context()), RemoteAddr: host,
		}); err != nil {
			slog.Error("failed to write audit log", "error", err, "route", route)
		}
	})
}
