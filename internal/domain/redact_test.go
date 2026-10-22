package domain_test

import (
	"strings"
	"testing"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/domain"
)

func TestRedactSecrets(t *testing.T) {
	cases := []struct{ in, mustNotContain string }{
		{"dial postgres://app:hunter2secret@db:5432/x failed", "hunter2secret"},
		{"401 from upstream: Authorization: Bearer abcdef1234567890", "abcdef1234567890"},
		{"bad key qf_live_AbCdEf1234567890XYZ", "AbCdEf1234567890XYZ"},
		{"config error password=SuperSecret99 host=x", "SuperSecret99"},
		{`json: {"api_key":"zzzz9999yyyy"}`, "zzzz9999yyyy"},
		{"aws AKIAIOSFODNN7EXAMPLE rejected", "AKIAIOSFODNN7EXAMPLE"},
	}
	for _, c := range cases {
		got := domain.RedactSecrets(c.in)
		if strings.Contains(got, c.mustNotContain) {
			t.Errorf("secret leaked: in=%q out=%q", c.in, got)
		}
		if !strings.Contains(got, "[REDACTED]") {
			t.Errorf("expected a redaction marker: %q", got)
		}
	}
}

func TestRedactSecrets_LeavesOrdinaryErrorsAlone(t *testing.T) {
	in := "connection refused while sending email to user 42"
	if got := domain.RedactSecrets(in); got != in {
		t.Fatalf("ordinary message changed: %q", got)
	}
	if domain.RedactSecrets("") != "" {
		t.Fatal("empty string must stay empty")
	}
}
