-- +goose Up
-- Append-only record of control-plane actions (who did what to which resource, and when).
-- The enqueue hot path is deliberately NOT audited (it has its own durable record: the job row).
CREATE TABLE audit_log (
    id          BIGSERIAL PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    tenant_id   UUID,                       -- NULL for operator actions that span tenants
    actor_type  TEXT NOT NULL CHECK (actor_type IN ('api_key', 'operator')),
    actor_id    TEXT NOT NULL,              -- api key id, or the operator name for the admin CLI
    action      TEXT NOT NULL,              -- e.g. "POST /v1/jobs/{id}/cancel", "create_api_key"
    target_id   TEXT,
    status_code INTEGER,
    request_id  TEXT,
    remote_addr TEXT,
    details     JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX audit_log_tenant_time_idx ON audit_log (tenant_id, occurred_at DESC);
CREATE INDEX audit_log_time_idx ON audit_log (occurred_at DESC);

-- Tamper resistance: the application role may append, never rewrite history.
-- +goose StatementBegin
CREATE FUNCTION audit_log_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is append-only';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER audit_log_no_update BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_immutable();

-- +goose Down
DROP TRIGGER IF EXISTS audit_log_no_update ON audit_log;
DROP FUNCTION IF EXISTS audit_log_immutable();
DROP TABLE IF EXISTS audit_log;
