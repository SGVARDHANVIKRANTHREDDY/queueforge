package httpapi

import (
	"math"
	"net/http"
	"strconv"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/auth"
)

// clientRateLimitMiddleware rejects clients that exceed the per-address request budget before any
// authentication or database work happens (THREAT_MODEL T9). The client address follows the same
// trust rules as the failure limiter (QF_TRUST_FORWARDED_FOR).
func (a *API) clientRateLimitMiddleware(l *auth.ClientRateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ok, wait := l.Allow(auth.ClientKey(r, a.cfg.TrustForwardedFor))
			if !ok {
				w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
				writeError(w, r, http.StatusTooManyRequests, "client_rate_limited", "Too many requests from this client address")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
