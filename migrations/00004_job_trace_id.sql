-- +goose Up
-- Correlation id carried from the API request, through the database, into worker logs, so one job's
-- whole life can be found with a single grep. W3C trace-context compatible (32 hex chars), so an
-- OpenTelemetry exporter can adopt it later without a schema change.
-- Adding a column with a constant default is a metadata-only change in PostgreSQL 11+ (no rewrite).
ALTER TABLE jobs ADD COLUMN trace_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE jobs DROP COLUMN trace_id;
