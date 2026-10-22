// Package workerkit is the PUBLIC entry point for writing QueueForge workers.
//
// Everything under internal/ is invisible to other Go modules, so before this package existed the
// only way to add your own task handlers was to fork the repository. Import this package from your
// own module instead:
//
//	func main() {
//	    err := workerkit.Run(map[string]workerkit.Handler{
//	        "email.send": func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
//	            // ... do the work; honour ctx.Done(); be idempotent (delivery is at-least-once)
//	            return json.RawMessage(`{"ok":true}`), nil
//	        },
//	    })
//	    if err != nil { log.Fatal(err) }
//	}
//
// Configuration comes from the same QF_* environment variables as the bundled worker.
// Return workerkit.Permanent(err) for failures that must not be retried. A handler that panics is
// recovered and retried like any other failure.
package workerkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/config"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/domain"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/observability"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/recovery"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/retention"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/scheduler"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/worker"
	"github.com/prometheus/client_golang/prometheus"
)

// Handler processes one job. Return the (optional) JSON result, or an error to fail the attempt.
type Handler = func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error)

// Permanent marks err as non-retryable: the job goes straight to the dead-letter queue.
func Permanent(err error) error { return domain.NewPermanentError(err) }

// Run starts a worker process (claiming, heartbeats, lease recovery, scheduler, retention and the
// private ops/metrics listener) and blocks until SIGINT or SIGTERM, then drains gracefully.
func Run(handlers map[string]Handler) error {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := postgres.NewDB(ctx, cfg)
	if err != nil {
		return fmt.Errorf("database connection failure: %w", err)
	}
	defer db.Close()
	prometheus.MustRegister(observability.NewPoolCollector(db.Pool))

	jobRepo := postgres.NewJobRepo(db)
	workerRepo := postgres.NewWorkerRepo(db)
	scheduleRepo := postgres.NewScheduleRepo(db)
	retentionRepo := postgres.NewRetentionRepo(db)
	metrics := observability.DefaultMetrics

	w := worker.NewWorker(cfg, jobRepo, workerRepo, metrics)
	for taskType, h := range handlers {
		if err := w.Register(taskType, worker.Handler(h)); err != nil {
			return fmt.Errorf("register %q: %w", taskType, err)
		}
	}

	metricsSrv := observability.NewOpsServer(cfg.MetricsAddr, db.Ping)
	go func() {
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("worker ops server failed", "addr", cfg.MetricsAddr, "error", err)
		}
	}()

	w.Start()
	recoveryLoop := recovery.NewRecoveryLoop(cfg, jobRepo, metrics)
	recoveryLoop.Start()
	sched := scheduler.NewScheduler(cfg, scheduleRepo, metrics)
	sched.Start()
	retentionSvc := retention.NewRetentionService(cfg, retentionRepo, metrics)
	retentionSvc.Start()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	slog.Info("shutting down worker services...")
	_ = metricsSrv.Shutdown(context.Background())
	retentionSvc.Stop()
	sched.Stop()
	recoveryLoop.Stop()
	w.Stop(cfg.ShutdownTimeout)
	slog.Info("worker process exited cleanly")
	return nil
}
