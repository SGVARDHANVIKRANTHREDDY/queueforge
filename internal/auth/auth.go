package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"queueforge/internal/domain"
	"queueforge/internal/postgres"
)

type contextKey string

const (
	PrincipalContextKey contextKey = "qf_principal"
	TenantContextKey    contextKey = "qf_tenant"
)

type Principal struct {
	TenantID    uuid.UUID
	APIKeyID    uuid.UUID
	KeyName     string
	Permissions []string
}

func (p *Principal) HasPermission(perm string) bool {
	for _, permItem := range p.Permissions {
		if permItem == perm || permItem == "*" || permItem == "admin" {
			return true
		}
	}
	return false
}

func GetPrincipal(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(PrincipalContextKey).(*Principal)
	return p, ok
}

func GetTenant(ctx context.Context) (*domain.Tenant, bool) {
	t, ok := ctx.Value(TenantContextKey).(*domain.Tenant)
	return t, ok
}

// DefaultFailureLimiter allows 20 failed authentications per client address per minute.
var DefaultFailureLimiter = NewFailureLimiter(20, time.Minute)

// Middleware authenticates API keys using DefaultFailureLimiter and ignores X-Forwarded-For.
func Middleware(tenantRepo *postgres.TenantRepo) func(http.Handler) http.Handler {
	return MiddlewareWithLimiter(tenantRepo, DefaultFailureLimiter, false)
}

// MiddlewareWithLimiter authenticates API keys. A client address that fails authentication too
// often is rejected with 429 BEFORE any database work is done.
func MiddlewareWithLimiter(tenantRepo *postgres.TenantRepo, limiter *FailureLimiter, trustForwarded bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			client := ClientKey(r, trustForwarded)
			if blocked, retry := limiter.Blocked(client); blocked {
				w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
				writeAuthError(w, r, http.StatusTooManyRequests, "too_many_failed_attempts", "Too many failed authentication attempts; slow down")
				return
			}
			fail := func(status int, code, msg string) {
				limiter.RecordFailure(client)
				writeAuthError(w, r, status, code, msg)
			}
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				fail(http.StatusUnauthorized, "unauthorized", "Missing Authorization header")
				return
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				fail(http.StatusUnauthorized, "unauthorized", "Invalid Authorization header format; expected Bearer token")
				return
			}

			rawToken := strings.TrimSpace(parts[1])
			if len(rawToken) < 16 || len(rawToken) > 256 {
				fail(http.StatusUnauthorized, "unauthorized", "Invalid API key format or length")
				return
			}

			apiKey, tenant, err := tenantRepo.LookupAPIKeyBySecret(r.Context(), rawToken)
			if err != nil {
				fail(http.StatusUnauthorized, "unauthorized", "Invalid or expired API key")
				return
			}

			if !apiKey.IsActive() {
				fail(http.StatusUnauthorized, "unauthorized", "API key has been revoked or expired")
				return
			}

			if tenant.Status != domain.TenantActive {
				writeAuthError(w, r, http.StatusForbidden, "tenant_suspended", "Tenant account is suspended")
				return
			}

			principal := &Principal{
				TenantID:    apiKey.TenantID,
				APIKeyID:    apiKey.ID,
				KeyName:     apiKey.Name,
				Permissions: apiKey.Permissions,
			}

			ctx := context.WithValue(r.Context(), PrincipalContextKey, principal)
			ctx = context.WithValue(ctx, TenantContextKey, tenant)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RequirePermission(perm string) func(http.Handler) http.Handler {
	return RequireAnyPermission(perm)
}

func RequireAnyPermission(perms ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := GetPrincipal(r.Context())
			if !ok {
				writeAuthError(w, r, http.StatusUnauthorized, "unauthorized", "Authentication required")
				return
			}
			hasAny := false
			for _, p := range perms {
				if principal.HasPermission(p) {
					hasAny = true
					break
				}
			}
			if !hasAny {
				writeAuthError(w, r, http.StatusForbidden, "forbidden", "Insufficient permissions for this operation")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeAuthError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	reqID := r.Header.Get("X-Request-ID")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	errPayload := map[string]string{
		"code":    code,
		"message": message,
	}
	if reqID != "" {
		errPayload["request_id"] = reqID
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": errPayload,
	})
}
