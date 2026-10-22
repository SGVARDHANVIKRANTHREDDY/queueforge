package main

import (
	"context"
	"github.com/prometheus/client_golang/prometheus"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"queueforge/internal/config"
	"queueforge/internal/httpapi"
	"queueforge/internal/observability"
	"queueforge/internal/postgres"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	slog.Info("applying database migrations...")
	if err := postgres.RunMigrations(cfg.MigrationURL()); err != nil {
		slog.Error("migration failure", "error", err)
		os.Exit(1)
	}
	slog.Info("migrations applied successfully")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := postgres.NewDB(ctx, cfg)
	if err != nil {
		slog.Error("database connection failure", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Queue-level gauges (depth, oldest runnable job age, live workers) are exposed by the API process
	// only, so they are not duplicated per worker replica.
	prometheus.MustRegister(observability.NewQueueStatsCollector(db.Pool).WithRetention(cfg.TerminalJobRetention).WithQueues(cfg.AllowedQueues), observability.NewPoolCollector(db.Pool))

	tenantRepo := postgres.NewTenantRepo(db)
	jobRepo := postgres.NewJobRepo(db)
	scheduleRepo := postgres.NewScheduleRepo(db)
	workerRepo := postgres.NewWorkerRepo(db)
	metrics := observability.DefaultMetrics

	api := httpapi.NewAPI(cfg, db, tenantRepo, jobRepo, scheduleRepo, workerRepo, metrics)

	server := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      api.Routes(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Metrics/health live on a PRIVATE listener, not the public API port.
	opsServer := observability.NewOpsServer(cfg.MetricsAddr, db.Ping)
	go func() {
		slog.Info("QueueForge ops server listening (private)", "addr", cfg.MetricsAddr)
		if err := opsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("ops server error", "error", err)
		}
	}()

	go func() {
		slog.Info("QueueForge API server listening", "addr", cfg.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "error", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	slog.Info("shutting down API server gracefully...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()

	_ = opsServer.Shutdown(shutdownCtx)
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("server forced to shutdown", "error", err)
	}
	slog.Info("server exited cleanly")
}
