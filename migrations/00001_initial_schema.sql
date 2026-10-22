-- +goose Up
-- SQL schema for QueueForge v1

CREATE TABLE tenants (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    status text NOT NULL CHECK (status IN ('active', 'suspended')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE api_keys (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    name text NOT NULL,
    secret_hash bytea NOT NULL UNIQUE,
    display_prefix text NOT NULL,
    permissions text[] NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    expires_at timestamptz,
    revoked_at timestamptz,
    CHECK (cardinality(permissions) > 0)
);
CREATE INDEX api_keys_tenant_idx ON api_keys(tenant_id);

CREATE TABLE tenant_limits (
    tenant_id uuid PRIMARY KEY REFERENCES tenants(id) ON DELETE RESTRICT,
    enqueue_rate_per_second numeric(12,4) NOT NULL DEFAULT 60 CHECK (enqueue_rate_per_second > 0),
    enqueue_burst_capacity numeric(12,4) NOT NULL DEFAULT 120 CHECK (enqueue_burst_capacity > 0),
    max_pending_jobs integer NOT NULL DEFAULT 10000 CHECK (max_pending_jobs > 0),
    max_concurrent_jobs integer NOT NULL DEFAULT 4 CHECK (max_concurrent_jobs > 0),
    max_schedules integer NOT NULL DEFAULT 100 CHECK (max_schedules > 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE tenant_rate_buckets (
    tenant_id uuid PRIMARY KEY REFERENCES tenants(id) ON DELETE RESTRICT,
    tokens numeric(14,6) NOT NULL CHECK (tokens >= 0),
    last_refill_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE tenant_usage (
    tenant_id uuid PRIMARY KEY REFERENCES tenants(id) ON DELETE RESTRICT,
    outstanding_jobs bigint NOT NULL DEFAULT 0 CHECK (outstanding_jobs >= 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE tenant_execution_slots (
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    slot_no integer NOT NULL CHECK (slot_no >= 0),
    current_job_id uuid,
    lease_token uuid,
    enabled boolean NOT NULL DEFAULT true,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, slot_no),
    CHECK ((current_job_id IS NULL) = (lease_token IS NULL))
);
CREATE INDEX tenant_execution_slots_free_idx
    ON tenant_execution_slots(tenant_id, slot_no)
    WHERE current_job_id IS NULL AND enabled = true;

CREATE TABLE jobs (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    queue_name text NOT NULL DEFAULT 'default',
    task_type text NOT NULL,
    payload jsonb NOT NULL,
    result jsonb,
    status text NOT NULL CHECK (status IN ('queued','running','succeeded','dead','cancelled')),
    priority smallint NOT NULL DEFAULT 5 CHECK (priority BETWEEN 0 AND 9),
    run_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    completed_at timestamptz,
    attempts_started integer NOT NULL DEFAULT 0 CHECK (attempts_started >= 0),
    max_attempts integer NOT NULL DEFAULT 5 CHECK (max_attempts BETWEEN 1 AND 10),
    timeout_seconds integer NOT NULL DEFAULT 300 CHECK (timeout_seconds BETWEEN 1 AND 3600),
    lease_token uuid,
    worker_id uuid,
    lease_expires_at timestamptz,
    cancellation_requested_at timestamptz,
    last_error_code text,
    last_error_message text,
    last_error_at timestamptz,
    schedule_id uuid,
    scheduled_for timestamptz,
    replay_generation integer NOT NULL DEFAULT 0 CHECK (replay_generation >= 0),
    CHECK (
        (status = 'running' AND lease_token IS NOT NULL AND worker_id IS NOT NULL AND lease_expires_at IS NOT NULL)
        OR
        (status <> 'running' AND lease_token IS NULL AND worker_id IS NULL AND lease_expires_at IS NULL)
    ),
    CHECK ((schedule_id IS NULL) = (scheduled_for IS NULL))
);

CREATE INDEX jobs_claim_idx
    ON jobs (priority DESC, run_at ASC, created_at ASC, id ASC)
    WHERE status = 'queued';

CREATE INDEX jobs_tenant_created_idx
    ON jobs (tenant_id, created_at DESC, id DESC);

CREATE INDEX jobs_tenant_status_idx
    ON jobs (tenant_id, status, created_at DESC);

CREATE INDEX jobs_running_lease_idx
    ON jobs (lease_expires_at ASC)
    WHERE status = 'running';

CREATE UNIQUE INDEX jobs_schedule_occurrence_uq
    ON jobs (schedule_id, scheduled_for)
    WHERE schedule_id IS NOT NULL;

CREATE TABLE job_attempts (
    id uuid PRIMARY KEY,
    job_id uuid NOT NULL REFERENCES jobs(id) ON DELETE RESTRICT,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    replay_generation integer NOT NULL DEFAULT 0,
    attempt_number integer NOT NULL CHECK (attempt_number >= 1),
    worker_id uuid,
    lease_token uuid NOT NULL,
    started_at timestamptz NOT NULL,
    finished_at timestamptz,
    outcome text CHECK (outcome IN ('succeeded','retryable_error','permanent_error','lease_expired','cancelled')),
    error_code text,
    error_message text,
    duration_ms bigint CHECK (duration_ms IS NULL OR duration_ms >= 0),
    UNIQUE (job_id, replay_generation, attempt_number)
);
CREATE INDEX job_attempts_job_idx ON job_attempts(job_id, replay_generation, attempt_number);
CREATE INDEX job_attempts_tenant_started_idx ON job_attempts(tenant_id, started_at DESC);

CREATE TABLE idempotency_keys (
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    idempotency_key text NOT NULL,
    request_hash bytea NOT NULL,
    job_id uuid NOT NULL REFERENCES jobs(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, idempotency_key)
);
CREATE INDEX idempotency_expiry_idx ON idempotency_keys(expires_at);

CREATE TABLE worker_instances (
    id uuid PRIMARY KEY,
    process_name text NOT NULL,
    hostname text,
    version text NOT NULL,
    queues text[] NOT NULL,
    task_types text[] NOT NULL,
    started_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    draining boolean NOT NULL DEFAULT false,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX worker_last_seen_idx ON worker_instances(last_seen_at);

CREATE TABLE job_schedules (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    name text NOT NULL,
    queue_name text NOT NULL DEFAULT 'default',
    task_type text NOT NULL,
    payload jsonb NOT NULL,
    cron_expression text NOT NULL,
    timezone text NOT NULL,
    priority smallint NOT NULL DEFAULT 5 CHECK (priority BETWEEN 0 AND 9),
    max_attempts integer NOT NULL DEFAULT 5 CHECK (max_attempts BETWEEN 1 AND 10),
    timeout_seconds integer NOT NULL DEFAULT 300 CHECK (timeout_seconds BETWEEN 1 AND 3600),
    misfire_policy text NOT NULL CHECK (misfire_policy IN ('skip','catch_up_one')),
    next_run_at timestamptz NOT NULL,
    last_scheduled_for timestamptz,
    pending_catch_up_at timestamptz,
    last_skipped_at timestamptz,
    paused boolean NOT NULL DEFAULT false,
    disabled_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);
CREATE INDEX job_schedules_due_idx ON job_schedules(next_run_at, id)
    WHERE paused = false AND disabled_at IS NULL;
CREATE INDEX job_schedules_catch_up_idx ON job_schedules(pending_catch_up_at, id)
    WHERE pending_catch_up_at IS NOT NULL AND paused = false AND disabled_at IS NULL;

ALTER TABLE jobs
    ADD CONSTRAINT jobs_schedule_fk
    FOREIGN KEY (schedule_id) REFERENCES job_schedules(id) ON DELETE RESTRICT;

CREATE TABLE job_audit_events (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    job_id uuid NOT NULL REFERENCES jobs(id) ON DELETE RESTRICT,
    actor_api_key_id uuid,
    event_type text NOT NULL,
    reason text,
    created_at timestamptz NOT NULL DEFAULT now(),
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX job_audit_job_idx ON job_audit_events(job_id, created_at);

-- +goose Down
DROP TABLE IF EXISTS job_audit_events;
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_schedule_fk;
DROP TABLE IF EXISTS job_schedules;
DROP TABLE IF EXISTS worker_instances;
DROP TABLE IF EXISTS idempotency_keys;
DROP TABLE IF EXISTS job_attempts;
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS tenant_execution_slots;
DROP TABLE IF EXISTS tenant_usage;
DROP TABLE IF EXISTS tenant_rate_buckets;
DROP TABLE IF EXISTS tenant_limits;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS tenants;
