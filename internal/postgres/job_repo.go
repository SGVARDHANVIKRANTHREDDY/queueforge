package postgres

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"queueforge/internal/domain"
)

type JobRepo struct {
	db *DB
}

func NewJobRepo(db *DB) *JobRepo {
	return &JobRepo{db: db}
}

type EnqueueParams struct {
	TenantID        uuid.UUID
	TaskType        string
	QueueName       string
	Payload         json.RawMessage
	Priority        int16
	RunAt           time.Time
	IsExplicitRunAt bool
	MaxAttempts     int
	TimeoutSeconds  int
	IdempotencyKey  string
	IdempotencyTTL  time.Duration
	ScheduleID      *uuid.UUID
	ScheduledFor    *time.Time
	TraceID         string // correlation id; generated if empty
}

type EnqueueResult struct {
	Job              *domain.Job
	IdempotentReplay bool
	RetryAfterHint   time.Duration
}

func (r *JobRepo) prepareEnqueue(ctx context.Context, p *EnqueueParams) ([]byte, *EnqueueResult, error) {
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
	if p.IdempotencyTTL <= 0 {
		p.IdempotencyTTL = 24 * time.Hour
	}

	requestHash, err := domain.ComputeRequestHash(
		p.TaskType, p.QueueName, p.Payload, p.Priority,
		p.RunAt, p.IsExplicitRunAt, p.MaxAttempts, p.TimeoutSeconds,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to compute request hash: %w", err)
	}

	// 1. If idempotency key is provided, check existing unexpired key first
	if p.IdempotencyKey != "" {
		var existingJobID uuid.UUID
		var existingHash []byte
		var existingExpiresAt time.Time
		err := r.db.Pool.QueryRow(ctx, `
			SELECT job_id, request_hash, expires_at
			FROM idempotency_keys
			WHERE tenant_id = $1 AND idempotency_key = $2 AND expires_at > clock_timestamp()
		`, p.TenantID, p.IdempotencyKey).Scan(&existingJobID, &existingHash, &existingExpiresAt)

		if err == nil {
			if subtle.ConstantTimeCompare(requestHash, existingHash) == 1 {
				existingJob, errGet := r.GetJob(ctx, p.TenantID, existingJobID)
				if errGet == nil {
					return nil, &EnqueueResult{Job: existingJob, IdempotentReplay: true}, nil
				}
			} else {
				return nil, nil, domain.ErrIdempotencyConflict
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, fmt.Errorf("failed to check idempotency key: %w", err)
		}
	}

	return requestHash, nil, nil
}

// errIdempotencyRace signals that a concurrent enqueue with the same idempotency key committed first.
var errIdempotencyRace = errors.New("idempotency key won by a concurrent request")

// enqueueInTx performs the whole enqueue (job row, idempotency key, NOTIFY, rate limit, pending
// quota) on tx. It never commits or rolls back: the caller owns the transaction.
func (r *JobRepo) enqueueInTx(ctx context.Context, tx pgx.Tx, p EnqueueParams, requestHash []byte) (*EnqueueResult, error) {
	if p.TraceID == "" {
		p.TraceID = domain.NewTraceID()
	}
	jobID := uuid.New()

	// The DATABASE is the only clock in this system. Using the application server's clock here
	// (as an earlier version did) lets API clock drift hide jobs (clock ahead) or run them early
	// (clock behind), because ClaimJob compares run_at against the database clock.
	var runAtArg any
	if p.IsExplicitRunAt && !p.RunAt.IsZero() {
		runAtArg = p.RunAt
	}
	var runAt, createdAt time.Time
	err := tx.QueryRow(ctx, `
		INSERT INTO jobs (
			id, tenant_id, queue_name, task_type, payload, status,
			priority, run_at, created_at, updated_at, max_attempts,
			timeout_seconds, schedule_id, scheduled_for, replay_generation, trace_id
		) VALUES (
			$1, $2, $3, $4, $5, 'queued',
			$6, COALESCE($7::timestamptz, clock_timestamp()), clock_timestamp(), clock_timestamp(), $8,
			$9, $10, $11, 0, $12
		)
		RETURNING run_at, created_at
	`, jobID, p.TenantID, p.QueueName, p.TaskType, p.Payload,
		p.Priority, runAtArg, p.MaxAttempts,
		p.TimeoutSeconds, p.ScheduleID, p.ScheduledFor, p.TraceID).Scan(&runAt, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("failed to insert provisional job: %w", err)
	}

	// Idempotency key handling atomically
	if p.IdempotencyKey != "" {
		var returnedJobID uuid.UUID
		err = tx.QueryRow(ctx, `
			INSERT INTO idempotency_keys (
				tenant_id, idempotency_key, request_hash, job_id, created_at, expires_at
			) VALUES (
				$1, $2, $3, $4, clock_timestamp(), clock_timestamp() + $5::interval
			)
			ON CONFLICT (tenant_id, idempotency_key) DO UPDATE
			SET request_hash = EXCLUDED.request_hash,
			    job_id = EXCLUDED.job_id,
			    created_at = EXCLUDED.created_at,
			    expires_at = EXCLUDED.expires_at
			WHERE idempotency_keys.expires_at <= clock_timestamp()
			RETURNING job_id
		`, p.TenantID, p.IdempotencyKey, requestHash, jobID, fmt.Sprintf("%d seconds", int(p.IdempotencyTTL.Seconds()))).Scan(&returnedJobID)

		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Another unexpired record won the uniqueness race; the caller resolves it
				// after rolling back (or releasing the savepoint of) this attempt.
				return nil, errIdempotencyRace
			}
			return nil, fmt.Errorf("failed to insert idempotency key: %w", err)
		}
	}

	// Wake idle workers immediately. NOTIFY is transactional: it is delivered only if (and when)
	// this transaction commits, so a worker is never woken for a job that does not exist.
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, JobsChannel, p.QueueName); err != nil {
		return nil, fmt.Errorf("failed to notify workers: %w", err)
	}

	// Rate limit + pending quota in ONE statement.
	//
	// Why one statement: every enqueue for a tenant serialises on that tenant's bucket and usage
	// rows, and those row locks are held until COMMIT. Profiling showed lock wait on the old
	// 3-statement version dominating enqueue time (14 ms mean). One round trip shortens the hold
	// time, and doing the arithmetic in SQL means the refill uses the DATABASE clock.
	//
	// Cron-generated jobs ($2 = false) skip token consumption (TDD 12.3) but still count against
	// the pending quota. Tokens are consumed only if BOTH checks pass.
	var rate, avail float64
	var outstanding int64
	var maxPending int
	var rateOK, quotaOK bool
	err = tx.QueryRow(ctx, `
		WITH locked AS MATERIALIZED (
			SELECT l.enqueue_rate_per_second AS rate, l.enqueue_burst_capacity AS burst,
			       l.max_pending_jobs AS max_pending, b.tokens, b.last_refill_at, u.outstanding_jobs
			FROM tenant_limits l
			JOIN tenant_rate_buckets b ON b.tenant_id = l.tenant_id
			JOIN tenant_usage u ON u.tenant_id = l.tenant_id
			WHERE l.tenant_id = $1
			FOR UPDATE OF b, u
		), verdict AS (
			SELECT rate, max_pending, outstanding_jobs,
			       LEAST(burst, tokens + GREATEST(0, EXTRACT(EPOCH FROM (clock_timestamp() - last_refill_at))) * rate) AS avail
			FROM locked
		), flags AS (
			SELECT *, (NOT $2::boolean OR avail >= 1.0) AS rate_ok, (outstanding_jobs < max_pending) AS quota_ok
			FROM verdict
		), upd_bucket AS (
			UPDATE tenant_rate_buckets
			SET tokens = (SELECT avail - 1.0 FROM flags), last_refill_at = clock_timestamp(), updated_at = clock_timestamp()
			WHERE tenant_id = $1 AND $2::boolean AND (SELECT rate_ok AND quota_ok FROM flags)
			RETURNING 1
		), upd_usage AS (
			UPDATE tenant_usage
			SET outstanding_jobs = outstanding_jobs + 1, updated_at = clock_timestamp()
			WHERE tenant_id = $1 AND (SELECT rate_ok AND quota_ok FROM flags)
			RETURNING 1
		)
		SELECT rate, max_pending, outstanding_jobs, avail, rate_ok, quota_ok FROM flags
	`, p.TenantID, p.ScheduleID == nil).Scan(&rate, &maxPending, &outstanding, &avail, &rateOK, &quotaOK)
	if err != nil {
		return nil, fmt.Errorf("failed to apply tenant rate limit and quota: %w", err)
	}
	if !rateOK {
		retryAfter := time.Duration(((1.0 - avail) / rate) * float64(time.Second))
		if retryAfter < 100*time.Millisecond {
			retryAfter = 100 * time.Millisecond
		}
		return &EnqueueResult{RetryAfterHint: retryAfter}, domain.ErrRateLimited
	}
	if !quotaOK {
		return nil, domain.ErrQuotaExceeded
	}

	createdJob := &domain.Job{
		ID:               jobID,
		TenantID:         p.TenantID,
		QueueName:        p.QueueName,
		TaskType:         p.TaskType,
		Payload:          p.Payload,
		Status:           domain.StatusQueued,
		Priority:         p.Priority,
		TraceID:          p.TraceID,
		RunAt:            runAt,
		CreatedAt:        createdAt,
		UpdatedAt:        createdAt,
		MaxAttempts:      p.MaxAttempts,
		TimeoutSeconds:   p.TimeoutSeconds,
		ScheduleID:       p.ScheduleID,
		ScheduledFor:     p.ScheduledFor,
		ReplayGeneration: 0,
	}

	return &EnqueueResult{Job: createdJob, IdempotentReplay: false}, nil
}

// EnqueueBatch enqueues several jobs atomically in ONE transaction: either all are accepted or none
// is (a rate-limit, quota or other rejection rolls everything back). One commit for N jobs is much
// cheaper than N separate commits. On error, failed carries the rejected item's result (for
// RetryAfterHint) when there is one.
func (r *JobRepo) EnqueueBatch(ctx context.Context, items []EnqueueParams) (results []*EnqueueResult, failed *EnqueueResult, err error) {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	results = make([]*EnqueueResult, 0, len(items))
	for _, it := range items {
		res, err := r.EnqueueJobTx(ctx, tx, it)
		if err != nil {
			return nil, res, err
		}
		results = append(results, res)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("failed to commit batch enqueue: %w", err)
	}
	return results, nil, nil
}

// resolveIdempotencyWinner returns the committed job that won an idempotency-key race, or a
// conflict if the winning request had a different body.
func (r *JobRepo) resolveIdempotencyWinner(ctx context.Context, p EnqueueParams, requestHash []byte) (*EnqueueResult, error) {
	var winnerJobID uuid.UUID
	var winnerHash []byte
	errWinner := r.db.Pool.QueryRow(ctx, `
		SELECT job_id, request_hash
		FROM idempotency_keys
		WHERE tenant_id = $1 AND idempotency_key = $2
	`, p.TenantID, p.IdempotencyKey).Scan(&winnerJobID, &winnerHash)
	if errWinner != nil {
		return nil, fmt.Errorf("failed to read winning idempotency key: %w", errWinner)
	}
	if subtle.ConstantTimeCompare(requestHash, winnerHash) == 1 {
		if existingJob, errGet := r.GetJob(ctx, p.TenantID, winnerJobID); errGet == nil {
			return &EnqueueResult{Job: existingJob, IdempotentReplay: true}, nil
		}
	}
	return nil, domain.ErrIdempotencyConflict
}

// EnqueueJob enqueues a job in its own transaction.
func (r *JobRepo) EnqueueJob(ctx context.Context, p EnqueueParams) (*EnqueueResult, error) {
	requestHash, replay, err := r.prepareEnqueue(ctx, &p)
	if err != nil || replay != nil {
		return replay, err
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	res, err := r.enqueueInTx(ctx, tx, p, requestHash)
	if errors.Is(err, errIdempotencyRace) {
		_ = tx.Rollback(ctx)
		return r.resolveIdempotencyWinner(ctx, p, requestHash)
	}
	if err != nil {
		return res, err // deferred Rollback discards the provisional job
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit job enqueue: %w", err)
	}
	return res, nil
}

// EnqueueJobTx enqueues a job inside a transaction the CALLER owns, so the job and the caller's own
// business rows commit or roll back together (the transactional-outbox pattern). It uses a
// savepoint, so a rate-limit, quota or idempotency rejection undoes only the enqueue and leaves the
// caller's transaction usable. The job becomes visible to workers (and the NOTIFY is delivered) only
// when the caller commits. Only available when your code shares QueueForge's PostgreSQL database.
func (r *JobRepo) EnqueueJobTx(ctx context.Context, tx pgx.Tx, p EnqueueParams) (*EnqueueResult, error) {
	requestHash, replay, err := r.prepareEnqueue(ctx, &p)
	if err != nil || replay != nil {
		return replay, err
	}

	sp, err := tx.Begin(ctx) // pgx nested transaction == SAVEPOINT
	if err != nil {
		return nil, fmt.Errorf("failed to create savepoint: %w", err)
	}
	res, err := r.enqueueInTx(ctx, sp, p, requestHash)
	if errors.Is(err, errIdempotencyRace) {
		_ = sp.Rollback(ctx)
		return r.resolveIdempotencyWinner(ctx, p, requestHash)
	}
	if err != nil {
		_ = sp.Rollback(ctx)
		return res, err
	}
	if err := sp.Commit(ctx); err != nil { // RELEASE SAVEPOINT
		return nil, fmt.Errorf("failed to release savepoint: %w", err)
	}
	return res, nil
}

func (r *JobRepo) GetJob(ctx context.Context, tenantID, jobID uuid.UUID) (*domain.Job, error) {
	row := r.db.Pool.QueryRow(ctx, `
		SELECT id, tenant_id, queue_name, task_type, payload, result, status,
		       priority, run_at, created_at, updated_at, started_at, completed_at,
		       attempts_started, max_attempts, timeout_seconds, lease_token,
		       worker_id, lease_expires_at, cancellation_requested_at,
		       last_error_code, last_error_message, last_error_at,
		       schedule_id, scheduled_for, replay_generation, trace_id
		FROM jobs
		WHERE id = $1 AND tenant_id = $2
	`, jobID, tenantID)

	var j domain.Job
	err := row.Scan(
		&j.ID, &j.TenantID, &j.QueueName, &j.TaskType, &j.Payload, &j.Result, &j.Status,
		&j.Priority, &j.RunAt, &j.CreatedAt, &j.UpdatedAt, &j.StartedAt, &j.CompletedAt,
		&j.AttemptsStarted, &j.MaxAttempts, &j.TimeoutSeconds, &j.LeaseToken,
		&j.WorkerID, &j.LeaseExpiresAt, &j.CancellationRequestedAt,
		&j.LastErrorCode, &j.LastErrorMessage, &j.LastErrorAt,
		&j.ScheduleID, &j.ScheduledFor, &j.ReplayGeneration, &j.TraceID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return &j, nil
}

type ListJobsFilter struct {
	TenantID uuid.UUID
	Status   *domain.JobStatus
	Queue    *string
	TaskType *string
	Limit    int
	Cursor   *time.Time
}

func (r *JobRepo) ListJobs(ctx context.Context, f ListJobsFilter) ([]domain.Job, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}

	query := `
		SELECT id, tenant_id, queue_name, task_type, payload, result, status,
		       priority, run_at, created_at, updated_at, started_at, completed_at,
		       attempts_started, max_attempts, timeout_seconds, lease_token,
		       worker_id, lease_expires_at, cancellation_requested_at,
		       last_error_code, last_error_message, last_error_at,
		       schedule_id, scheduled_for, replay_generation, trace_id
		FROM jobs
		WHERE tenant_id = $1
	`
	args := []any{f.TenantID}
	idx := 2

	if f.Status != nil {
		query += fmt.Sprintf(" AND status = $%d", idx)
		args = append(args, *f.Status)
		idx++
	}
	if f.Queue != nil && *f.Queue != "" {
		query += fmt.Sprintf(" AND queue_name = $%d", idx)
		args = append(args, *f.Queue)
		idx++
	}
	if f.TaskType != nil && *f.TaskType != "" {
		query += fmt.Sprintf(" AND task_type = $%d", idx)
		args = append(args, *f.TaskType)
		idx++
	}
	if f.Cursor != nil {
		query += fmt.Sprintf(" AND created_at < $%d", idx)
		args = append(args, *f.Cursor)
		idx++
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", idx)
	args = append(args, f.Limit)

	rows, err := r.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []domain.Job
	for rows.Next() {
		var j domain.Job
		err := rows.Scan(
			&j.ID, &j.TenantID, &j.QueueName, &j.TaskType, &j.Payload, &j.Result, &j.Status,
			&j.Priority, &j.RunAt, &j.CreatedAt, &j.UpdatedAt, &j.StartedAt, &j.CompletedAt,
			&j.AttemptsStarted, &j.MaxAttempts, &j.TimeoutSeconds, &j.LeaseToken,
			&j.WorkerID, &j.LeaseExpiresAt, &j.CancellationRequestedAt,
			&j.LastErrorCode, &j.LastErrorMessage, &j.LastErrorAt,
			&j.ScheduleID, &j.ScheduledFor, &j.ReplayGeneration, &j.TraceID,
		)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}

	return jobs, rows.Err()
}

func (r *JobRepo) GetJobAttempts(ctx context.Context, tenantID, jobID uuid.UUID) ([]domain.JobAttempt, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT id, job_id, tenant_id, replay_generation, attempt_number,
		       worker_id, lease_token, started_at, finished_at, outcome,
		       error_code, error_message, duration_ms
		FROM job_attempts
		WHERE job_id = $1 AND tenant_id = $2
		ORDER BY replay_generation ASC, attempt_number ASC
	`, jobID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var attempts []domain.JobAttempt
	for rows.Next() {
		var a domain.JobAttempt
		err := rows.Scan(
			&a.ID, &a.JobID, &a.TenantID, &a.ReplayGeneration, &a.AttemptNumber,
			&a.WorkerID, &a.LeaseToken, &a.StartedAt, &a.FinishedAt, &a.Outcome,
			&a.ErrorCode, &a.ErrorMessage, &a.DurationMs,
		)
		if err != nil {
			return nil, err
		}
		attempts = append(attempts, a)
	}
	return attempts, rows.Err()
}

type ClaimResult struct {
	Job        *domain.Job
	LeaseToken uuid.UUID
	SlotNo     int
	// QueueWaitSeconds is how long the job was runnable before this claim, measured entirely on
	// the database clock (the worker's own clock is never consulted).
	QueueWaitSeconds float64
}

type RenewLeaseResult struct {
	CancellationRequested bool
}

func (r *JobRepo) RenewLease(ctx context.Context, jobID, leaseToken uuid.UUID, leaseDuration time.Duration) (*RenewLeaseResult, error) {
	var cancellationRequestedAt *time.Time
	err := r.db.Pool.QueryRow(ctx, `
		UPDATE jobs
		SET lease_expires_at = clock_timestamp() + $3::interval,
		    updated_at = clock_timestamp()
		WHERE id = $1
		  AND status = 'running'
		  AND lease_token = $2
		  AND lease_expires_at > clock_timestamp()
		RETURNING cancellation_requested_at
	`, jobID, leaseToken, fmt.Sprintf("%d seconds", int(leaseDuration.Seconds()))).Scan(&cancellationRequestedAt)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrLeaseLost
		}
		return nil, err
	}

	return &RenewLeaseResult{
		CancellationRequested: cancellationRequestedAt != nil,
	}, nil
}

// CompleteJobSuccess records a successful attempt, releases the tenant slot and decrements the
// outstanding-jobs counter in ONE statement (data-modifying CTEs). One statement is atomic on its
// own, so there is no explicit transaction: 1 round trip and 1 commit instead of 6 round trips.
//
// Fencing: every row change hangs off the job update, which matches only if the caller still holds
// the current lease_token. A worker that lost its lease updates nothing and gets ErrLeaseLost.
func (r *JobRepo) CompleteJobSuccess(ctx context.Context, tenantID, jobID, leaseToken uuid.UUID, result json.RawMessage, durationMs int64) error {
	var completed int
	err := r.db.Pool.QueryRow(ctx, `
		WITH done AS (
			UPDATE jobs
			SET status = 'succeeded', result = $3, completed_at = clock_timestamp(),
			    lease_token = NULL, worker_id = NULL, lease_expires_at = NULL,
			    updated_at = clock_timestamp()
			WHERE id = $1 AND status = 'running' AND lease_token = $2
			RETURNING id, replay_generation, attempts_started
		), att AS (
			UPDATE job_attempts a
			SET finished_at = clock_timestamp(), outcome = $4, duration_ms = $5
			FROM done
			WHERE a.job_id = done.id AND a.replay_generation = done.replay_generation
			  AND a.attempt_number = done.attempts_started AND a.lease_token = $2
			RETURNING 1
		), slot AS (
			UPDATE tenant_execution_slots
			SET current_job_id = NULL, lease_token = NULL, updated_at = clock_timestamp()
			WHERE tenant_id = $6 AND current_job_id = $1 AND lease_token = $2
			  AND EXISTS (SELECT 1 FROM done)
			RETURNING 1
		), usage AS (
			UPDATE tenant_usage
			SET outstanding_jobs = GREATEST(0, outstanding_jobs - 1), updated_at = clock_timestamp()
			WHERE tenant_id = $6 AND EXISTS (SELECT 1 FROM done)
			RETURNING 1
		)
		SELECT count(*) FROM done
	`, jobID, leaseToken, result, domain.OutcomeSucceeded, durationMs, tenantID).Scan(&completed)
	if err != nil {
		return err
	}
	if completed == 0 {
		return domain.ErrLeaseLost
	}
	return nil
}

// SuccessCompletion is one finished job to record in CompleteJobsSuccessBatch.
type SuccessCompletion struct {
	TenantID, JobID, LeaseToken uuid.UUID
	Result                      json.RawMessage
	DurationMs                  int64
}

// CompleteJobsSuccessBatch records many successful jobs in ONE statement (one round trip, one
// commit) with exactly the guarantees of CompleteJobSuccess: each row is fenced by its own lease
// token, its attempt row is finished, its execution slot is released and the tenant's outstanding
// counter drops by the number of jobs actually completed. The returned slice is aligned with items;
// false means that job's lease was no longer valid (stale completion, rejected).
//
// Callers must fall back to CompleteJobSuccess on error: a batch can fail as a whole (for example a
// deadlock between two batches touching the same tenant rows) even though each job is fine.
func (r *JobRepo) CompleteJobsSuccessBatch(ctx context.Context, items []SuccessCompletion) ([]bool, error) {
	if len(items) == 0 {
		return nil, nil
	}
	jobIDs := make([]string, len(items))
	tenantIDs := make([]string, len(items))
	tokens := make([]string, len(items))
	results := make([]string, len(items)) // "" means NULL
	durations := make([]int64, len(items))
	for i, it := range items {
		jobIDs[i], tenantIDs[i], tokens[i] = it.JobID.String(), it.TenantID.String(), it.LeaseToken.String()
		if len(it.Result) > 0 {
			results[i] = string(it.Result)
		}
		durations[i] = it.DurationMs
	}

	rows, err := r.db.Pool.Query(ctx, `
		WITH input AS (
			SELECT t.job_id::uuid AS job_id, t.tenant_id::uuid AS tenant_id,
			       t.lease_token::uuid AS lease_token,
			       NULLIF(t.result, '')::jsonb AS result, t.duration_ms
			FROM unnest($1::text[], $2::text[], $3::text[], $4::text[], $5::bigint[])
			     AS t(job_id, tenant_id, lease_token, result, duration_ms)
		), done AS (
			UPDATE jobs j
			SET status = 'succeeded', result = i.result, completed_at = clock_timestamp(),
			    lease_token = NULL, worker_id = NULL, lease_expires_at = NULL,
			    updated_at = clock_timestamp()
			FROM input i
			WHERE j.id = i.job_id AND j.tenant_id = i.tenant_id
			  AND j.status = 'running' AND j.lease_token = i.lease_token
			RETURNING j.id, j.tenant_id, j.replay_generation, j.attempts_started,
			          i.lease_token AS old_token, i.duration_ms
		), att AS (
			UPDATE job_attempts a
			SET finished_at = clock_timestamp(), outcome = $6, duration_ms = d.duration_ms
			FROM done d
			WHERE a.job_id = d.id AND a.replay_generation = d.replay_generation
			  AND a.attempt_number = d.attempts_started AND a.lease_token = d.old_token
			RETURNING 1
		), slot AS (
			UPDATE tenant_execution_slots s
			SET current_job_id = NULL, lease_token = NULL, updated_at = clock_timestamp()
			FROM done d
			WHERE s.tenant_id = d.tenant_id AND s.current_job_id = d.id AND s.lease_token = d.old_token
			RETURNING 1
		), usage AS (
			UPDATE tenant_usage u
			SET outstanding_jobs = GREATEST(0, u.outstanding_jobs - c.n), updated_at = clock_timestamp()
			FROM (SELECT tenant_id, count(*) AS n FROM done GROUP BY tenant_id) c
			WHERE u.tenant_id = c.tenant_id
			RETURNING 1
		)
		SELECT id FROM done
	`, jobIDs, tenantIDs, tokens, results, durations, domain.OutcomeSucceeded)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	completed := make(map[uuid.UUID]bool, len(items))
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		completed[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ok := make([]bool, len(items))
	for i, it := range items {
		ok[i] = completed[it.JobID]
	}
	return ok, nil
}

// attemptFailure describes one failed attempt that must be settled: either requeued with
// backoff, or moved to 'dead' if it is permanent or out of attempts.
type attemptFailure struct {
	jobID, tenantID, leaseToken     uuid.UUID
	replayGen, attemptsStarted, max int
	forceDead                       bool                  // permanent error: never retry
	attemptOutcome                  domain.AttemptOutcome // recorded on job_attempts
	attemptCode, errMessage         string                // recorded on job_attempts (and job when requeued)
	deadCode                        string                // job.last_error_code if the job becomes dead
	durationMs                      *int64                // nil when unknown (e.g. worker crashed)
}

// settleFailedAttempt is the single place that implements retry-vs-dead. It is used both when a
// handler returns an error and when crash recovery finds an expired lease, so the two paths can
// never drift apart. It must run inside tx and assumes the job row is already locked by the caller.
func settleFailedAttempt(ctx context.Context, tx pgx.Tx, f attemptFailure, backoff *domain.BackoffPolicy) (dead bool, err error) {
	dead = f.forceDead || f.attemptsStarted >= f.max

	if dead {
		_, err = tx.Exec(ctx, `
			UPDATE jobs
			SET status = 'dead', completed_at = clock_timestamp(),
			    lease_token = NULL, worker_id = NULL, lease_expires_at = NULL,
			    last_error_code = $3, last_error_message = $4, last_error_at = clock_timestamp(),
			    updated_at = clock_timestamp()
			WHERE id = $1 AND lease_token = $2
		`, f.jobID, f.leaseToken, f.deadCode, f.errMessage)
	} else {
		delay := backoff.ComputeDelay(f.attemptsStarted)
		_, err = tx.Exec(ctx, `
			UPDATE jobs
			SET status = 'queued', run_at = clock_timestamp() + $3::interval,
			    lease_token = NULL, worker_id = NULL, lease_expires_at = NULL,
			    last_error_code = $4, last_error_message = $5, last_error_at = clock_timestamp(),
			    updated_at = clock_timestamp()
			WHERE id = $1 AND lease_token = $2
		`, f.jobID, f.leaseToken, fmt.Sprintf("%d milliseconds", delay.Milliseconds()), f.attemptCode, f.errMessage)
	}
	if err != nil {
		return dead, fmt.Errorf("settle job: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		UPDATE job_attempts
		SET finished_at = clock_timestamp(), outcome = $4, error_code = $5, error_message = $6, duration_ms = $7
		WHERE job_id = $1 AND replay_generation = $2 AND attempt_number = $3 AND lease_token = $8
	`, f.jobID, f.replayGen, f.attemptsStarted, f.attemptOutcome, f.attemptCode, f.errMessage, f.durationMs, f.leaseToken); err != nil {
		return dead, fmt.Errorf("close attempt: %w", err)
	}

	if dead {
		if _, err = tx.Exec(ctx, `
			UPDATE tenant_usage
			SET outstanding_jobs = GREATEST(0, outstanding_jobs - 1), updated_at = clock_timestamp()
			WHERE tenant_id = $1
		`, f.tenantID); err != nil {
			return dead, fmt.Errorf("decrement usage: %w", err)
		}
	}

	if _, err = tx.Exec(ctx, `
		UPDATE tenant_execution_slots
		SET current_job_id = NULL, lease_token = NULL, updated_at = clock_timestamp()
		WHERE tenant_id = $1 AND current_job_id = $2 AND lease_token = $3
	`, f.tenantID, f.jobID, f.leaseToken); err != nil {
		return dead, fmt.Errorf("release slot: %w", err)
	}
	return dead, nil
}

func (r *JobRepo) CompleteJobFailure(ctx context.Context, tenantID, jobID, leaseToken uuid.UUID, errVal error, backoffPolicy *domain.BackoffPolicy, durationMs int64) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var maxAttempts, attemptsStarted, replayGen int
	err = tx.QueryRow(ctx, `
		SELECT max_attempts, attempts_started, replay_generation
		FROM jobs
		WHERE id = $1 AND status = 'running' AND lease_token = $2
		FOR UPDATE
	`, jobID, leaseToken).Scan(&maxAttempts, &attemptsStarted, &replayGen)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrLeaseLost
		}
		return err
	}

	var permErr domain.PermanentError
	isPermanent := errors.As(errVal, &permErr)
	exhausted := attemptsStarted >= maxAttempts

	msg := domain.RedactSecrets(errVal.Error())
	if len(msg) > 2048 {
		msg = msg[:2048]
	}

	f := attemptFailure{
		jobID: jobID, tenantID: tenantID, leaseToken: leaseToken,
		replayGen: replayGen, attemptsStarted: attemptsStarted, max: maxAttempts,
		forceDead: isPermanent, errMessage: msg, durationMs: &durationMs,
		attemptOutcome: domain.OutcomeRetryableError, attemptCode: "handler_error", deadCode: "handler_error",
	}
	switch {
	case isPermanent:
		f.attemptOutcome, f.attemptCode, f.deadCode = domain.OutcomePermanentError, "permanent_error", "permanent_error"
	case exhausted:
		f.attemptCode, f.deadCode = "retries_exhausted", "retries_exhausted"
	}

	if _, err := settleFailedAttempt(ctx, tx, f, backoffPolicy); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *JobRepo) CompleteJobCancelled(ctx context.Context, tenantID, jobID, leaseToken uuid.UUID, durationMs int64) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var replayGen, attemptNum int
	err = tx.QueryRow(ctx, `
		UPDATE jobs
		SET status = 'cancelled',
		    completed_at = clock_timestamp(),
		    lease_token = NULL,
		    worker_id = NULL,
		    lease_expires_at = NULL,
		    updated_at = clock_timestamp()
		WHERE id = $1 AND status = 'running' AND lease_token = $2
		RETURNING replay_generation, attempts_started
	`, jobID, leaseToken).Scan(&replayGen, &attemptNum)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrLeaseLost
		}
		return err
	}

	outcome := domain.OutcomeCancelled
	_, err = tx.Exec(ctx, `
		UPDATE job_attempts
		SET finished_at = clock_timestamp(),
		    outcome = $4,
		    duration_ms = $5
		WHERE job_id = $1 AND replay_generation = $2 AND attempt_number = $3 AND lease_token = $6
	`, jobID, replayGen, attemptNum, outcome, durationMs, leaseToken)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		UPDATE tenant_execution_slots
		SET current_job_id = NULL, lease_token = NULL, updated_at = clock_timestamp()
		WHERE tenant_id = $1 AND current_job_id = $2 AND lease_token = $3
	`, tenantID, jobID, leaseToken)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		UPDATE tenant_usage
		SET outstanding_jobs = GREATEST(0, outstanding_jobs - 1), updated_at = clock_timestamp()
		WHERE tenant_id = $1
	`, tenantID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// RecoverExpiredLeases finds running jobs whose lease has expired (the worker crashed, hung or was
// partitioned) and requeues them with backoff, or marks them dead once attempts are exhausted.
//
// Each job is recovered in its OWN transaction. Doing the whole batch in one transaction (as an
// earlier version did) meant a single failing row aborted the Postgres transaction and silently
// rolled back every other recovery in the batch, on every tick, forever. Now a poison row is
// reported in the returned error while all healthy rows are still recovered.
func (r *JobRepo) RecoverExpiredLeases(ctx context.Context, batchSize int, backoffPolicy *domain.BackoffPolicy) (int, error) {
	if batchSize <= 0 {
		batchSize = 20
	}

	rows, err := r.db.Pool.Query(ctx, `
		SELECT id FROM jobs
		WHERE status = 'running' AND lease_expires_at <= clock_timestamp()
		ORDER BY lease_expires_at ASC
		LIMIT $1
	`, batchSize)
	if err != nil {
		return 0, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	recovered := 0
	var errs []error
	for _, id := range ids {
		ok, err := r.recoverOne(ctx, id, backoffPolicy)
		if err != nil {
			errs = append(errs, fmt.Errorf("job %s: %w", id, err))
			continue
		}
		if ok {
			recovered++
		}
	}
	return recovered, errors.Join(errs...)
}

// recoverOne recovers a single expired job. It re-checks expiry under the row lock, so a worker
// that renewed (or completed) at the last moment wins and the job is left alone (ok == false).
func (r *JobRepo) recoverOne(ctx context.Context, id uuid.UUID, backoff *domain.BackoffPolicy) (ok bool, err error) {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	f := attemptFailure{
		jobID: id, forceDead: false,
		attemptOutcome: domain.OutcomeLeaseExpired,
		attemptCode:    "worker_lease_expired",
		deadCode:       "retries_exhausted_lease_expired",
		errMessage:     "worker lease expired before completion",
	}
	err = tx.QueryRow(ctx, `
		SELECT tenant_id, lease_token, attempts_started, max_attempts, replay_generation
		FROM jobs
		WHERE id = $1 AND status = 'running' AND lease_expires_at <= clock_timestamp()
		FOR UPDATE SKIP LOCKED
	`, id).Scan(&f.tenantID, &f.leaseToken, &f.attemptsStarted, &f.max, &f.replayGen)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // renewed, completed or being handled by someone else
	}
	if err != nil {
		return false, err
	}

	if _, err := settleFailedAttempt(ctx, tx, f, backoff); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (r *JobRepo) CancelJob(ctx context.Context, tenantID, jobID uuid.UUID) (*domain.Job, error) {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Try cancelling queued job first
	tag, err := tx.Exec(ctx, `
		UPDATE jobs
		SET status = 'cancelled',
		    completed_at = clock_timestamp(),
		    updated_at = clock_timestamp()
		WHERE id = $1 AND tenant_id = $2 AND status = 'queued'
	`, jobID, tenantID)
	if err != nil {
		return nil, err
	}

	if tag.RowsAffected() > 0 {
		_, err = tx.Exec(ctx, `
			UPDATE tenant_usage
			SET outstanding_jobs = GREATEST(0, outstanding_jobs - 1), updated_at = clock_timestamp()
			WHERE tenant_id = $1
		`, tenantID)
		if err != nil {
			return nil, err
		}

		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return r.GetJob(ctx, tenantID, jobID)
	}

	// If running, set cancellation_requested_at
	tagRunning, err := tx.Exec(ctx, `
		UPDATE jobs
		SET cancellation_requested_at = COALESCE(cancellation_requested_at, clock_timestamp()),
		    updated_at = clock_timestamp()
		WHERE id = $1 AND tenant_id = $2 AND status = 'running'
	`, jobID, tenantID)
	if err != nil {
		return nil, err
	}

	if tagRunning.RowsAffected() > 0 {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return r.GetJob(ctx, tenantID, jobID)
	}

	// Reread to see if job exists or is terminal
	job, err := r.GetJob(ctx, tenantID, jobID)
	if err != nil {
		return nil, err
	}

	return job, domain.ErrInvalidState
}

func (r *JobRepo) ReplayDeadJob(ctx context.Context, tenantID, jobID uuid.UUID, actorKeyID *uuid.UUID, reason string) (*domain.Job, error) {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Check pending quota first
	var maxPending int
	var outstanding int64
	err = tx.QueryRow(ctx, `
		SELECT l.max_pending_jobs, u.outstanding_jobs
		FROM tenant_limits l
		JOIN tenant_usage u ON u.tenant_id = l.tenant_id
		WHERE l.tenant_id = $1
		FOR UPDATE OF u
	`, tenantID).Scan(&maxPending, &outstanding)
	if err != nil {
		return nil, fmt.Errorf("failed to query tenant quota: %w", err)
	}

	if outstanding >= int64(maxPending) {
		return nil, domain.ErrQuotaExceeded
	}

	tag, err := tx.Exec(ctx, `
		UPDATE jobs
		SET status = 'queued',
		    run_at = clock_timestamp(),
		    replay_generation = replay_generation + 1,
		    attempts_started = 0,
		    completed_at = NULL,
		    last_error_code = NULL,
		    last_error_message = NULL,
		    last_error_at = NULL,
		    cancellation_requested_at = NULL,
		    updated_at = clock_timestamp()
		WHERE id = $1 AND tenant_id = $2 AND status = 'dead'
	`, jobID, tenantID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		// Verify if job exists
		_, errCheck := r.GetJob(ctx, tenantID, jobID)
		if errCheck != nil {
			return nil, errCheck
		}
		return nil, domain.ErrInvalidState
	}

	_, err = tx.Exec(ctx, `
		UPDATE tenant_usage
		SET outstanding_jobs = outstanding_jobs + 1, updated_at = clock_timestamp()
		WHERE tenant_id = $1
	`, tenantID)
	if err != nil {
		return nil, err
	}

	auditID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO job_audit_events (
			id, tenant_id, job_id, actor_api_key_id, event_type, reason, created_at
		) VALUES (
			$1, $2, $3, $4, 'job_replayed', $5, clock_timestamp()
		)
	`, auditID, tenantID, jobID, actorKeyID, reason)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return r.GetJob(ctx, tenantID, jobID)
}

func (r *JobRepo) GetQueueStats(ctx context.Context, tenantID uuid.UUID, queueName string) (map[string]int64, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT status, count(*)
		FROM jobs
		WHERE tenant_id = $1 AND queue_name = $2
		GROUP BY status
	`, tenantID, queueName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := map[string]int64{
		"queued":    0,
		"running":   0,
		"succeeded": 0,
		"dead":      0,
		"cancelled": 0,
	}

	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err == nil {
			stats[status] = count
		}
	}

	return stats, rows.Err()
}
