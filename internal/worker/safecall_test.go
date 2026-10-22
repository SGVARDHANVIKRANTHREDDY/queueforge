package worker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestSafeCall_PanicBecomesError(t *testing.T) {
	h := func(ctx context.Context, p json.RawMessage) (json.RawMessage, error) {
		panic("intentional panic fixture")
	}
	res, err := safeCall(h, context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("expected panic converted to error, got res=%v err=%v", res, err)
	}
}

func TestSafeCall_PassesThroughResultAndError(t *testing.T) {
	want := errors.New("plain failure")
	h := func(ctx context.Context, p json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"ok":true}`), want
	}
	res, err := safeCall(h, context.Background(), nil)
	if !errors.Is(err, want) || string(res) != `{"ok":true}` {
		t.Fatalf("unexpected: res=%s err=%v", res, err)
	}
}
