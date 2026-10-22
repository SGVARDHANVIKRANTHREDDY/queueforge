package retention

import (
	"context"
	"log/slog"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/config"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/observability"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
)

type RetentionService struct {
	cfg           *config.Config
	retentionRepo *postgres.RetentionRepo
	metrics       *observability.Metrics
	ctx           context.Context
	cancel        context.CancelFunc
}

func NewRetentionService(cfg *config.Config, retentionRepo *postgres.RetentionRepo, metrics *observability.Metrics) *RetentionService {
	ctx, cancel := context.WithCancel(context.Background())
	return &RetentionService{
		cfg:           cfg,
		retentionRepo: retentionRepo,
		metrics:       metrics,
		ctx:           ctx,
		cancel:        cancel,
	}
}

func (s *RetentionService) Start() {
	slog.Info("starting retention cleanup service", "interval", s.cfg.RetentionInterval)
	go s.run()
}

func (s *RetentionService) Stop() {
	slog.Info("stopping retention cleanup service")
	s.cancel()
}

func (s *RetentionService) run() {
	ticker := time.NewTicker(s.cfg.RetentionInterval)
	defer ticker.Stop()

	// Perform initial cleanup shortly after startup
	time.Sleep(10 * time.Second)
	s.tick()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

func (s *RetentionService) tick() {
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	res, err := s.retentionRepo.CleanupExpired(ctx, s.cfg.TerminalJobRetention, 1000)
	if err != nil {
		slog.Error("error during retention cleanup", "error", err)
	}
	if res == nil {
		return
	}
	if s.metrics != nil {
		s.metrics.RetentionDeletedTotal.WithLabelValues("jobs").Add(float64(res.TerminalJobsDeleted))
		s.metrics.RetentionDeletedTotal.WithLabelValues("idempotency_keys").Add(float64(res.ExpiredIdempotencyKeysDeleted))
		s.metrics.RetentionDeletedTotal.WithLabelValues("workers").Add(float64(res.StaleWorkersDeleted))
	}

	if res.ExpiredIdempotencyKeysDeleted > 0 || res.TerminalJobsDeleted > 0 || res.StaleWorkersDeleted > 0 {
		slog.Info("retention cleanup finished",
			"keys_deleted", res.ExpiredIdempotencyKeysDeleted,
			"jobs_deleted", res.TerminalJobsDeleted,
			"stale_workers_deleted", res.StaleWorkersDeleted,
		)
	}
}
