package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type RetentionRepo struct {
	db *DB
}

func NewRetentionRepo(db *DB) *RetentionRepo {
	return &RetentionRepo{db: db}
}

type RetentionCleanupResult struct {
	ExpiredIdempotencyKeysDeleted int64
	TerminalJobsDeleted           int64
	StaleWorkersDeleted           int64
}

// terminalStatuses must match the predicate of jobs_terminal_completed_idx.
const deleteTerminalBatchSQL = `
WITH doomed AS (
	SELECT id FROM jobs
	WHERE status IN ('succeeded', 'dead', 'cancelled')
	  AND completed_at <= statement_timestamp() - $1::interval
	ORDER BY completed_at
	LIMIT $2
	FOR UPDATE SKIP LOCKED
), a AS (DELETE FROM job_attempts      WHERE job_id IN (SELECT id FROM doomed)),
   e AS (DELETE FROM job_audit_events  WHERE job_id IN (SELECT id FROM doomed)),
   k AS (DELETE FROM idempotency_keys  WHERE job_id IN (SELECT id FROM doomed))
DELETE FROM jobs WHERE id IN (SELECT id FROM doomed)`

// maxRetentionRounds bounds one CleanupExpired call so a huge backlog cannot monopolise the
// database: it is resumed by the next tick.
const maxRetentionRounds = 200

// CleanupExpired deletes expired idempotency keys, terminal jobs older than terminalRetention
// (with their attempts, audit events and idempotency keys) and stale worker registrations.
//
// It keeps deleting batch after batch until the backlog is gone, the context ends, or
// maxRetentionRounds is reached. An earlier version deleted ONE batch of 100 per hourly tick, a
// ceiling of 2,400 jobs/day: any real workload grew the table (and the disk) without bound.
//
// Each batch is one atomic statement. If a batch fails (say one poisoned row), the rows of that
// batch are retried one at a time so a single bad row cannot stall retention; every failure is
// returned (errors.Join), never swallowed.
func (r *RetentionRepo) CleanupExpired(ctx context.Context, terminalRetention time.Duration, batchSize int) (*RetentionCleanupResult, error) {
	if batchSize <= 0 {
		batchSize = 1000
	}
	result := &RetentionCleanupResult{}
	var errs []error
	interval := fmt.Sprintf("%d seconds", int(terminalRetention.Seconds()))

	// 1. expired idempotency keys
	if tag, err := r.db.Pool.Exec(ctx, `DELETE FROM idempotency_keys WHERE expires_at <= statement_timestamp()`); err != nil {
		errs = append(errs, fmt.Errorf("delete expired idempotency keys: %w", err))
	} else {
		result.ExpiredIdempotencyKeysDeleted = tag.RowsAffected()
	}

	// 2. terminal jobs, oldest first, until caught up
	for round := 0; round < maxRetentionRounds && ctx.Err() == nil; round++ {
		tag, err := r.db.Pool.Exec(ctx, deleteTerminalBatchSQL, interval, batchSize)
		if err == nil {
			n := tag.RowsAffected()
			result.TerminalJobsDeleted += n
			if n < int64(batchSize) {
				break // caught up
			}
			continue
		}
		errs = append(errs, fmt.Errorf("delete terminal job batch: %w", err))
		n, rowErrs := r.deleteTerminalOneByOne(ctx, interval, batchSize)
		result.TerminalJobsDeleted += n
		errs = append(errs, rowErrs...)
		if n == 0 {
			break // nothing could be deleted; do not spin on a persistent failure
		}
	}

	// 3. stale worker registrations
	if tag, err := r.db.Pool.Exec(ctx, `DELETE FROM worker_instances WHERE last_seen_at <= statement_timestamp() - interval '7 days'`); err != nil {
		errs = append(errs, fmt.Errorf("delete stale workers: %w", err))
	} else {
		result.StaleWorkersDeleted = tag.RowsAffected()
	}
	return result, errors.Join(errs...)
}

// deleteTerminalOneByOne is the isolation fallback for a failed batch.
func (r *RetentionRepo) deleteTerminalOneByOne(ctx context.Context, interval string, limit int) (int64, []error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT id FROM jobs
		WHERE status IN ('succeeded', 'dead', 'cancelled') AND completed_at <= statement_timestamp() - $1::interval
		ORDER BY completed_at LIMIT $2`, interval, limit)
	if err != nil {
		return 0, []error{err}
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, []error{err}
		}
		ids = append(ids, id)
	}
	rows.Close()
	var deleted int64
	var errs []error
	for _, id := range ids {
		tag, err := r.db.Pool.Exec(ctx, `
			WITH a AS (DELETE FROM job_attempts WHERE job_id = $1),
			     e AS (DELETE FROM job_audit_events WHERE job_id = $1),
			     k AS (DELETE FROM idempotency_keys WHERE job_id = $1)
			DELETE FROM jobs WHERE id = $1 AND status IN ('succeeded', 'dead', 'cancelled')`, id)
		if err != nil {
			errs = append(errs, fmt.Errorf("job %s: %w", id, err))
			continue
		}
		deleted += tag.RowsAffected()
	}
	return deleted, errs
}
