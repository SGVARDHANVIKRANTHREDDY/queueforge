# QueueForge — Technical Design Document (TDD)

**Document status:** Draft for implementation  
**Version:** 1.0  
**Date:** 16 October 2026  
**Companion document:** `QueueForge_PRD.md`  
**Architecture style:** Modular monolith + independently deployed worker processes  
**Initial datastore:** PostgreSQL 18.x (patched minor release pinned in deployment)  
**Primary language:** Go 1.27.x (pin the exact supported patch release in toolchain/CI)  

## 1. Purpose and design summary

This document defines a concrete implementation for QueueForge v1. The primary objective is to preserve durable state and correct lifecycle transitions while keeping the architecture small enough for one developer to build, understand, test, and operate.

The service is split into an HTTP API process, worker processes, and scheduler/recovery loops. These processes share one Go codebase and one PostgreSQL schema. PostgreSQL is the authoritative source for jobs, idempotency, schedules, rate-limit state, worker leases, attempt history, and tenant limits. Multiple API, worker, and scheduler replicas may run concurrently.

The core concurrency primitive is a short transaction using PostgreSQL row-level locks and `FOR UPDATE SKIP LOCKED` for queue-like claiming. PostgreSQL explicitly cautions that `SKIP LOCKED` produces an inconsistent view unsuitable for general-purpose reads but can reduce lock contention for multiple consumers of queue-like tables; QueueForge uses it only for claim/recovery coordination, not normal user-facing reads. [R1]

No database transaction remains open while user handler code executes. Job execution is at-least-once. A random lease token fences stale state updates, but it cannot stop a stale process from finishing an external side effect after its lease has expired. Consumer handlers should therefore use destination-supported idempotency keys or other domain-level deduplication when the side effect must not be duplicated.

## 2. Architecture overview

### 2.1 Logical component diagram

```text
Clients / dashboard
        | HTTPS REST
        v
     API (Go) ----------> Prometheus scrape
        |
        v
   PostgreSQL <---------- Worker replicas (Go)
        ^                  registered handlers
        |
 Scheduler + recovery loops
 due cron / expired leases
```

### 2.2 Process roles

**API process**
- Serves versioned REST endpoints.
- Authenticates API keys and resolves tenant + permissions.
- Validates payloads and enforces quotas.
- Creates jobs and idempotency records in one transaction.
- Provides read APIs, schedule management, cancellation, dead-letter operations, health/readiness, and protected metrics.

**Worker process**
- Registers a finite set of trusted handler functions.
- Creates/renews a worker registry heartbeat.
- Polls for eligible jobs and claims work using database transactions.
- Executes handler code outside transactions.
- Reports success/failure conditionally on its current lease token.
- Renews job lease and observes cancellation requests while the handler is running.
- Shuts down gracefully with a timeout; abandoned leases recover if graceful shutdown fails.

**Scheduler loop**
- Finds due recurring schedule records.
- Uses row locks to prevent two scheduler instances from advancing one schedule simultaneously.
- Generates one job per occurrence and advances `next_run_at` in one transaction.
- Uses a database uniqueness constraint as a second line of defense against duplicate occurrence creation.

**Recovery loop**
- Finds running jobs with expired leases.
- Locks a bounded batch with `SKIP LOCKED`.
- Marks the previous attempt lease-expired, schedules a retry or marks the job dead, releases the tenant concurrency slot, and clears lease fields atomically.

**Retention loop**
- Deletes/archives expired terminal job records and attempt history according to a documented retention policy.
- Never deletes queued/running jobs or idempotency rows before their configured expiry.
- Uses bounded batches to avoid long transactions and table bloat.

### 2.3 Why modular monolith + workers

For v1, separate service codebases and network protocols between API, worker, scheduler, and recovery service would increase complexity without proving a necessary benefit. They are separate processes where scaling or failure isolation matters, but share packages and schema. This keeps the worker deployment horizontally scalable while retaining a clear domain model.

### 2.4 Why PostgreSQL first

- Durable relational transactions for job creation, idempotency, attempts, and schedule occurrence generation.
- Row locking and `SKIP LOCKED` support multiple queue consumers.
- JSONB is sufficient for bounded JSON payloads/results.
- One required stateful dependency keeps local development and a cheap VPS simpler.
- The system can later add another queue backend only through an explicit ADR backed by benchmarks.

Redis is not required in v1. Do not introduce it solely as a résumé keyword. If benchmarks later show a measured limit, define the exact responsibility Redis would own, the source-of-truth boundary, failure semantics, reconciliation strategy, and operational cost before implementation.

## 3. Technology choices

| Area | Choice | Reason / constraint |
|---|---|---|
| Language/runtime | Go 1.27.x; pin exact patch | Worker concurrency, easy static binary deployment; verify supported patched version at build time. [R2] |
| HTTP router | `net/http` + `chi` v5 | Small routing/middleware layer; no large framework needed. |
| PostgreSQL driver | `pgx/v5` and `pgxpool` | Native PostgreSQL feature support, context-aware operations, explicit connection pooling. |
| SQL generation | Plain SQL first; optionally `sqlc` once queries stabilize | Queue logic is SQL-sensitive; query behavior must remain visible. Avoid hiding locking semantics. |
| Migrations | `goose` (`pressly/goose/v3`) | Versioned SQL migrations; no ad hoc startup DDL. |
| Cron parser | `robfig/cron/v3`, pinned | Five-field cron parsing and next-run calculation; timezone/DST behavior must be tested. |
| Logging | Standard library `log/slog` with JSON handler | Structured logs, low dependency count. |
| Metrics | Prometheus Go client | Standard scrape model and histograms/counters. |
| API description | OpenAPI 3.2.x-compatible schema/toolchain pinned in repo | Contract visible to clients and checked in CI. [R3] |
| Dashboard | React + TypeScript + Vite (or equivalent static SPA) | Separate from core queue; no secrets embedded in built assets. |
| Local infrastructure | Docker Compose + PostgreSQL; optional Prometheus/Grafana | Reproducible development and demo. |
| Load/chaos tools | Python scripts + k6 or custom concurrent generator | Easier to produce machine-readable experiments and reports. |
| CI | GitHub Actions | Test, lint, race checks, vulnerability scanning, container build, OpenAPI validation. |

Avoid adding dependencies until the first vertical slice works. Pin versions and commit the dependency lock/sum files.

## 4. Repository structure

Recommended monorepo layout:

```text
queueforge/
├── cmd/{api,worker,admin}/
├── internal/{config,httpapi,auth,domain,service,postgres,worker,scheduler,recovery,retention,observability}/
├── migrations/                 # versioned SQL migrations
├── api/openapi.yaml
├── web/                        # React + TypeScript dashboard
├── sdk/python/                 # submission client
├── examples/{echo-handler,placement-alert-bot}/
├── tests/{integration,load,chaos,fixtures}/
├── deploy/{docker-compose,prometheus,grafana}/
├── docs/{ADRs,runbooks,benchmark-reports}/
├── Makefile, Dockerfile, .env.example
├── .github/workflows/ci.yml
└── go.mod, go.sum, README.md
```

**Dependency rule:** `domain` must not import HTTP or PostgreSQL packages. HTTP handlers call services; services define transaction-level use cases; repositories implement data access. Worker handler code may depend on the public handler interface and domain error types but should not bypass lease checks to modify job state directly.

## 5. Configuration design

Use environment variables with a single validated configuration struct. Fail fast on missing required values or invalid relationships. Do not log secret values.

| Variable | Example/default | Secret? | Purpose |
|---|---|---|---|
| `QF_HTTP_ADDR` | `0.0.0.0:8080` | No | API bind address |
| `QF_DATABASE_URL` | `postgres://...` | Yes | PostgreSQL connection string |
| `QF_DATABASE_MAX_CONNS` | `20` | No | Pool cap per process; total replicas must fit DB budget |
| `QF_DATABASE_MIN_CONNS` | `2` | No | Pool warm minimum |
| `QF_DATABASE_TLS_MODE` | `verify-full` in public deployment | Indirectly sensitive | TLS policy; local Compose can use a documented exception |
| `QF_LOG_LEVEL` | `info` | No | Log level |
| `QF_LOG_FORMAT` | `json` | No | Structured output |
| `QF_METRICS_ADDR` | `127.0.0.1:9090` or same server protected path | No | Private metrics listener/address |
| `QF_DEFAULT_MAX_ATTEMPTS` | `5` | No | Default total attempts |
| `QF_DEFAULT_JOB_TIMEOUT_SECONDS` | `300` | No | Default handler timeout |
| `QF_MAX_JOB_TIMEOUT_SECONDS` | `3600` | No | Upper bound accepted for one attempt |
| `QF_MAX_SCHEDULE_HORIZON` | `365d` | No | Maximum allowed future due time |
| `QF_IDEMPOTENCY_TTL` | `24h` | No | Idempotency key retention |
| `QF_TERMINAL_JOB_RETENTION` | `720h` | No | Terminal job and child-history retention |
| `QF_ALLOWED_QUEUES` | `default, notifications, background, maintenance` | No | Global queue allowlist keeps routing/metrics cardinality bounded |
| `QF_ALLOWED_ORIGINS` | configured dashboard origin(s) | No | CORS allowlist; avoid wildcard on public deployment |
| `QF_RETRY_BASE_DELAY` | `1s` | No | Exponential retry base |
| `QF_RETRY_MAX_DELAY` | `15m` | No | Backoff cap |
| `QF_DEFAULT_LEASE_DURATION` | `60s` | No | Lease validity duration |
| `QF_LEASE_RENEW_INTERVAL` | `20s` | No | Job lease renewal cadence |
| `QF_WORKER_HEARTBEAT_INTERVAL` | `10s` | No | Worker registry heartbeat cadence |
| `QF_CLAIM_POLL_INTERVAL` | `250ms` | No | Idle poll interval; use backoff when empty/error |
| `QF_RECOVERY_INTERVAL` | `5s` | No | Expired lease scan cadence |
| `QF_SCHEDULER_INTERVAL` | `1s` | No | Due schedule scan cadence |
| `QF_SCHEDULE_MISFIRE_GRACE` | `30s` | No | Distinguishes normal scheduler lag from a true cron misfire |
| `QF_RETENTION_INTERVAL` | `1h` | No | Retention sweep cadence |
| `QF_MAX_REQUEST_BYTES` | `1048576` | No | HTTP body limit |
| `QF_MAX_PAYLOAD_BYTES` | `262144` | No | JSON payload limit |
| `QF_SHUTDOWN_TIMEOUT` | `30s` | No | Graceful shutdown cap |


Validation rules include: lease renewal interval substantially below lease duration; base retry delay <= max delay; max attempts within a bounded range; connection pool within database connection budget; request body limit >= payload limit plus JSON envelope overhead; and terminal-job retention >= idempotency-key TTL so an unexpired idempotency row cannot outlive its referenced job.

For multi-process deployments, remember that each process has its own pool. Total possible database connections are approximately `API replicas * API pool max + worker replicas * worker pool max + scheduler/recovery pools + operational headroom`. Document and monitor this budget.

## 6. API design

### 6.1 General conventions

- Base path `/v1`.
- JSON request and response payloads; UTF-8.
- Authentication header `Authorization: Bearer <api-key>`.
- `Idempotency-Key` header for enqueue requests; accept a bounded opaque string (for example 1–200 ASCII-safe characters), never a value with unbounded size.
- Every request receives or propagates `X-Request-ID` and returns it in the error envelope/log context.
- All time values are RFC 3339 timestamps with timezone offsets; database stores `timestamptz`, application uses UTC internally.
- Strict body-size limits and JSON decoding. Reject duplicate or unknown fields if the chosen decoder/validation layer can enforce it reliably.
- Public errors use stable codes; internal stack traces remain in restricted logs.
- List endpoints use cursor pagination; hard cap page size at 100.

### 6.2 Authentication/authorization flow

1. Extract the Bearer token.
2. Validate token syntax/size.
3. Hash the token and look up an active API-key record.
4. Resolve `tenant_id`, key ID, and permissions from the record.
5. Attach an immutable principal to request context.
6. Service functions accept principal/tenant context explicitly.
7. Repository queries include tenant predicates for tenant-owned resources.
8. Audit sensitive actions without recording the raw key.

Do not take `tenant_id` from a client request to establish ownership. Every object-level endpoint must scope the query by both object ID and tenant ID; missing/cross-tenant objects return the same `404` shape to avoid enumeration. This addresses an important API risk called out by OWASP's API security guidance. [R4]

### 6.3 Routes and authorization matrix

| Route | Permission | Notes |
|---|---|---|
| `POST /v1/jobs` | `jobs:write` | Atomic job + idempotency + quota transaction |
| `GET /v1/jobs/{id}` | `jobs:read` | Tenant scoped; safe response DTO |
| `GET /v1/jobs` | `jobs:read` | Filters and cursor pagination |
| `POST /v1/jobs/{id}/cancel` | `jobs:write` | Queued cancellation or running cancellation request |
| `POST /v1/schedules` | `schedules:write` | Recurring schedule create |
| `GET /v1/schedules` | `schedules:read` | Tenant scoped |
| `GET /v1/schedules/{id}` | `schedules:read` | Tenant scoped |
| `PATCH /v1/schedules/{id}` | `schedules:write` | Explicit allowed fields only; validate cron/timezone |
| `DELETE /v1/schedules/{id}` | `schedules:write` | Disable future schedule generation; do not delete generated jobs |
| `GET /v1/dead-letters` | `deadletters:read` | Bounded list of `dead` jobs |
| `POST /v1/jobs/{id}/replay` | `deadletters:write` | `dead` only; auditable state transition |
| `GET /v1/queues/{name}/stats` | `metrics:read` | Tenant-level statistics; use aggregation queries/cache if necessary |
| `GET /healthz` | no tenant auth | Only process liveness; no dependency details/secrets |
| `GET /readyz` | internal/protected | Checks API can use required database |
| `GET /metrics` | private scrape access | Separate listener or network restriction preferred |

Tenant/API-key creation and revocation are initially handled by a restricted CLI (for example `queueforge admin create-tenant`, `queueforge admin create-api-key`, `queueforge admin revoke-api-key`) using an operator-controlled database connection. These are not public tenant routes in v1. Do not expose an administrative HTTP endpoint or create a default production key.

Schedule creation must be concurrency-safe: in one short transaction, lock the tenant's `tenant_limits` row (or a dedicated schedule-usage row), count schedules where `disabled_at IS NULL` including paused schedules, reject if the count is at `max_schedules`, then insert the schedule. The tenant-level lock serializes concurrent schedule creation so the limit cannot be exceeded by simultaneous requests. Schedule updates that re-enable a disabled schedule must use the same check.

### 6.4 Job submission request schema

```json
{
  "task_type": "placement_notice.send",
  "queue": "notifications",
  "payload": { "notice_id": "notice_123" },
  "priority": 5,
  "run_at": "2026-10-16T13:00:00Z",
  "max_attempts": 5,
  "timeout_seconds": 300
}
```

Validation:
- `task_type`: required, 1–128 chars, recommended syntax `[a-zA-Z0-9._-]+`; must be a handler registered by a worker that consumes the selected queue or produce a clearly defined no-handler outcome.
- `queue`: default `default`, must be present in the operator-configured global `QF_ALLOWED_QUEUES` allowlist; bounded identifier length, no control characters. V1 does not allow arbitrary tenant-created queue names, keeping routing simple and global metric cardinality bounded.
- `payload`: JSON object (not arbitrary bytes); maximum 256 KiB serialized size. Canonicalize for idempotency hash deterministically.
- `priority`: integer 0–9; default 5.
- `run_at`: optional; defaults to database current time; reject beyond the configured future scheduling horizon. An explicit timestamp in the past is accepted as immediately eligible. Normalize timestamps to UTC before storing/hashing.
- `max_attempts`: optional; 1–10 total attempts; default 5.
- `timeout_seconds`: optional; positive and <= deployment maximum.
- `tenant_id`: forbidden in public body; derive from authenticated principal.

### 6.5 Response/error contract

A new accepted enqueue returns `202 Accepted` only after its transaction commits. The response includes job ID, status, creation time, run time, and status URL. An identical retry with an unexpired idempotency key returns `200 OK`, the original job representation, and `Idempotent-Replay: true`. Reusing that key with a different semantic request returns `409 idempotency_key_reused`. This behavior is fixed for V1 and must match OpenAPI and tests.

Error body:

```json
{
  "error": {
    "code": "idempotency_key_reused",
    "message": "The idempotency key was already used for a different request.",
    "request_id": "req_01J..."
  }
}
```

Do not return raw SQL text, driver errors, stack traces, lease tokens, API-key hashes, or another tenant's existence.

## 7. Data model

Use UUID primary keys generated application-side or by PostgreSQL. Use `timestamptz` for all absolute timestamps. Use `jsonb` for bounded JSON payload/results. Use foreign keys where they support safe retention and tenant integrity; where deleting parent history would cascade undesirably, use explicit archival semantics instead.

The following DDL is a reference blueprint; implement it as ordered migrations, review constraints, and test every migration on a clean database. The precise fields may change in implementation but must preserve the stated invariants.

### 7.1 Tenants and API keys

Tenant creation is one operator transaction: insert the tenant, default/overridden `tenant_limits`, initial `tenant_rate_buckets` row, `tenant_usage` row, and exactly the configured number of enabled `tenant_execution_slots`. API-key issuance is a separate auditable operation. Never create a tenant with limits but without its usage/rate/slot rows, because API and worker logic depend on these rows existing.

```sql
CREATE TABLE tenants (
  id uuid PRIMARY KEY,
  name text NOT NULL,
  status text NOT NULL CHECK (status IN ('active', 'suspended')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE api_keys (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL REFERENCES tenants(id),
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
```

Generate keys from a cryptographically secure random source with enough entropy (at least 256 random bits recommended). Store only the hash of the random secret; show the secret once at creation. Since randomly generated secrets are high entropy, a modern cryptographic hash is suitable for lookup. A keyed HMAC can also be used if the pepper lifecycle is designed carefully. Never store a reversible encrypted copy just to redisplay the secret.

### 7.2 Tenant limits and rate bucket

```sql
CREATE TABLE tenant_limits (
  tenant_id uuid PRIMARY KEY REFERENCES tenants(id),
  enqueue_rate_per_second numeric(12,4) NOT NULL DEFAULT 60 CHECK (enqueue_rate_per_second > 0),
  enqueue_burst_capacity numeric(12,4) NOT NULL DEFAULT 120 CHECK (enqueue_burst_capacity > 0),
  max_pending_jobs integer NOT NULL DEFAULT 10000 CHECK (max_pending_jobs > 0),
  max_concurrent_jobs integer NOT NULL DEFAULT 4 CHECK (max_concurrent_jobs > 0),
  max_schedules integer NOT NULL DEFAULT 100 CHECK (max_schedules > 0),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE tenant_rate_buckets (
  tenant_id uuid PRIMARY KEY REFERENCES tenants(id),
  tokens numeric(14,6) NOT NULL CHECK (tokens >= 0),
  last_refill_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);
```

Tenant provisioning creates the initial token-bucket row with `tokens = enqueue_burst_capacity` and `last_refill_at = clock_timestamp()`. The token bucket row is locked and updated transactionally during enqueue. Refill amount is `min(capacity, tokens + elapsed_seconds * rate_per_second)`; if fewer than one token remains, reject with 429 and compute an approximate retry-after from the deficit/rate. Consume one token for a new job submission. Idempotent replays should not create new jobs; the successful idempotency replay bypasses enqueue token consumption because it does not create new work. The key lookup and creation race must be handled transactionally.

### 7.3 Tenant execution slots

Use database-backed slots to enforce a hard per-tenant concurrency cap across worker replicas. This is preferable to checking `COUNT(running)` without a lock, which can oversubscribe during concurrent claims.

```sql
CREATE TABLE tenant_execution_slots (
  tenant_id uuid NOT NULL REFERENCES tenants(id),
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
```

Provision exactly `max_concurrent_jobs` enabled slots per tenant. If the cap changes, a transaction updates the slot set: never delete an occupied slot; mark excess slots for retirement after they become free or represent enablement explicitly. Simpler v1 option: store an `enabled` boolean on slot rows and only claim enabled free slots; resizing uses this boolean. Use that option if dynamic cap changes are required. Slot assignment and job claim occur in the same transaction. Slot release must match both job ID and lease token to avoid clearing a newer reservation.

### 7.4 Jobs

```sql
CREATE TABLE jobs (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL REFERENCES tenants(id),
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
```

Add a tenant-scoped job index and queue-claim index. Candidate index sketch (verify with `EXPLAIN (ANALYZE, BUFFERS)` on realistic data):

```sql
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
```

The claim index is a hypothesis, not a guaranteed optimal plan. The exact order must be benchmarked; including `run_at <= now()` and priority order can cause trade-offs between selecting high-priority eligible work and finding due work quickly. Do not add many overlapping indexes without measuring write amplification and query plans. PostgreSQL supports partial indexes, but their predicates must match the query predicates the planner can recognize. [R5]

### 7.5 Attempts

```sql
CREATE TABLE job_attempts (
  id uuid PRIMARY KEY,
  job_id uuid NOT NULL REFERENCES jobs(id),
  tenant_id uuid NOT NULL REFERENCES tenants(id),
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
```

One attempt row is created in the claim transaction. On successful finish/failure/lease expiry, update that row with outcome and finish time in the same transaction as the job transition. Error message storage is capped (for example 2 KiB), normalized, and scrubbed of known secrets. Keep detailed stack traces only in restricted structured logs with a correlation ID.

### 7.6 Idempotency keys

```sql
CREATE TABLE idempotency_keys (
  tenant_id uuid NOT NULL REFERENCES tenants(id),
  idempotency_key text NOT NULL,
  request_hash bytea NOT NULL,
  job_id uuid NOT NULL REFERENCES jobs(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, idempotency_key)
);
CREATE INDEX idempotency_expiry_idx ON idempotency_keys(expires_at);
```

`request_hash` is computed over a canonical, validated semantic representation, including fields that alter job behavior (task type, queue, canonical payload, priority, due-time mode/value, retry limit, timeout, and applicable schedule references). Do not hash raw JSON bytes because property ordering and whitespace should not make semantically identical requests conflict. Important edge case: if `run_at` is omitted, hash an explicit `immediate` sentinel rather than the actual generated enqueue timestamp; otherwise a retry seconds later would incorrectly conflict with the original request. If `run_at` is provided, hash its normalized UTC instant. Normalize stable defaults such as queue, priority, attempts, and timeout before hashing.

### 7.7 Worker registry

```sql
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
```

Do not treat a stale registry row as proof that a job is safe to recover; the individual job lease is authoritative. The registry is for visibility and operational diagnosis.

### 7.8 Schedules

```sql
CREATE TABLE job_schedules (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL REFERENCES tenants(id),
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
  FOREIGN KEY (schedule_id) REFERENCES job_schedules(id);
```

The `jobs` table's unique partial index on `(schedule_id, scheduled_for)` prevents duplicate job creation for one schedule occurrence. If a future design requires an explicit occurrence/audit table, add it through an ADR.

### 7.9 Constraints and integrity principles

- All tenant-owned child records must point to a tenant or a tenant-owned parent.
- A `running` job must have worker ID, lease token, and expiration; non-running jobs must not have those fields populated.
- Attempt numbers are unique within a job and replay generation.
- `job_schedules` must never directly execute code; it only generates ordinary jobs. Schedule creation must enforce `tenant_limits.max_schedules` for enabled/non-disabled schedules.
- Validate application-level assumptions at the database layer when practical, but don't try to express every state-machine rule as a CHECK constraint.
- Migrations add indexes carefully; for large production tables, use concurrent index creation in a migration path that supports it.

## 8. Claiming algorithm and concurrency control

### 8.1 Invariants

1. A job has at most one current valid QueueForge lease at a time.
2. Claiming a job, consuming a tenant concurrency slot, incrementing attempt count, setting lease fields, and creating an attempt record commit atomically.
3. If any required step fails, the transaction rolls back and none of these changes is visible.
4. A job's user handler runs only after the claim transaction commits.
5. Success/failure/renewal updates require the same `lease_token` as the active claim.
6. No tenant can have more running jobs than enabled execution slots, assuming all workers use this protocol and slot state is not manually corrupted.

### 8.2 Claim flow

Each worker loop:

1. Checks context/shutdown state.
2. Begins a transaction with `READ COMMITTED` unless integration evidence requires another isolation level.
3. Selects one eligible queued job with due `run_at`, matching the worker's supported queue/task types, ordered by priority descending then due time, creation time, ID, and `FOR UPDATE SKIP LOCKED`.
4. Selects a free enabled slot for the job's tenant with `FOR UPDATE SKIP LOCKED`.
5. If no slot is available, rolls back/releases the candidate lock and backs off briefly or searches another candidate from a bounded candidate batch. The implementation must avoid hot-looping repeatedly on a tenant whose slots are full.
6. Generates a fresh cryptographically secure UUID lease token.
7. Updates the job to `running`, increments `attempts_started`, records `worker_id`, `lease_token`, `lease_expires_at = now() + lease_duration`, and `started_at` for the first attempt if needed.
8. Reserves the tenant slot with the job ID and lease token.
9. Inserts `job_attempts` with `attempt_number = attempts_started` and current replay generation.
10. Commits, then starts handler execution.

Illustrative SQL for the first selection (final SQL may use CTEs or a bounded candidate batch):

```sql
SELECT j.id, j.tenant_id, j.queue_name, j.task_type, j.payload,
       j.attempts_started, j.max_attempts, j.timeout_seconds
FROM jobs AS j
WHERE j.status = 'queued'
  AND j.run_at <= now()
  AND j.queue_name = ANY($1::text[])
  AND j.task_type = ANY($2::text[])
  AND EXISTS (
    SELECT 1 FROM tenant_execution_slots AS s
    WHERE s.tenant_id = j.tenant_id
      AND s.current_job_id IS NULL
      AND s.enabled = true
  )
ORDER BY j.priority DESC, j.run_at ASC, j.created_at ASC, j.id ASC
LIMIT 1
FOR UPDATE OF j SKIP LOCKED;
```

The schema includes `enabled` so tenant limits can be adjusted without deleting occupied slots: disabling a busy slot takes effect only after it becomes free. Candidate selection only indicates that a free slot appears to exist; the slot itself must still be locked and successfully reserved in the transaction. If another worker wins the slot between the `EXISTS` check and slot lock, rollback and retry with bounded jitter. This pattern is safe but must be measured for contention.

Slot query:

```sql
SELECT tenant_id, slot_no
FROM tenant_execution_slots
WHERE tenant_id = $1 AND current_job_id IS NULL AND enabled = true
ORDER BY slot_no
LIMIT 1
FOR UPDATE SKIP LOCKED;
```

The job row and slot row are then updated in the same transaction. If the selected job has no registered handler in the local worker, it should not be claimed by that worker; filter supported `task_type` values at query time. A job whose task type is unsupported by every worker needs an operational diagnostic/runbook so it is not left queued indefinitely without explanation.

### 8.3 Strict priority and fairness

V1 uses strict priority order. This is straightforward and deterministic, but a continuous high-priority stream may starve lower-priority jobs. Document this limitation and measure wait time by priority during the benchmark. Weighted round-robin or aging is a future enhancement only if the measured workload calls for it.

## 9. Lease management, heartbeat, and recovery

### 9.1 Lease policy

Initial defaults: 60-second lease, renewal every 20 seconds. Use monotonic timers in process; persisted timestamps are PostgreSQL wall-clock timestamps. A lease expiration is based on database time (`now()`/`clock_timestamp()` chosen consistently), not worker machine time, to avoid clock-skew disagreements. Use PostgreSQL `clock_timestamp()` consistently for lease creation, renewal, expiration checks, and recovery decisions; use `now()`/transaction time for ordinary created/updated timestamps. Do not compare a worker machine clock with a lease expiry to decide ownership.

### 9.2 Renewal

A running worker periodically updates the job's lease expiry only if the job remains running, the token matches, and the existing lease has not expired:

```sql
UPDATE jobs
SET lease_expires_at = clock_timestamp() + $3::interval,
    updated_at = clock_timestamp()
WHERE id = $1
  AND status = 'running'
  AND lease_token = $2
  AND lease_expires_at > clock_timestamp()
RETURNING cancellation_requested_at;
```

If zero rows are returned, the worker has lost ownership. It must cancel the handler context, stop attempting to commit the result, emit `lease_lost`, and not attempt to reclaim the same job through the old token. Cancellation may be cooperative; the handler might still complete an external side effect.

The associated slot remains reserved while the job is running; its token is the same lease token. Slot lease renewal may be implicit through the job row, but slot release requires a matching `(tenant_id, slot_no, current_job_id, lease_token)` condition. Do not let a stale worker clear a slot that now belongs to a later attempt.

### 9.3 Worker heartbeat

Each worker process updates `worker_instances.last_seen_at` every 10 seconds by default. If a heartbeat update fails temporarily, log/metric the problem and retry with bounded backoff. Heartbeat absence is useful for the dashboard but must not directly force-release every job for that worker; a process can be temporarily partitioned from the registry while still processing a task. Job-specific lease expiry is the recovery signal.

### 9.4 Recovery transaction

For each bounded batch of jobs where `status='running'` and lease has expired:

1. Lock jobs with `FOR UPDATE SKIP LOCKED`.
2. Match the currently stored lease token to the attempt and slot.
3. Mark attempt outcome `lease_expired`, with finish time and duration if derivable.
4. Clear worker/lease fields.
5. If `attempts_started < max_attempts`, set status to `queued`, compute `run_at` using exponential full-jitter delay, and set an error code such as `worker_lease_expired`.
6. Otherwise set status `dead` and `completed_at`.
7. Release the slot conditionally on its matching job ID/token.
8. Commit changes atomically.

Do not set a recovery job back to queued without preserving why its previous attempt was abandoned. Use bounded batches to minimize lock duration. If the recovery transaction crashes, the transaction rolls back and another scan can retry it.

### 9.5 Graceful shutdown

On SIGTERM:
- Mark the worker `draining=true`.
- Stop claiming new jobs.
- Continue renewing and wait for active handlers up to configured shutdown timeout.
- Let completed handlers commit normally.
- Cancel remaining handler contexts at timeout.
- Do not forcibly clear active leases unless the handler has stopped and release occurs transactionally under the current token; otherwise allow lease expiration recovery.

## 10. Retry and dead-letter algorithm

### 10.1 Error classification

Define a handler signature similar to:

```go
type Handler func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error)

type PermanentError struct { Err error }
func (e PermanentError) Error() string { return e.Err.Error() }
func (e PermanentError) Unwrap() error { return e.Err }
```

A `PermanentError` bypasses automatic retries. Other errors are retryable by default, subject to cancellation/timeouts and max attempts. Avoid leaking raw error strings without truncation/sanitization. If handler returns success but its database success transaction fails, the job remains running until recovery and can execute again; this ambiguity is fundamental to at-least-once processing.

### 10.2 Backoff formula

Let `n` be the failed attempt number (1-based), `base` the base delay, and `cap` the maximum delay.

`upper = min(cap, base * 2^(n-1))`  
`delay = uniform_random(0, upper)` (full jitter)

Use overflow-safe duration math. Store the resulting `run_at` in PostgreSQL. The exact delay is sampled once when recording the failure and is not resampled by a restart. A unit-testable injected random source makes tests deterministic.

### 10.3 Failure transition

In one transaction, guarded by matching current lease token:
- Update attempt row with failure outcome, finish time, duration, bounded error code/message.
- If permanent or attempts reached `max_attempts`, set job state `dead`, `completed_at`, clear lease fields, release slot.
- Otherwise set state `queued`, set `run_at = now + retry_delay`, clear lease fields, release slot.
- Commit. If update affects zero rows because ownership changed, treat as lease lost; do not overwrite the new owner.

### 10.4 Replay semantics

V1 manual replay is auditable. Only allow replay of a `dead` job; preserve old attempt rows and increment `replay_generation`; reset `attempts_started` to 0; set status queued, `run_at=now`, clear terminal fields/errors and cancellation flag; keep payload/task/queue/priority/retry policy unchanged unless an explicit safe edit API exists; increment `tenant_usage.outstanding_jobs` transactionally after checking the tenant quota; and record actor/key ID and reason in `job_audit_events`. The idempotency record for the original submission still points to the same job ID; clients repeating the original submission must not cause an additional enqueue. This replay policy should be stated in the API docs.

Add:

```sql
CREATE TABLE job_audit_events (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL REFERENCES tenants(id),
  job_id uuid NOT NULL REFERENCES jobs(id),
  actor_api_key_id uuid,
  event_type text NOT NULL,
  reason text,
  created_at timestamptz NOT NULL DEFAULT now(),
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX job_audit_job_idx ON job_audit_events(job_id, created_at);
```

If deletion/retention removes jobs, audit retention policy must not leave invalid references; choose archive or controlled cascade explicitly.

## 11. Idempotency implementation

### 11.1 Canonical request hash

After parsing and validating, marshal a deterministic struct with normalized defaults and UTC timestamp representations. JSON object keys inside arbitrary payloads need canonicalization (sort map keys recursively) before hashing. Hash the canonical byte sequence with SHA-256.

### 11.2 Transaction flow

1. Authenticate and validate the request; compute the canonical request hash.
2. If an idempotency key is present, read an unexpired record for `(tenant_id, key)`. If found, compare hashes: same hash returns the existing job with `200 OK` and `Idempotent-Replay: true`; different hash returns `409 idempotency_key_reused`. This fast path does not consume enqueue quota because it creates no work.
3. Begin a transaction and insert a provisional job row.
4. If a key is present, attempt the atomic idempotency insert/expired-key replacement described below. If no row is returned because another unexpired record won the uniqueness race, roll back the entire transaction (including the provisional job), read the winning record in a new transaction, compare hashes, and return either the existing job or 409.
5. For a genuinely new job, lock/update tenant usage and rate-bucket rows in a consistent order across enqueue paths. Enforce `max_pending_jobs`, refill/consume one token, then increment `outstanding_jobs`. If any quota fails, roll back the job and idempotency reservation and return the appropriate 429/quota error.
6. Commit. Return `202 Accepted` only after commit.

For correct expiry behavior, use `INSERT ... ON CONFLICT (tenant_id, idempotency_key) DO UPDATE SET request_hash = EXCLUDED.request_hash, job_id = EXCLUDED.job_id, created_at = EXCLUDED.created_at, expires_at = EXCLUDED.expires_at WHERE idempotency_keys.expires_at <= clock_timestamp() RETURNING ...` after the provisional job is inserted. A new key inserts; an expired key is atomically rebound to the new job; an unexpired conflict returns no row and triggers rollback of the provisional job, followed by reading the winning record and comparing request hashes. A concurrent loser waits on the unique key and then observes the committed winner. This handles expiry safely without requiring the cleanup loop to run at the exact expiry timestamp. Do not rely only on an in-memory mutex because multiple API replicas may be running.

### 11.3 Expiry

A cleanup process deletes expired idempotency records in batches. An expired key may later be reused according to the API contract. The client must not assume an idempotency key is permanent forever. Set idempotency retention longer than the typical client retry window and document it clearly.

## 12. Scheduling and delayed jobs

### 12.1 Delayed jobs

Delayed work uses `jobs.status='queued'` and `jobs.run_at` in the future. Claim query requires `run_at <= database time`; no separate in-memory timer is authoritative. This makes delay state durable through process restarts.

### 12.2 Cron expression support

V1 accepts standard five-field cron (minute, hour, day of month, month, day of week) with an explicit IANA timezone. Seconds and nonstandard extensions are rejected. Validate the expression once on creation/update and compute the next occurrence using a pinned cron library. Store `next_run_at` as UTC instant and preserve the declared timezone/expression for future calculations.

### 12.3 Scheduler transaction

For each bounded candidate schedule:
1. Begin transaction.
2. Select a due, unpaused, enabled schedule with `FOR UPDATE SKIP LOCKED`. Also scan schedules with `pending_catch_up_at IS NOT NULL` so a deferred catch-up is not lost.
3. Lock the tenant's `tenant_usage` row and check the pending-job quota before creating an occurrence.
4. If capacity is available, select the occurrence to generate: `pending_catch_up_at` first when present; otherwise apply the lateness/misfire policy to `next_run_at`.
5. Insert a job with copied task/queue/payload/retry configuration and the pair `(schedule_id, scheduled_for)`. Set the job's `run_at` to the intended occurrence time so scheduler delay remains measurable.
6. Use the unique occurrence index to prevent duplicates. If an occurrence already exists, do not create a second job or increment usage again.
7. Increment the tenant outstanding-job counter exactly once for a new job. Do not consume the HTTP enqueue token bucket for a cron-generated job, but do enforce the tenant pending-job quota.
8. If the generated job came from the current `next_run_at`, advance `next_run_at` using cron/timezone rules. If it came from `pending_catch_up_at`, clear that field but leave the already-advanced `next_run_at` unchanged. Update `last_scheduled_for`/`updated_at`.
9. Commit.

Job insert, usage counter change, catch-up cursor change, and `next_run_at` advancement must be atomic. If quota is full: for `skip`, record `last_skipped_at` and advance to the first future occurrence without generating a job; for `catch_up_one`, store the most recent missed occurrence in `pending_catch_up_at`, advance `next_run_at` to the first future occurrence, and attempt to generate the pending occurrence when capacity is available. Coalesce subsequent missed occurrences into that one pending timestamp. Process schedules in bounded batches so one tenant at quota does not prevent unrelated tenants from progressing. If anything fails before commit, transaction changes roll back.

### 12.4 Misfire policies

Use a configurable `QF_SCHEDULE_MISFIRE_GRACE` (initial default 30 seconds) to distinguish a normal scheduler poll delay from a genuine misfire. If `now - next_run_at` is within the grace window, generate the due occurrence normally. If it exceeds the grace window, apply the schedule's misfire policy:

- `skip`: create no job for missed occurrences; record the last skipped occurrence and advance to the first future cron occurrence.
- `catch_up_one`: generate one job for the **most recent** missed occurrence only, then advance to the first future occurrence. If quota is full, hold one `pending_catch_up_at` and coalesce later missed occurrences to the most recent one.

Do not implement “catch up every missed run” in v1; it can create a burst after downtime. Define daylight-saving behavior in docs and tests: non-existent local times are skipped according to cron parser semantics; ambiguous repeated local times follow the selected library behavior. Pin and test that dependency behavior.

### 12.5 Schedule update/delete

Updating cron expression/timezone recomputes `next_run_at` from a clearly documented reference time; it does not rewrite already-generated jobs. Deleting a schedule should set `disabled_at` rather than physically deleting it immediately, preserving auditability. “Pause” prevents creation of future runs but does not cancel jobs already generated.

## 13. Tenant rate limits and concurrency

### 13.1 Enqueue rate token bucket

Use `tenant_rate_buckets` as shared PostgreSQL state so API replicas enforce one logical limit. Lock the tenant's bucket row for update in the job-creation transaction. Refill tokens according to elapsed database time, cap at burst capacity, reject if less than one token, or consume one token for a new job. Return 429 with `Retry-After` rounded conservatively.

Trade-off: one row per tenant may serialize high-rate enqueue requests from the same tenant. This is acceptable for a first version; use metrics/benchmarks to evaluate. Per-process memory-only token buckets are not adequate for shared global limits across replicas.

### 13.2 Outstanding-job quota

Inside the enqueue transaction, count or maintain a counter of nonterminal jobs for that tenant. A raw count is easiest initially but can become expensive. If raw count is used, ensure enqueue uses a tenant-level lock or an atomic quota-counter row so concurrent requests cannot overshoot the limit. Prefer a `tenant_usage` counter updated transactionally when a job enters/leaves the counted states; define consistency/reconciliation tests. For v1, an explicit `tenant_usage` table is recommended:

```sql
CREATE TABLE tenant_usage (
  tenant_id uuid PRIMARY KEY REFERENCES tenants(id),
  outstanding_jobs bigint NOT NULL DEFAULT 0 CHECK (outstanding_jobs >= 0),
  updated_at timestamptz NOT NULL DEFAULT now()
);
```

On enqueue, lock the row and verify `outstanding_jobs < max_pending_jobs`, then increment it. Decrement once when a job first transitions out of outstanding states, guarded by old state; replay increments again. Recovery from `running` to `queued` does not alter outstanding count. Provide a periodic reconciliation query/test to compare the counter with actual job states.

### 13.3 Execution concurrency slots

The slot count limits how many jobs from one tenant can be in `running` state across all workers. Claiming a job and reserving one free slot happen in the same transaction. On success, failure-to-queue, dead-letter, cancellation, or lease recovery, clear the matching slot in that same transaction. Replayed dead jobs must reserve a slot only when claimed, not when requeued.

## 14. Cancellation semantics

For queued jobs, cancel by conditional update `WHERE status='queued'`, set state cancelled and terminal timestamp, update tenant outstanding counter, and audit. If the conditional update affects zero rows, reread current status under tenant scope and return a consistent conflict/terminal response.

For running jobs, set `cancellation_requested_at` if absent. The worker's lease renewal response includes the cancellation flag; the worker cancels the handler context and waits for cooperative exit until timeout. On handler exit, record cancelled if the handler acknowledges cancellation and has not already committed success. A race between completion and cancellation is serialized by conditional state/token updates; exactly one terminal transition wins.

Do not promise rollback of actions already performed by the handler. Long-running handlers should periodically check `ctx.Done()` and make cancellation safe at domain level where possible.

## 15. Worker SDK/runtime contract

### 15.1 Handler registration

A trusted worker binary registers handlers at startup. Example public API:

```go
type Handler func(context.Context, json.RawMessage) (json.RawMessage, error)

type Registry interface {
    Register(taskType string, handler Handler)
}
```

A practical implementation may return a typed result wrapper or use a standard error classifier. Registration must reject duplicate task type names. Handler code should not write directly to QueueForge job tables.

### 15.2 Handler requirements

- Validate payload schema at the handler boundary.
- Respect context cancellation and deadlines.
- Use a domain-level idempotency mechanism before non-idempotent external side effects.
- Return a permanent typed error for non-retryable invalid input or business rejection.
- Avoid logging raw secrets or full sensitive payloads.
- Keep execution within configured timeouts where possible.
- Produce a bounded JSON result or return no result; large output belongs in object storage with a reference.

### 15.3 Unknown handler behavior

A worker only claims a task type it has registered. Provide a diagnostic endpoint/command that can show queued task types without a recent compatible worker. Do not let every worker claim unknown tasks and repeatedly fail them. An optional operator policy may dead-letter a task after an explicit no-handler timeout; this is better than implicit fast retries but should be separately configured.

## 16. Database transaction boundaries

| Operation | Single transaction? | Important invariant |
|---|---|---|
| New enqueue + idempotency + rate token + outstanding count | Yes | Accepted job cannot exist without durable idempotency/quota bookkeeping |
| Worker claim + attempt insert + slot reservation | Yes | No partially claimed job or leaked slot |
| Job success + attempt success + slot release + outstanding decrement | Yes | Terminal status and accounting stay consistent |
| Retryable failure + attempt failure + queue time + slot release | Yes | Retry is durable and scheduled once |
| Lease recovery + attempt close + requeue/dead + slot release | Yes | Expired work cannot remain indefinitely running after recovery commit |
| Schedule occurrence insert + next-run advance | Yes | No duplicate or lost schedule cursor advancement |
| Tenant API-key revocation | Yes | Subsequent authorization checks reject the key after commit |
| Retention deletes | Bounded batches | Avoid giant long-running locks; preserve FK/audit policy |

Use `READ COMMITTED` as initial isolation level and conditional updates/locks to enforce invariants. Do not use `SERIALIZABLE` by default without a demonstrated need; if a transaction aborts due to serialization/deadlock, retries must be bounded and safe. Keep transaction callbacks free of external network calls.

## 17. Retention and cleanup

Recommended defaults (configurable): terminal jobs and their attempt/audit history 30 days, idempotency keys 24 hours, worker registry rows kept for 7 days after `last_seen_at` becomes stale. Startup validation must require terminal-job retention to be greater than or equal to the idempotency TTL. Retention values must be consistent with FK relationships and replay/audit needs.

- Hard-delete terminal jobs only after the 30-day retention window. In bounded transactions, delete expired idempotency records and dependent attempt/audit rows before deleting the terminal job rows; keep FK checks enabled.
- Do not delete an active/queued job.
- Use small batches (`LIMIT` in a CTE) with short transactions; attempt history and audit rows do not outlive the related job in V1.
- Track rows deleted and runtime.
- Do not run VACUUM manually inside application transactions; rely on PostgreSQL autovacuum and monitor dead tuples/table growth.
- Before enabling automatic deletion in a production demo, test retention against idempotency replay, scheduled-occurrence uniqueness, audit, and dashboards.

## 18. Observability and operational metrics

Metric names should follow Prometheus naming conventions, include units in names, use counters/gauges/histograms appropriately, and avoid high-cardinality labels such as job IDs, user IDs, payload values, or tenant IDs. Prometheus explains that every unique label combination creates a time series and warns against unbounded label dimensions. [R6]

### 18.1 Proposed metrics

| Metric | Type | Labels (bounded) | Purpose |
|---|---|---|---|
| `queueforge_api_requests_total` | Counter | route template, method, status class | Request rate/errors |
| `queueforge_api_request_duration_seconds` | Histogram | route template, method | API latency |
| `queueforge_jobs_enqueued_total` | Counter | queue, task type only if bounded registry | Accepted jobs |
| `queueforge_jobs_claimed_total` | Counter | queue | Claim throughput |
| `queueforge_jobs_completed_total` | Counter | queue, outcome | Success/dead/cancelled results |
| `queueforge_job_attempts_total` | Counter | queue, outcome | Attempt-level reliability |
| `queueforge_job_execution_duration_seconds` | Histogram | queue, task class/bounded type | Handler duration |
| `queueforge_job_queue_wait_seconds` | Histogram | queue, priority | Time from due eligibility to start |
| `queueforge_job_retries_total` | Counter | queue, error class | Retry counts |
| `queueforge_job_lease_recoveries_total` | Counter | queue | Crash recovery events |
| `queueforge_jobs_queued` | Gauge | queue | Current queued count (query/cache strategy documented) |
| `queueforge_jobs_running` | Gauge | queue | Current running count |
| `queueforge_worker_last_heartbeat_age_seconds` | Gauge | worker pool/role, not individual ID | Heartbeat health aggregation |
| `queueforge_scheduler_runs_total` | Counter | outcome | Scheduler operation status |
| `queueforge_db_pool_connections` | Gauge | state | Pool utilization |
| `queueforge_tenant_rate_limit_rejections_total` | Counter | reason only | Limit activity without tenant label |
| `queueforge_recovery_scan_duration_seconds` | Histogram | result | Recovery work duration |

A bounded set of `task_type` label values is acceptable only if the system limits and governs them; otherwise aggregate by queue/outcome to avoid unbounded cardinality. Tenant-specific dashboard metrics should come from tenant-scoped API queries rather than tenant IDs exposed as Prometheus labels.

### 18.2 Logs

Use JSON logs with timestamp, severity, service, version, request ID, worker ID, job ID, attempt number, queue, task type, event, outcome, duration, and error code. `tenant_id` may appear in restricted logs if operationally necessary, but avoid personal identifiers. Never log API secrets, full authentication headers, raw secret hashes, or payloads by default.

### 18.3 Health/readiness

- `/healthz`: returns success if process event loop/server is alive; no dependency query required.
- `/readyz`: verifies PostgreSQL connectivity and migration/schema compatibility; returns 503 when it cannot safely accept work.
- `/metrics`: bound to private interface/network or protected by scrape authentication. Do not publicly expose database topology or per-tenant activity.

### 18.4 Example operational alerts

- Oldest queued-job age exceeds threshold for a sustained window.
- Dead-letter count/rate rises sharply.
- Lease recoveries rise above baseline.
- No recent heartbeat from worker pool while queued jobs exist.
- Database pool is near maximum or wait duration increases.
- Enqueue rejection rate indicates tenant quota abuse or unexpected traffic.
- Scheduler has due schedules but no new occurrences are generated.

## 19. Dashboard implementation design

Use a static React + TypeScript frontend in `/web`; the Go API remains the only business backend. Dashboard pages call tenant-scoped REST APIs. Keep API-key secrets out of compiled JavaScript and never hardcode a tenant secret at build time. V1 dashboard authentication is deliberately limited to private/demo operation: the user enters a tenant API key and the frontend keeps it only in memory for that tab/session (not localStorage/sessionStorage); refresh requires re-entry. Do not expose this mode as a public multi-tenant dashboard. An internet-facing dashboard requires a server-side session/BFF or OIDC authentication design before public deployment.

Minimum pages:
1. Overview: high-level counts and rate charts.
2. Jobs list: status/queue/task type/time filters and pagination.
3. Job detail: timeline, attempts, error, schedule, payload/result view, actions.
4. Schedules: list/create/pause/resume/update/disable.
5. Dead letters: list/detail/replay.
6. Settings/status: key-safe tenant limits and API/server status, without platform secrets.

Do not let dashboard filtering bypass API authorization; the server is the security boundary. Use accessible labels, keyboard navigation, loading/empty/error states, and confirmations for replay/cancellation.

## 20. Deployment topology

### 20.1 Local development

Docker Compose services:
- `postgres` (persistent named volume, healthcheck)
- `api`
- `worker` (one or more replicas launched explicitly)
- `scheduler` / recovery loops (can initially live in worker process behind flags, but isolate process role in deployment when convenient)
- optional `prometheus`
- optional `grafana`
- optional `web` dev server

An initial simple deployment may run API, worker, scheduler, and recovery loops as separate commands from the same image. Ensure only one active migration runner executes migrations at startup, or use a documented migration job before service rollout.

### 20.2 Small VPS demo

- One Linux VPS running Docker Compose.
- PostgreSQL volume on a persistent disk; configure backups and verify restore.
- Reverse proxy (Caddy or Nginx) for HTTPS and frontend/API routing.
- Only expose HTTPS API and dashboard publicly; keep PostgreSQL and metrics private.
- Use separate DB users/roles if practical: migration owner, API/worker runtime, read-only metrics/admin. Runtime roles should not be superusers or schema owners.
- Configure memory limits and restart policies; do not rely on container restart as the only recovery mechanism.
- Collect logs centrally or rotate logs to prevent disk exhaustion.
- Back up `.env` secrets securely, not into public repository or public artifact storage.

### 20.3 Database availability and backup assumptions

The v1 platform relies on PostgreSQL durability. Document the configured durability/backup assumptions. A successful API response means PostgreSQL committed according to the server's configured guarantees; it does not guarantee protection against every disk, host, operator, or backup failure. Test backup restoration before describing recovery objectives.

## 21. Security design and threat model

### 21.1 Trust boundaries

- External clients are untrusted until API-key authentication succeeds.
- Tenant payloads are untrusted JSON data and must never be executed as code.
- Worker handler binaries are trusted deployment artifacts; handler output/errors are still untrusted for rendering/logging.
- Dashboard browser is not a trusted place to store a long-lived platform admin credential.
- PostgreSQL is private infrastructure, accessible only to service roles and operators.

### 21.2 Controls

- Hash API keys, restrict permissions, revoke keys, and redact authorization headers from logs.
- Enforce tenant filters on every read/write; add automated cross-tenant tests for every tenant-owned endpoint.
- Limit body/payload/result sizes, pagination, schedule counts, cron catch-up behavior, concurrent running jobs, and enqueue rate.
- Use parameterized SQL; never concatenate user payloads into SQL statements.
- Render payloads in the dashboard as escaped JSON/text; do not render HTML from payloads.
- Set reasonable HTTP read-header/read/write/idle timeouts; cap outbound calls made by handler examples.
- Store database credentials in environment/secret store and use least privilege.
- Run dependency scanning, static analysis, and secret scanning in CI.
- Use TLS in public deployment; maintain a clear local-development exception. Configure CORS from `QF_ALLOWED_ORIGINS`, do not use a wildcard for a public deployment, and set appropriate security headers/CSP on the dashboard.
- Document key rotation and revoke procedures.

### 21.3 Threat / mitigation table

| Threat | Control | Test/evidence |
|---|---|---|
| Guessed job ID accesses another tenant | Tenant-scoped query and consistent 404 | Cross-tenant ID tests |
| API key leaked in logs | Authorization header redaction; key hash storage | Log capture test / grep CI |
| Enqueue flood exhausts DB | Shared tenant token bucket, pending quota, body caps, DB pool limits | Load/rate test |
| Malicious oversized payload | Early content-length and streaming body cap | 413 integration test |
| SQL injection | Parameterized queries and strict sort/filter allowlist | Static review/security test |
| Malicious task type | Only local registered handlers; no dynamic eval/shell | Code review and test asserting unknown handler is not executed |
| Cross-tenant schedule mutation | Tenant scope in update/delete | Authorization tests |
| Metrics reveal customer activity | Bounded labels, private endpoint | Config check and metric-label test |
| Replay causes accidental duplicate side effect | Permission, audit, clear semantics, handler idempotency docs | Replay authorization/integration test |
| Stale worker overwrites newer run | Conditional lease-token updates | Stale-token test |

OWASP highlights broken object-level authorization, broken authentication, and unrestricted resource consumption among major API risks. QueueForge should turn those into explicit test cases, not just a security checklist. [R4]

## 22. Testing strategy

### 22.1 Test layers

**Unit tests**
- State transition validation.
- Retry schedule computation with deterministic random source.
- Cron validation and misfire policy.
- Canonical payload hashing.
- API validation and permission matrix.
- Error classification and sanitization.
- Token-bucket math with simulated time.

**PostgreSQL integration tests**
- Apply migrations against a real supported PostgreSQL instance.
- Idempotency same-key/same-request returns one job.
- Same key/different request returns 409.
- Many concurrent enqueue requests with identical key produce one job.
- Many workers claim jobs concurrently; no simultaneous valid lease for a job.
- Tenant slot ceiling is never exceeded under concurrent claims.
- Outstanding counter remains consistent through all states/replay/recovery.
- Lease renewal succeeds only with a valid current token and unexpired lease.
- Stale worker cannot complete or release a slot after reclaim.
- Schedule occurrence is generated once under multiple schedulers.
- Retry budget and dead-letter transition are exact.

**API tests**
- Authentication missing/invalid/expired/revoked.
- Permission denied for each route.
- Cross-tenant access for each object endpoint.
- Pagination caps, invalid filters, malformed JSON, oversized payloads.
- Error envelope codes and request IDs.

**Worker tests**
- Success result is committed.
- Retryable failure queues the next attempt with persisted due time.
- Permanent error goes dead.
- Context deadline and shutdown behavior.
- Unknown task type is not claimed by that worker.
- Lease loss cancels context and stale completion is rejected.

**Chaos/fault tests**
- Kill process before handler starts after claim.
- Kill process during a handler.
- Kill process after external mock side effect but before completion DB update.
- Drop/restart DB connection during enqueue and during result acknowledgement.
- Run multiple scheduler instances concurrently.
- Flood queue while workers are stopped, then restart workers.
- Suspend one tenant's capacity and verify another tenant progresses.
- Interrupt scheduler for several expected occurrences and validate each misfire policy.

**Performance tests**
- Enqueue at ramped arrival rates.
- Compare one, two, four, and higher worker counts.
- Mix short/no-op and longer-running handlers.
- Test one hot tenant vs many tenants.
- Measure enqueue p50/p95/p99, end-to-end completion latency, throughput, retry/recovery rate, DB CPU/IO, pool wait, row/index size, and queue age.
- Include a test duration long enough to observe steady state and backlog behavior; publish warm-up and cool-down rules.

### 22.2 Race and static checks

CI should run at least:
- `go test ./...`
- `go test -race ./...` for supported packages/environments
- `go vet ./...`
- formatter check (`gofmt`)
- linter with pinned version
- migration lint/clean-database test
- OpenAPI validation
- dependency vulnerability scan
- secret scanning
- Docker image build
- frontend lint/typecheck/build if dashboard is included

Do not count a mocked repository test as evidence for row-lock correctness. Concurrency guarantees must be integration-tested against PostgreSQL.

## 23. Benchmark and chaos methodology

### 23.1 Baseline benchmark protocol

1. Record CPU, RAM, disk, OS/kernel, Docker version, Go version, PostgreSQL version/settings, DB pool sizes, worker count, and exact commit SHA.
2. Define workload: payload size, task type, handler behavior, target arrival rate, concurrency, duration, and whether `fsync`/durability settings are production-like.
3. Warm up, execute fixed-duration runs, and capture raw outputs.
4. Repeat each run at least three times where possible; report variability, not just the best run.
5. Avoid generating load on the same machine if it materially consumes the resources being measured, or explicitly label the setup.
6. Report successes, failures, timed-out requests, duplicate accepted jobs, completed jobs, queue backlog at start/end, and consistency reconciliation.
7. Include both no-op job throughput and a realistic integration workload; never present the no-op result as representative of all tasks.

### 23.2 Required benchmark metrics

- API enqueue requests/sec and accepted jobs/sec.
- Enqueue p50/p95/p99 latency.
- Job starts/sec and successful completion/sec.
- Queue wait p50/p95/p99.
- Execution duration p50/p95/p99.
- End-to-end latency p50/p95/p99.
- Error and 429 rates.
- Worker utilization and active slots.
- DB CPU, I/O, connections, transaction time, lock waits, pool wait, and query plans where available.
- Recovery time after worker termination.
- Retry counts and final reconciliation differences.

### 23.3 Required chaos scenario: kill worker

- Enqueue a known set of uniquely identified jobs.
- Start at least two workers.
- Kill one worker while it has active jobs, recording the selected test phase.
- Keep the remaining worker online, then allow lease expiry/recovery.
- Wait until no job remains queued/running, or reach a bounded timeout.
- Reconcile unique submitted jobs with final job rows and attempt history.
- Assert every accepted job ends in success or an explicitly expected dead/cancelled state; no job is silently missing.
- Assert no two current valid lease tokens coexist for a job.
- Do **not** assert that the handler function was entered only once in the crash-after-side-effect scenario; duplicate attempts are expected under at-least-once semantics.

### 23.4 Report format

Every benchmark report should contain:
- Date and commit SHA.
- Environment and versions.
- Database settings and worker/pool counts.
- Workload definition and script command.
- Warm-up, duration, repetitions.
- Raw data file reference.
- Charts/tables for throughput and latency percentiles.
- Failure/reconciliation results.
- Identified bottleneck, evidence, optimization, and before/after comparison.
- Limitations and the next experiment.

## 24. Failure-mode analysis

| Failure point | Possible result | Required behavior |
|---|---|---|
| Before enqueue transaction commits | Job may not exist | Return error if known; client retries with same key safely |
| Commit succeeds, HTTP response is lost | Client is uncertain | Same idempotency key returns original job |
| Claim tx fails midway | No partial claim visible | Rollback; slot/job/attempt changes all absent |
| Worker crashes before handler starts | Job remains running until lease expiry | Recovery closes attempt and retries/dead-letters |
| Worker crashes after side effect before result commit | Side effect may duplicate | At-least-once behavior documented; handler uses idempotency when possible |
| Worker renews after expiration | Stale worker may be working | Renewal rejected; context cancelled; stale token cannot update state |
| Old worker returns after a new worker claimed | Competing completion | Conditional update by token affects zero rows; old result discarded |
| Database fails during success commit | Outcome ambiguous | Handler may run again; don't report durable success without commit |
| Scheduler dies after insert but before commit | Transaction rollback | Schedule remains due and will be retried |
| Two schedulers process same due time | Contention | Row lock + unique occurrence key produces at most one occurrence job |
| Cleanup runs while job is active | Potential data corruption | Query only terminal/expired records and bounded batches; FK protection |
| Tenant quota counter drifts | Wrong reject/accept behavior | Transactional updates plus periodic reconciliation alarm/test |
| Worker registry heartbeat stale | Dashboard shows unhealthy worker | Do not release jobs based on heartbeat alone; lease recovery remains authoritative |

## 25. Operational runbooks

### Runbook A — Queue backlog rising
1. Check queue depth and oldest eligible-job age.
2. Check worker heartbeat, active leases, handler duration, and worker errors.
3. Check DB pool utilization, query latency, lock waits, CPU, and storage I/O.
4. Check whether one tenant or high-priority queue dominates; inspect rate/concurrency limits.
5. Add worker replicas only if database capacity and task nature support it; increasing workers can worsen a DB bottleneck.
6. Capture benchmark/diagnostic data before changing indexes or pool size.

### Runbook B — Many lease recoveries
1. Check worker restarts/OOM events and shutdown logs.
2. Compare job timeout, lease duration, and renewal interval.
3. Inspect DB latency/network connectivity; renewals may fail during DB stalls.
4. Ensure handlers respect context and do not block renewal goroutines.
5. Do not simply extend lease time without checking how it affects recovery delay.

### Runbook C — Dead-letter spike
1. Group by task type and error code, not raw error text only.
2. Identify code/config/provider outage or malformed payload pattern.
3. Pause the affected schedule or worker task type if it is causing a retry storm.
4. Fix the handler or dependency, then replay a small sample.
5. Verify idempotency/duplicate side-effect safeguards before bulk replay.

### Runbook D — Database unavailable
1. API should fail readiness and reject writes instead of claiming acceptance.
2. Workers should pause claims/renewals and use bounded reconnect backoff.
3. Restore DB connectivity and verify schema/migrations before workers resume.
4. Allow expired leases to recover through standard flow; do not bulk reset running jobs manually without a transaction and documented reconciliation.
5. Validate accepted job IDs and job/attempt reconciliation after recovery.

### Runbook E — Suspected cross-tenant access
1. Disable/revoke the affected API key.
2. Preserve relevant access logs and audit events.
3. Reproduce with a security test, patch repository/service authorization, add a regression test for every affected route.
4. Rotate any exposed credentials and inspect payload/log retention.
5. Document incident and deploy a verified fix before re-enabling keys.

## 26. CI/CD and deployment workflow

### Pull request pipeline
1. Format/lint and `go vet`.
2. Unit tests.
3. Start PostgreSQL service/container; apply migrations and run integration tests.
4. Race detector on supported runners.
5. OpenAPI schema validation and generated-contract diff check if code generation is used.
6. Frontend typecheck/lint/build.
7. Dependency/secret scan.
8. Build API, worker, and admin binaries/container.
9. Publish test artifacts and coverage results.

### Deployment pipeline
1. Build immutable container image tagged with commit SHA.
2. Back up database before risky schema change.
3. Apply backward-compatible migration as a separate job.
4. Deploy API and worker versions compatible with schema.
5. Verify `/readyz`, queue processing, error rates, and migration status.
6. Do not automatically run destructive down-migrations in production; use forward fixes and a tested restore plan.
7. Roll back application images if needed, while preserving backward schema compatibility.

## 27. Implementation sequence and engineering tasks

### Phase 0 — Repository and design
- [ ] Create repo and add PRD/TDD/ADR folder.
- [ ] Pin Go patch version, PostgreSQL image tag, dependencies and linters.
- [ ] Add Docker Compose for PostgreSQL with health check and named volume.
- [ ] Add config validation, structured logging, request IDs, `/healthz`, `/readyz`.
- [ ] Add CI baseline.

### Phase 1 — Durable job vertical slice
- [ ] Create migrations for tenants, keys, jobs, attempts, rate limits/slots.
- [ ] Add restricted admin CLI for tenant/key provisioning.
- [ ] Add authentication and permission middleware.
- [ ] Add enqueue/get/list APIs and OpenAPI file.
- [ ] Add one sample task handler and worker poll loop.
- [ ] Implement transaction claim + attempt record + success acknowledgement.
- [ ] Add real-PostgreSQL smoke test.

### Phase 2 — Correctness and recovery
- [ ] Add canonical idempotency hashing and concurrent conflict behavior.
- [ ] Add retry classifier, backoff+jitter, attempt history and dead-letter state.
- [ ] Add worker registry heartbeats and job lease renewal.
- [ ] Add expired-lease recovery loop and stale-token protection.
- [ ] Add graceful shutdown/drain behavior.
- [ ] Add concurrent worker and kill-process tests.

### Phase 3 — Scheduler and quotas
- [ ] Add delayed job policy and validation.
- [ ] Add cron schedules, timezone validation, misfire policy, occurrence uniqueness.
- [ ] Add tenant token bucket, outstanding counter and reconciliation.
- [ ] Add execution slots and concurrency-cap tests.
- [ ] Add priority and starvation measurement.
- [ ] Add cancellation request handling.

### Phase 4 — Visibility and integration
- [ ] Add Prometheus counters/gauges/histograms.
- [ ] Add dashboard overview, job search/detail, schedules, dead letters.
- [ ] Add Python client/reference integration.
- [ ] Integrate one real workload (placement-alert bot, campus reminder, or notice scraper).
- [ ] Write operational runbooks and known limitations.

### Phase 5 — Evidence and polish
- [ ] Add load generator and machine-readable output.
- [ ] Run baseline, vary worker counts, profile bottleneck.
- [ ] Run worker-crash, DB-outage, backlog, and multi-scheduler chaos tests.
- [ ] Document before/after result with commit SHA/environment.
- [ ] Verify backup/restore, key rotation, and retention behavior.
- [ ] Review claims against evidence and cut any unproven résumé numbers.

## 28. Architecture decision records to create

Create short ADRs as decisions become concrete:

- **ADR-001 — PostgreSQL first; no Redis in v1.** Record query/throughput evidence and the trigger that would justify another backend.
- **ADR-002 — At-least-once execution.** Define why exactly-once external effects are not promised and how handlers use idempotency.
- **ADR-003 — Direct PostgreSQL worker claims.** Record trade-offs versus API-mediated claim/ack endpoints.
- **ADR-004 — Lease duration and recovery timing.** Define renewal ratio, max handler duration, and expected worst-case recovery lag.
- **ADR-005 — Strict priority in v1.** Record starvation limitation and future fairness options.
- **ADR-006 — Schedule misfire behavior.** Define `skip` and `catch_up_one`, timezones, DST, and occurrence uniqueness.
- **ADR-007 — Tenant quota implementation.** Record token bucket, outstanding counter, and slot invariants.
- **ADR-008 — Retention and replay semantics.** Define which records are retained, deleted, archived, and how replay generation works.
- **ADR-009 — API key storage and permissions.** Document hashing, issuance, revocation, rotation, and least privilege.
- **ADR-010 — Benchmark protocol.** Fix workloads, hardware reporting, test duration, percentiles, and result format.

## 29. Decisions to lock in before implementation

The following decisions are now specified and should be encoded in ADRs/tests rather than re-opened casually:

1. Idempotency keys expire after 24 hours and become reusable atomically when the old record's `expires_at` has passed. Clients should nevertheless generate a new key for each new logical submission.
2. Identical replayed submissions return `200 OK` and `Idempotent-Replay: true`; new accepted submissions return `202 Accepted`. Terminal-job retention must never be shorter than the configured idempotency TTL.
3. PostgreSQL `clock_timestamp()` is authoritative for lease creation/renewal/expiry/recovery. Ordinary creation/update timestamps may use `now()`.
4. Dynamic concurrency-limit changes use the slot `enabled` flag; never delete/disable an occupied slot until it becomes free.
5. V1 dashboard is private/demo-only with a manually entered API key held in memory for that browser tab. Public dashboard auth requires a server-side session/BFF or OIDC and is out of scope until implemented.
6. Use pinned `robfig/cron/v3` with five-field cron and IANA timezone. Add tests for spring-forward gaps and fall-back repeated local times, and document the library's actual behavior before enabling recurring schedules.
7. The API accepts a syntactically valid `task_type`; only workers that register a matching handler may claim it. The dashboard/diagnostics must show queued task types with no compatible recent worker. V1 does not auto-dead-letter solely because no worker is currently present.
8. Hard-delete terminal jobs and their attempt/audit children after 30 days in bounded transactions. Validate terminal-job retention >= idempotency TTL so a valid idempotency record cannot point at a deleted job. Do not enable retention until FK-safe deletion and schedule occurrence history behavior are covered by tests.
9. `QF_MAX_SCHEDULE_HORIZON` defaults to 365 days and defines the maximum accepted future `run_at`; past `run_at` values are accepted as immediately eligible.

No decision may weaken tenant isolation, atomic job creation, lease-token state updates, schedule occurrence uniqueness, or documented at-least-once semantics.

## 30. Definition of technically complete

V1 implementation is technically complete only when:

- Schema migrations are repeatable and tested against a real PostgreSQL version.
- All public API operations have an OpenAPI schema and tenant-authorization tests.
- Job lifecycle invariants hold under simultaneous workers and process crashes.
- Idempotency works under duplicate and concurrent submissions.
- Lease expiration recovers work and stale workers cannot overwrite newer state.
- Retry, dead-letter, replay, cancellation, delayed work, and cron policy are deterministic and documented.
- Per-tenant enqueue rate, outstanding count, and execution concurrency have concurrency-safe enforcement and reconciliation.
- Metrics and logs show operational behavior without leaking secrets or creating uncontrolled metric cardinality.
- Deployment, backup/restore, key rotation, and recovery runbooks are tested at least in a local/staging environment.
- Benchmarks are reproducible and report raw result data, not only a résumé-friendly summary.

## 31. References

- **[R1] PostgreSQL SELECT / SKIP LOCKED:** https://www.postgresql.org/docs/current/sql-select.html
- **[R2] Go release history and support policy:** https://go.dev/doc/devel/release
- **[R3] OpenAPI Specification:** https://swagger.io/specification/
- **[R4] OWASP API Security Top 10 (2023), including object-level authorization and resource consumption:** https://api-security.owasp.org/editions/2023/en/0x11-t10/
- **[R5] PostgreSQL partial indexes:** https://www.postgresql.org/docs/current/indexes-partial.html
- **[R6] Prometheus metric/label naming and cardinality recommendations:** https://prometheus.io/docs/practices/naming/
- **[R7] Amazon SQS visibility timeout and duplicate delivery note:** https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-visibility-timeout.html
- **[R8] Go Effective Go, concurrency concepts:** https://go.dev/doc/effective_go

These references were consulted on 16 October 2026. Re-check versions and release support before implementation/deployment.
