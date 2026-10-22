package domain

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strings"
)

var (
	traceparentRe = regexp.MustCompile(`^[0-9a-f]{2}-([0-9a-f]{32})-[0-9a-f]{16}-[0-9a-f]{2}$`)
	safeIDRe      = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)
)

// NewTraceID returns a random W3C-compatible trace id (32 lowercase hex characters).
func NewTraceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ResolveTraceID picks the correlation id for a new job. Priority: the trace id of an incoming W3C
// `traceparent` header (so QueueForge joins the caller's trace); else a well-formed caller
// supplied request id; else a fresh one. Anything malformed is ignored rather than trusted:
// the value ends up in logs and a database column.
func ResolveTraceID(traceparent, requestID string) string {
	if m := traceparentRe.FindStringSubmatch(strings.ToLower(strings.TrimSpace(traceparent))); m != nil && m[1] != strings.Repeat("0", 32) {
		return m[1]
	}
	if id := strings.TrimSpace(requestID); safeIDRe.MatchString(id) {
		return id
	}
	return NewTraceID()
}
