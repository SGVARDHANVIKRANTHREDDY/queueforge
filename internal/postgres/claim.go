package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/domain"
	"github.com/google/uuid"
)

// claimBatchSQL leases up to $4 jobs in ONE statement.
//
// Pipeline (every step is a CTE of the same statement, so it is atomic):
//
//	cand      probe the claim index once per (queue, task type, priority) for the oldest due jobs
//	          (equality prefix + range on run_at: one index descent each). Cost is independent of how
//	          many UNRELATED rows are queued (other task types, delayed jobs); an earlier single-scan
//	          design walked all of them: 653 ms at 400k rows, now ~1 ms.
//	locked    lock those candidate rows (SKIP LOCKED: concurrent workers take different jobs)
//	ranked    number each tenant's candidate jobs in priority order (1st, 2nd, ...)
//	free      lock and number each tenant's FREE execution slots (SKIP LOCKED: a slot another worker
//	          is claiming simply counts as not free; no retry loop needed)
//	picked    pair the Nth job of a tenant with its Nth free slot, highest priority first, LIMIT $4.
//	          A tenant with 1 free slot and 5 queued jobs contributes exactly its best job; its other
//	          jobs are not picked, so a busy tenant can never starve the others.
//	upd_*     mark jobs running (new lease token), reserve the slots, open the attempts.
//
// The final row of the result is a 'meta' row reporting which tenants have MORE candidate jobs than
// free slots (they are exhausted: the caller excludes them so the next round looks past them
// instead of drowning in one noisy tenant's backlog) and how many candidates were seen.
//
// statement_timestamp(), not clock_timestamp(), bounds run_at: it is the same DATABASE clock but is
// STABLE, so PostgreSQL can use it as an index range bound. A volatile function can only ever be a
// row-by-row filter, which is what made the old query read every delayed job.
const claimBatchSQL = `
WITH cand AS MATERIALIZED (
	SELECT c.id
	FROM unnest($1::text[]) AS q(queue_name)
	CROSS JOIN unnest($2::text[]) AS t(task_type)
	CROSS JOIN generate_series(0, 9) AS p(priority)
	CROSS JOIN LATERAL (
		SELECT j.id, j.priority, j.run_at, j.created_at
		FROM jobs j
		WHERE j.status = 'queued' AND j.queue_name = q.queue_name AND j.task_type = t.task_type
		  AND j.priority = p.priority AND j.run_at <= statement_timestamp()
		  AND j.tenant_id::text <> ALL($3::text[])
		ORDER BY j.run_at, j.created_at, j.id
		LIMIT $7
	) c
	ORDER BY c.priority DESC, c.run_at, c.created_at, c.id
	LIMIT $7
), locked AS MATERIALIZED (
	SELECT j.id, j.tenant_id, j.queue_name, j.task_type, j.payload, j.priority, j.run_at, j.created_at,
	       j.attempts_started, j.max_attempts, j.timeout_seconds, j.replay_generation, j.trace_id
	FROM jobs j
	WHERE j.id IN (SELECT id FROM cand) AND j.status = 'queued'
	ORDER BY j.priority DESC, j.run_at, j.created_at, j.id
	FOR UPDATE OF j SKIP LOCKED
), ranked AS MATERIALIZED (
	SELECT l.*, row_number() OVER (PARTITION BY l.tenant_id ORDER BY l.priority DESC, l.run_at, l.created_at, l.id) AS trank
	FROM locked l
), free AS MATERIALIZED (
	SELECT s.tenant_id, s.slot_no, row_number() OVER (PARTITION BY s.tenant_id ORDER BY s.slot_no) AS srank
	FROM (
		SELECT tenant_id, slot_no FROM tenant_execution_slots
		WHERE tenant_id IN (SELECT tenant_id FROM locked) AND current_job_id IS NULL AND enabled = true
		ORDER BY tenant_id, slot_no
		FOR UPDATE SKIP LOCKED
	) s
), picked AS MATERIALIZED (
	SELECT r.*, f.slot_no, gen_random_uuid() AS lease_token, gen_random_uuid() AS attempt_id
	FROM ranked r JOIN free f ON f.tenant_id = r.tenant_id AND f.srank = r.trank
	ORDER BY r.priority DESC, r.run_at, r.created_at, r.id
	LIMIT $4
), upd_jobs AS (
	UPDATE jobs j
	SET status = 'running', attempts_started = j.attempts_started + 1, worker_id = $5,
	    lease_token = p.lease_token, lease_expires_at = clock_timestamp() + $6::interval,
	    started_at = COALESCE(j.started_at, clock_timestamp()), updated_at = clock_timestamp()
	FROM picked p WHERE j.id = p.id
	RETURNING j.id, j.lease_expires_at
), upd_slots AS (
	UPDATE tenant_execution_slots s
	SET current_job_id = p.id, lease_token = p.lease_token, updated_at = clock_timestamp()
	FROM picked p WHERE s.tenant_id = p.tenant_id AND s.slot_no = p.slot_no
	RETURNING 1
), ins_attempts AS (
	INSERT INTO job_attempts (id, job_id, tenant_id, replay_generation, attempt_number, worker_id, lease_token, started_at)
	SELECT p.attempt_id, p.id, p.tenant_id, p.replay_generation, p.attempts_started + 1, $5, p.lease_token, clock_timestamp()
	FROM picked p
	RETURNING 1
)
SELECT 'job'::text AS kind, p.id, p.tenant_id, p.queue_name, p.task_type, p.payload,
       p.attempts_started + 1 AS attempts_started, p.max_attempts, p.timeout_seconds, p.replay_generation,
       p.run_at, p.trace_id, p.slot_no, p.lease_token, u.lease_expires_at,
       GREATEST(0, EXTRACT(EPOCH FROM (clock_timestamp() - p.run_at)))::float8 AS queue_wait,
       NULL::text[] AS starved, 0 AS ncand
FROM picked p JOIN upd_jobs u ON u.id = p.id
UNION ALL
SELECT 'meta', NULL::uuid, NULL::uuid, NULL, NULL, NULL::jsonb, NULL::int, NULL::int, NULL::int, NULL::int,
       NULL::timestamptz, NULL, NULL::int, NULL::uuid, NULL::timestamptz, NULL::float8,
       (SELECT array_agg(DISTINCT r.tenant_id::text) FROM ranked r
         WHERE r.trank > (SELECT count(*) FROM free f WHERE f.tenant_id = r.tenant_id)),
       (SELECT count(*)::int FROM cand)
`

// maxClaimRounds bounds how many times one ClaimJobs call re-runs the statement. A round is needed
// only when the top candidates all belong to tenants without a free slot (they are then excluded
// and the next-best tenants are tried), or when every candidate was taken by a concurrent worker.
const maxClaimRounds = 8

// ClaimJob leases a single job. See ClaimJobs.
func (r *JobRepo) ClaimJob(ctx context.Context, workerID uuid.UUID, allowedQueues, supportedTaskTypes []string, leaseDuration time.Duration) (*ClaimResult, error) {
	res, err := r.ClaimJobs(ctx, workerID, allowedQueues, supportedTaskTypes, leaseDuration, 1)
	if err != nil || len(res) == 0 {
		return nil, err
	}
	return res[0], nil
}

// ClaimJobs leases up to max jobs in one transaction (usually one statement). Jobs are chosen by
// priority then FIFO across all tenants, subject to each tenant's free execution slots. It returns
// fewer than max jobs when nothing more is claimable.
func (r *JobRepo) ClaimJobs(ctx context.Context, workerID uuid.UUID, allowedQueues, supportedTaskTypes []string, leaseDuration time.Duration, max int) ([]*ClaimResult, error) {
	if len(allowedQueues) == 0 || len(supportedTaskTypes) == 0 || max < 1 {
		return nil, nil
	}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin claim transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	leaseText := fmt.Sprintf("%d seconds", int(leaseDuration.Seconds()))
	skipped := []string{}
	var out []*ClaimResult
	emptyRaces := 0

	for round := 0; round < maxClaimRounds && len(out) < max; round++ {
		need := max - len(out)
		pool := 4 * need // candidates to lock: slack for tenants whose jobs end up not eligible
		if pool < 16 {
			pool = 16
		}
		rows, err := tx.Query(ctx, claimBatchSQL, allowedQueues, supportedTaskTypes, skipped, need, workerID, leaseText, pool)
		if err != nil {
			return nil, fmt.Errorf("claim: %w", err)
		}
		var picked []*ClaimResult
		var starved []string
		ncand := 0
		for rows.Next() {
			var (
				kind                                      string
				id, tenantID, leaseToken                  *uuid.UUID
				queue, taskType, trace                    *string
				payload                                   []byte
				attempts, maxAttempts, timeoutSec, replay *int32
				slot, nc                                  *int32
				runAt, leaseExp                           *time.Time
				qwait                                     *float64
				st                                        []string
			)
			if err := rows.Scan(&kind, &id, &tenantID, &queue, &taskType, &payload, &attempts, &maxAttempts,
				&timeoutSec, &replay, &runAt, &trace, &slot, &leaseToken, &leaseExp, &qwait, &st, &nc); err != nil {
				rows.Close()
				return nil, fmt.Errorf("claim scan: %w", err)
			}
			if kind == "meta" {
				starved = st
				if nc != nil {
					ncand = int(*nc)
				}
				continue
			}
			wid := workerID
			picked = append(picked, &ClaimResult{
				Job: &domain.Job{
					ID: *id, TenantID: *tenantID, QueueName: *queue, TaskType: *taskType, Payload: payload,
					Status: domain.StatusRunning, AttemptsStarted: int(*attempts), MaxAttempts: int(*maxAttempts),
					TimeoutSeconds: int(*timeoutSec), ReplayGeneration: int(*replay), RunAt: *runAt, TraceID: *trace,
					WorkerID: &wid, LeaseToken: leaseToken, LeaseExpiresAt: leaseExp,
				},
				LeaseToken: *leaseToken, SlotNo: int(*slot), QueueWaitSeconds: *qwait,
			})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("claim: %w", err)
		}

		out = append(out, picked...)
		switch {
		case len(out) >= max:
		case ncand == 0:
			round = maxClaimRounds // nothing runnable for us at all
		case len(starved) > 0:
			// Some tenants have more queued candidates than free slots. Exclude them and look past
			// them for other tenants' jobs.
			skipped = append(skipped, starved...)
		case len(picked) == 0:
			// Candidates existed but a concurrent worker took them first; try again a couple of times.
			if emptyRaces++; emptyRaces > 2 {
				round = maxClaimRounds
			}
		default:
			round = maxClaimRounds // got some jobs and nothing more is claimable right now
		}
	}

	if len(out) == 0 {
		return nil, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit claim transaction: %w", err)
	}
	return out, nil
}

var _ = errors.Is // keep import if unused after edits
