package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"queueforge/internal/config"
	"queueforge/internal/domain"
	"queueforge/internal/observability"
	"queueforge/internal/postgres"
	"queueforge/internal/worker"
)

// EchoPayload represents the input payload for the echo handler.
type EchoPayload struct {
	Message string `json:"message"`
	SleepMs int    `json:"sleep_ms,omitempty"`
	Fail    bool   `json:"fail,omitempty"`
}

// EchoResult represents the structured result returned by the handler.
type EchoResult struct {
	EchoedMessage string    `json:"echoed_message"`
	ProcessedAt   time.Time `json:"processed_at"`
	Status        string    `json:"status"`
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	slog.Info("Starting QueueForge Echo Handler Example Worker")

	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := postgres.NewDB(ctx, cfg)
	if err != nil {
		slog.Error("Failed to connect to PostgreSQL", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	metrics := observability.DefaultMetrics
	jobRepo := postgres.NewJobRepo(db)
	workerRepo := postgres.NewWorkerRepo(db)

	w := worker.NewWorker(cfg, jobRepo, workerRepo, metrics)

	// Register the echo task handler
	_ = w.Register("echo", func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
		var p EchoPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			// Permanent error if JSON payload is malformed
			return nil, domain.NewPermanentError(fmt.Errorf("malformed echo payload: %w", err))
		}

		if p.Fail {
			return nil, domain.NewPermanentError(fmt.Errorf("simulated permanent failure requested for message: %s", p.Message))
		}

		if p.SleepMs > 0 {
			select {
			case <-time.After(time.Duration(p.SleepMs) * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		res := EchoResult{
			EchoedMessage: p.Message,
			ProcessedAt:   time.Now().UTC(),
			Status:        "SUCCESS",
		}

		return json.Marshal(res)
	})

	w.Start()
	slog.Info("Worker runtime started, listening for 'echo' tasks...")

	// Listen for OS signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	<-sigChan
	slog.Info("Shutdown signal received, draining worker...")

	w.Stop(cfg.ShutdownTimeout)
	slog.Info("Echo worker gracefully stopped.")
}
