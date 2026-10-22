package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/domain"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/workerkit"
)

// The bundled worker is just workerkit.Run with sample handlers. To run YOUR task types, copy this
// file into your own module and register your handlers (see the workerkit package documentation).
func main() {
	handlers := map[string]workerkit.Handler{
		"echo": func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
			slog.Info("processing echo task", "payload", string(payload))
			return payload, nil
		},

		"placement_notice.send": func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
			var p struct {
				NoticeID       string `json:"notice_id"`
				RecipientGroup string `json:"recipient_group"`
				Message        string `json:"message"`
			}
			if err := json.Unmarshal(payload, &p); err != nil {
				return nil, domain.NewPermanentError(fmt.Errorf("invalid placement notice payload: %w", err))
			}
			slog.Info("dispatching placement notice", "notice_id", p.NoticeID, "group", p.RecipientGroup)
			time.Sleep(100 * time.Millisecond) // Simulated send
			res, _ := json.Marshal(map[string]any{
				"sent_at":   time.Now().UTC().Format(time.RFC3339),
				"notice_id": p.NoticeID,
				"status":    "delivered",
			})
			return res, nil
		},

		"reminder.send": func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
			slog.Info("processing reminder task", "payload", string(payload))
			time.Sleep(50 * time.Millisecond)
			return json.Marshal(map[string]string{"status": "delivered"})
		},

		"report.generate": func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
			slog.Info("generating report...", "payload", string(payload))
			time.Sleep(300 * time.Millisecond)
			return json.Marshal(map[string]any{"report_id": "rep_" + time.Now().Format("20060102150405"), "status": "ready"})
		},

		// test.sleep blocks for payload.ms milliseconds (or until cancelled). Used by the kill -9 chaos
		// test and for live demos ("start a slow job, then kill the worker").
		"test.sleep": func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
			var p struct {
				Ms int `json:"ms"`
			}
			_ = json.Unmarshal(payload, &p)
			select {
			case <-time.After(time.Duration(p.Ms) * time.Millisecond):
				return json.Marshal(map[string]any{"slept_ms": p.Ms, "pid": os.Getpid()})
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},

		"test.fail": func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
			var p struct {
				Permanent bool   `json:"permanent"`
				Message   string `json:"message"`
			}
			_ = json.Unmarshal(payload, &p)
			if p.Message == "" {
				p.Message = "deliberate failure triggered for testing"
			}
			if p.Permanent {
				return nil, domain.NewPermanentError(errors.New(p.Message))
			}
			return nil, errors.New(p.Message)
		},
	}
	if err := workerkit.Run(handlers); err != nil {
		slog.Error("worker failed", "error", err)
		os.Exit(1)
	}
}
