package recovery

import (
	"context"
	"log/slog"
	"time"

	"queueforge/internal/config"
	"queueforge/internal/domain"
	"queueforge/internal/observability"
	"queueforge/internal/postgres"
)

type RecoveryLoop struct {
	cfg           *config.Config
	jobRepo       *postgres.JobRepo
	metrics       *observability.Metrics
	backoffPolicy *domain.BackoffPolicy
	ctx           context.Context
	cancel        context.CancelFunc
}

func NewRecoveryLoop(cfg *config.Config, jobRepo *postgres.JobRepo, metrics *observability.Metrics) *RecoveryLoop {
	ctx, cancel := context.WithCancel(context.Background())
	return &RecoveryLoop{
		cfg:           cfg,
		jobRepo:       jobRepo,
		metrics:       metrics,
		backoffPolicy: domain.NewBackoffPolicy(cfg.RetryBaseDelay, cfg.RetryMaxDelay, nil),
		ctx:           ctx,
		cancel:        cancel,
	}
}

func (r *RecoveryLoop) Start() {
	slog.Info("starting lease recovery loop", "interval", r.cfg.RecoveryInterval)
	go s_run(r)
}

func s_run(r *RecoveryLoop) {
	ticker := time.NewTicker(r.cfg.RecoveryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.tick()
		}
	}
}

func (r *RecoveryLoop) Stop() {
	slog.Info("stopping lease recovery loop")
	r.cancel()
}

func (r *RecoveryLoop) tick() {
	start := time.Now()
	ctx, cancel := context.WithTimeout(r.ctx, 15*time.Second)
	defer cancel()

	count, err := r.jobRepo.RecoverExpiredLeases(ctx, 50, r.backoffPolicy)
	duration := time.Since(start).Seconds()

	if err != nil {
		slog.Error("error during lease recovery scan", "error", err)
		if r.metrics != nil {
			r.metrics.RecoveryScanDurationSeconds.WithLabelValues("error").Observe(duration)
		}
		return
	}

	if r.metrics != nil {
		r.metrics.RecoveryScanDurationSeconds.WithLabelValues("success").Observe(duration)
		if count > 0 {
			r.metrics.JobLeaseRecoveriesTotal.WithLabelValues("all").Add(float64(count))
		}
	}

	if count > 0 {
		slog.Warn("recovered expired job leases", "count", count)
	}
}
