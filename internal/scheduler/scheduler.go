package scheduler

import (
	"context"
	"log/slog"
	"time"

	"queueforge/internal/config"
	"queueforge/internal/observability"
	"queueforge/internal/postgres"
)

type Scheduler struct {
	cfg          *config.Config
	scheduleRepo *postgres.ScheduleRepo
	metrics      *observability.Metrics
	ctx          context.Context
	cancel       context.CancelFunc
}

func NewScheduler(cfg *config.Config, scheduleRepo *postgres.ScheduleRepo, metrics *observability.Metrics) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		cfg:          cfg,
		scheduleRepo: scheduleRepo,
		metrics:      metrics,
		ctx:          ctx,
		cancel:       cancel,
	}
}

func (s *Scheduler) Start() {
	slog.Info("starting cron scheduler loop", "interval", s.cfg.SchedulerInterval)
	go s.run()
}

func (s *Scheduler) Stop() {
	slog.Info("stopping cron scheduler loop")
	s.cancel()
}

func (s *Scheduler) run() {
	ticker := time.NewTicker(s.cfg.SchedulerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

func (s *Scheduler) tick() {
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	count, err := s.scheduleRepo.ProcessDueSchedules(ctx, 20, s.cfg.ScheduleMisfireGrace)
	if err != nil {
		slog.Error("scheduler error processing due schedules", "error", err)
		if s.metrics != nil {
			s.metrics.SchedulerRunsTotal.WithLabelValues("error").Inc()
		}
		return
	}

	if s.metrics != nil {
		s.metrics.SchedulerRunsTotal.WithLabelValues("success").Inc()
	}

	if count > 0 {
		slog.Info("scheduler generated recurring jobs", "count", count)
	}
}
