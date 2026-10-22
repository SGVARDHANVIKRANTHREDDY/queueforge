-- +goose Up
-- The queue tables are write-hot "churn" tables: rows are inserted, updated several times and then
-- stay as dead tuples. PostgreSQL's default autovacuum/autoanalyze trigger (20% / 10% of the table
-- changed) means that on a big table the planner statistics and dead-tuple cleanup lag far behind.
-- Measured effect of cold planner statistics on a 3,000-job backlog: 269 vs 682 jobs/s (2.5x).
-- Trigger much earlier so statistics stay fresh and bloat stays bounded.
ALTER TABLE jobs SET (
    autovacuum_vacuum_scale_factor = 0.02,
    autovacuum_analyze_scale_factor = 0.02,
    autovacuum_vacuum_cost_limit = 1000
);
ALTER TABLE job_attempts SET (
    autovacuum_vacuum_scale_factor = 0.05,
    autovacuum_analyze_scale_factor = 0.05
);
-- Tiny, extremely hot single-row-per-tenant tables: vacuum them aggressively by row count.
ALTER TABLE tenant_execution_slots SET (autovacuum_vacuum_scale_factor = 0, autovacuum_vacuum_threshold = 50, autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE tenant_rate_buckets   SET (autovacuum_vacuum_scale_factor = 0, autovacuum_vacuum_threshold = 50, autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);
ALTER TABLE tenant_usage          SET (autovacuum_vacuum_scale_factor = 0, autovacuum_vacuum_threshold = 50, autovacuum_analyze_scale_factor = 0, autovacuum_analyze_threshold = 50);

-- +goose Down
ALTER TABLE jobs RESET (autovacuum_vacuum_scale_factor, autovacuum_analyze_scale_factor, autovacuum_vacuum_cost_limit);
ALTER TABLE job_attempts RESET (autovacuum_vacuum_scale_factor, autovacuum_analyze_scale_factor);
ALTER TABLE tenant_execution_slots RESET (autovacuum_vacuum_scale_factor, autovacuum_vacuum_threshold, autovacuum_analyze_scale_factor, autovacuum_analyze_threshold);
ALTER TABLE tenant_rate_buckets RESET (autovacuum_vacuum_scale_factor, autovacuum_vacuum_threshold, autovacuum_analyze_scale_factor, autovacuum_analyze_threshold);
ALTER TABLE tenant_usage RESET (autovacuum_vacuum_scale_factor, autovacuum_vacuum_threshold, autovacuum_analyze_scale_factor, autovacuum_analyze_threshold);
