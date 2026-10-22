package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/robfig/cron/v3"
	"queueforge/internal/domain"
)

type ScheduleRepo struct {
	db     *DB
	parser cron.Parser
}

func NewScheduleRepo(db *DB) *ScheduleRepo {
	return &ScheduleRepo{
		db: db,
		parser: cron.NewParser(
			cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
		),
	}
}

type CreateScheduleParams struct {
	TenantID       uuid.UUID
	Name           string
	QueueName      string
	TaskType       string
	Payload        json.RawMessage
	CronExpression string
	Timezone       string
	Priority       int16
	MaxAttempts    int
	TimeoutSeconds int
	MisfirePolicy  domain.MisfirePolicy
}

func (r *ScheduleRepo) ParseNextOccurrence(cronExpr, timezone string, fromTime time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid timezone %q: %w", timezone, err)
	}

	schedule, err := r.parser.Parse(cronExpr)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid cron expression %q: %w", cronExpr, err)
	}

	next := schedule.Next(fromTime.In(loc))
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("cron expression produces no future runs")
	}
	return next.UTC(), nil
}

func (r *ScheduleRepo) CreateSchedule(ctx context.Context, p CreateScheduleParams) (*domain.JobSchedule, error) {
	if p.QueueName == "" {
		p.QueueName = "default"
	}
	if p.Priority < 0 || p.Priority > 9 {
		p.Priority = 5
	}
	if p.MaxAttempts < 1 {
		p.MaxAttempts = 5
	}
	if p.TimeoutSeconds < 1 {
		p.TimeoutSeconds = 300
	}
	if p.MisfirePolicy == "" {
		p.MisfirePolicy = domain.MisfireSkip
	}

	dbTime, err := dbNow(ctx, r.db.Pool)
	if err != nil {
		return nil, err
	}
	nextRunAt, err := r.ParseNextOccurrence(p.CronExpression, p.Timezone, dbTime)
	if err != nil {
		return nil, err
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Check max schedules quota under tenant lock
	var maxSchedules int
	err = tx.QueryRow(ctx, `
		SELECT max_schedules
		FROM tenant_limits
		WHERE tenant_id = $1
		FOR UPDATE
	`, p.TenantID).Scan(&maxSchedules)
	if err != nil {
		return nil, fmt.Errorf("failed to lock tenant limits: %w", err)
	}

	var activeCount int
	err = tx.QueryRow(ctx, `
		SELECT count(*)
		FROM job_schedules
		WHERE tenant_id = $1 AND disabled_at IS NULL
	`, p.TenantID).Scan(&activeCount)
	if err != nil {
		return nil, fmt.Errorf("failed to count active schedules: %w", err)
	}

	if activeCount >= maxSchedules {
		return nil, domain.ErrQuotaExceeded
	}

	scheduleID := uuid.New()
	now := dbTime
	s := &domain.JobSchedule{
		ID:             scheduleID,
		TenantID:       p.TenantID,
		Name:           p.Name,
		QueueName:      p.QueueName,
		TaskType:       p.TaskType,
		Payload:        p.Payload,
		CronExpression: p.CronExpression,
		Timezone:       p.Timezone,
		Priority:       p.Priority,
		MaxAttempts:    p.MaxAttempts,
		TimeoutSeconds: p.TimeoutSeconds,
		MisfirePolicy:  p.MisfirePolicy,
		NextRunAt:      nextRunAt,
		Paused:         false,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO job_schedules (
			id, tenant_id, name, queue_name, task_type, payload,
			cron_expression, timezone, priority, max_attempts,
			timeout_seconds, misfire_policy, next_run_at, paused,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, false, $14, $14
		)
	`, s.ID, s.TenantID, s.Name, s.QueueName, s.TaskType, s.Payload,
		s.CronExpression, s.Timezone, s.Priority, s.MaxAttempts,
		s.TimeoutSeconds, s.MisfirePolicy, s.NextRunAt, now)
	if err != nil {
		return nil, fmt.Errorf("failed to insert schedule: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return s, nil
}

func (r *ScheduleRepo) GetSchedule(ctx context.Context, tenantID, scheduleID uuid.UUID) (*domain.JobSchedule, error) {
	row := r.db.Pool.QueryRow(ctx, `
		SELECT id, tenant_id, name, queue_name, task_type, payload,
		       cron_expression, timezone, priority, max_attempts, timeout_seconds,
		       misfire_policy, next_run_at, last_scheduled_for, pending_catch_up_at,
		       last_skipped_at, paused, disabled_at, created_at, updated_at
		FROM job_schedules
		WHERE id = $1 AND tenant_id = $2
	`, scheduleID, tenantID)

	var s domain.JobSchedule
	err := row.Scan(
		&s.ID, &s.TenantID, &s.Name, &s.QueueName, &s.TaskType, &s.Payload,
		&s.CronExpression, &s.Timezone, &s.Priority, &s.MaxAttempts, &s.TimeoutSeconds,
		&s.MisfirePolicy, &s.NextRunAt, &s.LastScheduledFor, &s.PendingCatchUpAt,
		&s.LastSkippedAt, &s.Paused, &s.DisabledAt, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return &s, nil
}

func (r *ScheduleRepo) ListSchedules(ctx context.Context, tenantID uuid.UUID) ([]domain.JobSchedule, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT id, tenant_id, name, queue_name, task_type, payload,
		       cron_expression, timezone, priority, max_attempts, timeout_seconds,
		       misfire_policy, next_run_at, last_scheduled_for, pending_catch_up_at,
		       last_skipped_at, paused, disabled_at, created_at, updated_at
		FROM job_schedules
		WHERE tenant_id = $1 AND disabled_at IS NULL
		ORDER BY created_at DESC
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var schedules []domain.JobSchedule
	for rows.Next() {
		var s domain.JobSchedule
		err := rows.Scan(
			&s.ID, &s.TenantID, &s.Name, &s.QueueName, &s.TaskType, &s.Payload,
			&s.CronExpression, &s.Timezone, &s.Priority, &s.MaxAttempts, &s.TimeoutSeconds,
			&s.MisfirePolicy, &s.NextRunAt, &s.LastScheduledFor, &s.PendingCatchUpAt,
			&s.LastSkippedAt, &s.Paused, &s.DisabledAt, &s.CreatedAt, &s.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, s)
	}
	return schedules, rows.Err()
}

func (r *ScheduleRepo) SetSchedulePaused(ctx context.Context, tenantID, scheduleID uuid.UUID, paused bool) (*domain.JobSchedule, error) {
	tag, err := r.db.Pool.Exec(ctx, `
		UPDATE job_schedules
		SET paused = $3, updated_at = clock_timestamp()
		WHERE id = $1 AND tenant_id = $2 AND disabled_at IS NULL
	`, scheduleID, tenantID, paused)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, domain.ErrNotFound
	}
	return r.GetSchedule(ctx, tenantID, scheduleID)
}

func (r *ScheduleRepo) DisableSchedule(ctx context.Context, tenantID, scheduleID uuid.UUID) error {
	tag, err := r.db.Pool.Exec(ctx, `
		UPDATE job_schedules
		SET disabled_at = clock_timestamp(), updated_at = clock_timestamp()
		WHERE id = $1 AND tenant_id = $2 AND disabled_at IS NULL
	`, scheduleID, tenantID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ProcessDueSchedules processes due recurring schedules with concurrency protection and misfire grace
func (r *ScheduleRepo) ProcessDueSchedules(ctx context.Context, batchSize int, misfireGrace time.Duration) (int, error) {
	if batchSize <= 0 {
		batchSize = 20
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT id, tenant_id, name, queue_name, task_type, payload,
		       cron_expression, timezone, priority, max_attempts, timeout_seconds,
		       misfire_policy, next_run_at, pending_catch_up_at
		FROM job_schedules
		WHERE paused = false AND disabled_at IS NULL
		  AND (next_run_at <= clock_timestamp() OR pending_catch_up_at IS NOT NULL)
		ORDER BY next_run_at ASC
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`, batchSize)
	if err != nil {
		return 0, err
	}

	type scheduleCandidate struct {
		id               uuid.UUID
		tenantID         uuid.UUID
		name             string
		queueName        string
		taskType         string
		payload          json.RawMessage
		cronExpr         string
		timezone         string
		priority         int16
		maxAttempts      int
		timeoutSeconds   int
		misfirePolicy    domain.MisfirePolicy
		nextRunAt        time.Time
		pendingCatchUpAt *time.Time
	}

	var candidates []scheduleCandidate
	for rows.Next() {
		var c scheduleCandidate
		if err := rows.Scan(
			&c.id, &c.tenantID, &c.name, &c.queueName, &c.taskType, &c.payload,
			&c.cronExpr, &c.timezone, &c.priority, &c.maxAttempts, &c.timeoutSeconds,
			&c.misfirePolicy, &c.nextRunAt, &c.pendingCatchUpAt,
		); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, c)
	}
	rows.Close()

	if len(candidates) == 0 {
		return 0, nil
	}

	now, err := dbNow(ctx, tx)
	if err != nil {
		return 0, err
	}
	jobsGenerated := 0

	for _, c := range candidates {
		// Check tenant pending quota
		var maxPending int
		var outstanding int64
		err = tx.QueryRow(ctx, `
			SELECT l.max_pending_jobs, u.outstanding_jobs
			FROM tenant_limits l
			JOIN tenant_usage u ON u.tenant_id = l.tenant_id
			WHERE l.tenant_id = $1
			FOR UPDATE OF u
		`, c.tenantID).Scan(&maxPending, &outstanding)
		if err != nil {
			continue
		}

		hasQuota := outstanding < int64(maxPending)

		// Determine target occurrence to schedule
		var scheduledFor time.Time
		fromCatchUp := false

		if c.pendingCatchUpAt != nil {
			scheduledFor = *c.pendingCatchUpAt
			fromCatchUp = true
		} else {
			// Check if normal poll delay or true misfire
			lateness := now.Sub(c.nextRunAt)
			if lateness <= misfireGrace {
				// Normal poll delay
				scheduledFor = c.nextRunAt
			} else {
				// Genuine misfire beyond grace
				if c.misfirePolicy == domain.MisfireSkip {
					// Skip: advance to future, no job
					nextFuture, _ := r.ParseNextOccurrence(c.cronExpr, c.timezone, now)
					_, _ = tx.Exec(ctx, `
						UPDATE job_schedules
						SET last_skipped_at = $2, next_run_at = $3, updated_at = clock_timestamp()
						WHERE id = $1
					`, c.id, c.nextRunAt, nextFuture)
					continue
				} else {
					// Catch-up one
					scheduledFor = c.nextRunAt
				}
			}
		}

		if !hasQuota {
			// Quota full: save pending catch-up or skip
			nextFuture, _ := r.ParseNextOccurrence(c.cronExpr, c.timezone, now)
			if c.misfirePolicy == domain.MisfireSkip {
				_, _ = tx.Exec(ctx, `
					UPDATE job_schedules
					SET last_skipped_at = $2, next_run_at = $3, updated_at = clock_timestamp()
					WHERE id = $1
				`, c.id, scheduledFor, nextFuture)
			} else {
				_, _ = tx.Exec(ctx, `
					UPDATE job_schedules
					SET pending_catch_up_at = $2, next_run_at = $3, updated_at = clock_timestamp()
					WHERE id = $1
				`, c.id, scheduledFor, nextFuture)
			}
			continue
		}

		// Insert occurrence job
		jobID := uuid.New()
		tag, err := tx.Exec(ctx, `
			INSERT INTO jobs (
				id, tenant_id, queue_name, task_type, payload, status,
				priority, run_at, created_at, updated_at, max_attempts,
				timeout_seconds, schedule_id, scheduled_for, replay_generation
			) VALUES (
				$1, $2, $3, $4, $5, 'queued',
				$6, $7, $8, $8, $9,
				$10, $11, $12, 0
			)
			ON CONFLICT (schedule_id, scheduled_for) WHERE schedule_id IS NOT NULL DO NOTHING
		`, jobID, c.tenantID, c.queueName, c.taskType, c.payload,
			c.priority, scheduledFor, now, c.maxAttempts,
			c.timeoutSeconds, c.id, scheduledFor)

		if err != nil {
			continue
		}

		if tag.RowsAffected() > 0 {
			_, _ = tx.Exec(ctx, `
				UPDATE tenant_usage
				SET outstanding_jobs = outstanding_jobs + 1, updated_at = clock_timestamp()
				WHERE tenant_id = $1
			`, c.tenantID)
			jobsGenerated++
		}

		// Advance schedule cursor
		if fromCatchUp {
			_, _ = tx.Exec(ctx, `
				UPDATE job_schedules
				SET pending_catch_up_at = NULL, last_scheduled_for = $2, updated_at = clock_timestamp()
				WHERE id = $1
			`, c.id, scheduledFor)
		} else {
			nextFuture, _ := r.ParseNextOccurrence(c.cronExpr, c.timezone, c.nextRunAt)
			_, _ = tx.Exec(ctx, `
				UPDATE job_schedules
				SET next_run_at = $2, last_scheduled_for = $3, updated_at = clock_timestamp()
				WHERE id = $1
			`, c.id, nextFuture, scheduledFor)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}

	return jobsGenerated, nil
}
