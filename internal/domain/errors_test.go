package domain_test

import (
	"errors"
	"fmt"
	"testing"

	"queueforge/internal/domain"
)

func TestPermanentError(t *testing.T) {
	inner := errors.New("invalid payload structure")
	perm := domain.NewPermanentError(inner)

	// Verify errors.As detection
	var target domain.PermanentError
	if !errors.As(perm, &target) {
		t.Fatalf("Expected errors.As to match PermanentError")
	}

	if target.Error() != inner.Error() {
		t.Errorf("Expected error message %q, got %q", inner.Error(), target.Error())
	}

	if !errors.Is(perm, inner) {
		t.Errorf("Expected errors.Is to match inner error through unwrapping")
	}
}

func TestAppError_Formatting(t *testing.T) {
	appErr := &domain.AppError{
		Code:       "idempotency_key_reused",
		Message:    "Key already bound",
		HTTPStatus: 409,
		RequestID:  "req_123",
		Err:        fmt.Errorf("hash mismatch"),
	}

	msg := appErr.Error()
	if msg != "[idempotency_key_reused] Key already bound: hash mismatch" {
		t.Errorf("Unexpected AppError string: %s", msg)
	}
}
