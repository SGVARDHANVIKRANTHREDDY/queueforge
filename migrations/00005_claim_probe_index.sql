-- +goose NO TRANSACTION
-- +goose Up
-- The claim query probes the queue once per (queue_name, task_type, priority) instead of scanning
-- the global (priority, run_at, ...) order and filtering. With the old index, a worker that does not
-- handle the head-of-line task type (or whose head-of-line jobs are delayed) had to walk every such
-- row on every claim: measured 7.6 ms at 10k, 132 ms at 100k, 653 ms at 400k unrelated queued jobs.
-- With equality on the first three columns, each probe is a single index descent.
--
-- CONCURRENTLY: does not block writers while building. (Requires NO TRANSACTION, above.)
CREATE INDEX CONCURRENTLY IF NOT EXISTS jobs_claim_probe_idx
    ON jobs (queue_name, task_type, priority, run_at ASC, created_at ASC, id ASC)
    WHERE status = 'queued';
DROP INDEX CONCURRENTLY IF EXISTS jobs_claim_idx;

-- +goose Down
CREATE INDEX CONCURRENTLY IF NOT EXISTS jobs_claim_idx
    ON jobs (priority DESC, run_at ASC, created_at ASC, id ASC)
    WHERE status = 'queued';
DROP INDEX CONCURRENTLY IF EXISTS jobs_claim_probe_idx;
