-- +goose NO TRANSACTION
-- +goose Up
-- Retention deletes terminal jobs older than a cutoff, oldest first. Without this index every
-- retention pass was a sequential scan of the whole jobs table: measured 2,000,000 rows,
-- 48,812 buffers (~400 MB of reads) per pass in steady state, when nothing was eligible to delete.
-- Partial: only terminal rows, which is the only set retention ever looks at.
CREATE INDEX CONCURRENTLY IF NOT EXISTS jobs_terminal_completed_idx
    ON jobs (completed_at)
    WHERE status IN ('succeeded', 'dead', 'cancelled');

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS jobs_terminal_completed_idx;
