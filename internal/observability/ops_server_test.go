package observability

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func get(h http.Handler, path string) int {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code
}

func TestOpsHandler(t *testing.T) {
	healthy := NewOpsHandler(func(context.Context) error { return nil })
	broken := NewOpsHandler(func(context.Context) error { return errors.New("db down") })

	if c := get(healthy, "/healthz"); c != 200 {
		t.Fatalf("healthz = %d", c)
	}
	if c := get(healthy, "/readyz"); c != 200 {
		t.Fatalf("readyz healthy = %d", c)
	}
	if c := get(broken, "/readyz"); c != 503 {
		t.Fatalf("readyz with DB down = %d, want 503", c)
	}
	if c := get(broken, "/healthz"); c != 200 {
		t.Fatalf("healthz must stay 200 when only the DB is down (liveness != readiness), got %d", c)
	}
	if c := get(healthy, "/metrics"); c != 200 {
		t.Fatalf("metrics = %d", c)
	}
}
