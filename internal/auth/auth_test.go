package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/auth"
	"github.com/google/uuid"
)

func TestPrincipal_HasPermission(t *testing.T) {
	p := &auth.Principal{
		TenantID:    uuid.New(),
		APIKeyID:    uuid.New(),
		Permissions: []string{"jobs:read", "jobs:write"},
	}

	if !p.HasPermission("jobs:read") {
		t.Errorf("Expected principal to have 'jobs:read'")
	}
	if !p.HasPermission("jobs:write") {
		t.Errorf("Expected principal to have 'jobs:write'")
	}
	if p.HasPermission("schedules:write") {
		t.Errorf("Did not expect principal to have 'schedules:write'")
	}

	// Wildcard admin
	adminPrincipal := &auth.Principal{
		Permissions: []string{"admin"},
	}
	if !adminPrincipal.HasPermission("schedules:write") {
		t.Errorf("Expected admin principal to have wildcard permission")
	}
}

func TestRequirePermission_Middleware(t *testing.T) {
	handler := auth.RequirePermission("jobs:write")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	// Case 1: No principal in context -> 401
	req1 := httptest.NewRequest(http.MethodPost, "/v1/jobs", nil)
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 Unauthorized, got %d", rr1.Code)
	}

	// Case 2: Insufficient permission -> 403
	pReadOnly := &auth.Principal{
		Permissions: []string{"jobs:read"},
	}
	req2 := httptest.NewRequest(http.MethodPost, "/v1/jobs", nil)
	req2 = req2.WithContext(context.WithValue(req2.Context(), auth.PrincipalContextKey, pReadOnly))
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden, got %d", rr2.Code)
	}

	// Case 3: Proper permission -> 200
	pWrite := &auth.Principal{
		Permissions: []string{"jobs:write"},
	}
	req3 := httptest.NewRequest(http.MethodPost, "/v1/jobs", nil)
	req3 = req3.WithContext(context.WithValue(req3.Context(), auth.PrincipalContextKey, pWrite))
	rr3 := httptest.NewRecorder()
	handler.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d", rr3.Code)
	}
}
