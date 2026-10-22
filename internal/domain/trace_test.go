package domain

import (
	"strings"
	"testing"
)

func TestResolveTraceID(t *testing.T) {
	const tid = "4bf92f3577b34da6a3ce929d0e0e4736"
	if got := ResolveTraceID("00-"+tid+"-00f067aa0ba902b7-01", "req-12345678"); got != tid {
		t.Fatalf("traceparent must win: %q", got)
	}
	if got := ResolveTraceID("", "req-12345678"); got != "req-12345678" {
		t.Fatalf("request id fallback: %q", got)
	}
	for _, bad := range []string{"00-" + strings.Repeat("0", 32) + "-00f067aa0ba902b7-01", "garbage", "00-short-00f067aa0ba902b7-01"} {
		if got := ResolveTraceID(bad, ""); len(got) != 32 || got == strings.Repeat("0", 32) {
			t.Fatalf("malformed traceparent %q must yield a fresh id, got %q", bad, got)
		}
	}
	// hostile request ids (log injection / oversized) are not trusted
	for _, bad := range []string{"a\nb\nINFO fake", "x", strings.Repeat("a", 200), "<script>alert(1)</script>"} {
		got := ResolveTraceID("", bad)
		if got == bad || len(got) != 32 {
			t.Fatalf("hostile id %q was trusted: %q", bad, got)
		}
	}
	if a, b := NewTraceID(), NewTraceID(); a == b {
		t.Fatal("trace ids must be unique")
	}
}
