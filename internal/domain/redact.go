package domain

import (
	"regexp"
	"strings"
)

// Handler error text is stored in the database (last_error_message, attempt rows) and returned to
// tenants through the API. Handlers often wrap errors from libraries that echo connection strings,
// tokens or request URLs, so every error message is passed through RedactSecrets before it is
// persisted. This is a best-effort safety net, not a substitute for handlers keeping secrets out of
// their errors.

const redacted = "[REDACTED]"

var (
	// qf_live_xxx style API keys and common provider token prefixes.
	tokenRe = regexp.MustCompile(`\b(?:qf_(?:live|test)_|sk-|ghp_|gho_|xox[baprs]-|AKIA)[A-Za-z0-9_\-]{8,}`)
	// Authorization: Bearer <token>
	bearerRe = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=\-]{8,}`)
	// scheme://user:password@host
	urlCredRe = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://[^\s:/@]+):[^\s@/]+@`)
	// password=..., token: ..., "secret":"..." and similar key/value pairs
	kvRe = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|api[_-]?key|access[_-]?key|auth[_-]?token|token)\b(["']?\s*[:=]\s*["']?)[^\s"',;&]+`)
)

// RedactSecrets masks credentials that commonly leak into error messages.
func RedactSecrets(s string) string {
	if s == "" {
		return s
	}
	s = urlCredRe.ReplaceAllString(s, "${1}:"+redacted+"@")
	s = bearerRe.ReplaceAllString(s, "${1} "+redacted)
	s = tokenRe.ReplaceAllString(s, redacted)
	s = kvRe.ReplaceAllString(s, "${1}${2}"+redacted)
	return strings.TrimSpace(s)
}
