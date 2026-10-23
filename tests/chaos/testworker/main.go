// Command testworker is a TEST-ONLY worker used by the chaos tests. It registers deliberately
// misbehaving handlers, which must never ship in the real worker binary (cmd/worker).
//
// It is built and started as a separate OS process by tests/chaos/panic_worker_test.go.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/workerkit"
)

func main() {
	handlers := map[string]workerkit.Handler{
		// ok finishes immediately.
		"ok": func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"ok":true}`), nil
		},

		// slow keeps healthy work in flight next to the panicking jobs ({"ms": N}).
		"slow": func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
			var p struct {
				Ms int `json:"ms"`
			}
			_ = json.Unmarshal(payload, &p)
			select {
			case <-time.After(time.Duration(p.Ms) * time.Millisecond):
				return json.RawMessage(`{"slow":true}`), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},

		// panic crashes the goroutine running the handler. Before the safeCall fix this killed the
		// whole worker process and every job in flight on it.
		"panic": func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
			panic("deliberate test panic")
		},

		// leak fails permanently with an error message that contains a credential. The stored
		// error text must be redacted before it reaches the database.
		"leak": func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
			return nil, workerkit.Permanent(fmt.Errorf("upstream rejected request: dial postgres://svc:hunter2secret@db.internal:5432/app failed"))
		},
	}
	if err := workerkit.Run(handlers); err != nil {
		log.Fatal(err)
	}
}
