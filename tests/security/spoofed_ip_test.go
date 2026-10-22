package security_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A client must not be able to dodge the failure limiter by sending a different
// X-Forwarded-For / X-Real-IP header on every request, unless the operator has
// explicitly said the API sits behind a trusted proxy (QF_TRUST_FORWARDED_FOR).
func TestBruteForceCannotBeBypassedWithSpoofedForwardingHeaders(t *testing.T) {
	g := newRig(t)
	bad := "qf_live_" + strings.Repeat("y", 40)
	saw429 := false
	for i := 0; i < 60; i++ {
		req := httptest.NewRequest("GET", "/v1/jobs", nil)
		req.RemoteAddr = "203.0.113.50:40000"
		req.Header.Set("Authorization", "Bearer "+bad)
		req.Header.Set("X-Forwarded-For", "10.9.8."+strings.Repeat("1", i%3+1)+","+"192.0.2."+string(rune('0'+i%10)))
		req.Header.Set("X-Real-IP", "198.18.0."+string(rune('0'+i%10)))
		rec := httptest.NewRecorder()
		g.handler.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			saw429 = true
			break
		}
	}
	if !saw429 {
		t.Fatal("spoofed forwarding headers bypassed the failure limiter")
	}
}
