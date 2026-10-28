# QueueForge — Comprehensive Project Manual & Architecture Reference

**Document Version:** 1.0  
**Repository:** `QueueForge`  
**Architecture Style:** Modular Monolith + Independently Deployed Worker Replicas  
**Primary Datastore:** PostgreSQL 16.x / 18.x  
**Primary Language:** Go 1.27.x (with companion Python 3.12+ Client SDK)  

---

## 1. What This Project Actually Is

**QueueForge** is an enterprise-grade, distributed background job queue and recurring task scheduling engine designed from first principles to provide durable, at-least-once execution guarantees without the operational complexity or dual-state inconsistency of external cache/broker brokers like Redis, Celery, or RabbitMQ.

In modern application engineering, background processing (sending emails, processing payments, generating PDF reports, dispatching webhook notifications, and scraping feeds) is frequently offloaded to naive queues. These ad-hoc systems routinely suffer from:
1. **Silent Job Loss:** Jobs lost in volatile broker memory during ungraceful process crashes or restarts.
2. **Double Execution / State Overwrite:** Stale workers resuming after network hiccups and overwriting newer execution states.
3. **Noisy Neighbor Starvation:** High-volume tenants flooding the queue and starving latency-critical tenants.
4. **Duplicate Submission Bursts:** Client network timeouts causing retries that dispatch duplicate payments or notices.
5. **Dual-Write Inconsistency:** Enqueueing a database record and simultaneously pushing a message to Redis/RabbitMQ, where one commits and the other fails.

QueueForge eliminates these failure modes by using **PostgreSQL as the single, authoritative, relational source of truth** for all jobs, execution attempts, leases, token buckets, and cron occurrences. It coordinates concurrent workers using native PostgreSQL row-level locks (`SELECT ... FOR UPDATE SKIP LOCKED`) inside ultra-short transactions, ensuring worker execution code runs strictly outside database transaction boundaries.

---

## 2. What The System Does (Complete Feature Catalog)

### 2.1 Durable Job Submission & Validation
- **Versioned REST API:** Public routes exposed under `/v1` using standard JSON envelopes with request IDs (`X-Request-ID`).
- **Input Sanitization & Constraints:** Accepts validated payloads up to 256 KiB, validates priority (0–9, default 5), max attempts (1–10), and execution timeouts (1–3600s).
- **Atomic Persistence:** A successful `202 Accepted` response is returned **only after** the job row, idempotency record, and tenant quota updates are committed in a single PostgreSQL transaction.

### 2.2 Lock-Free Concurrent Worker Claims (`FOR UPDATE SKIP LOCKED`)
- **Direct Database Claims:** Workers poll PostgreSQL directly and execute a single atomic query using `FOR UPDATE SKIP LOCKED` on both the candidate job and an enabled tenant execution slot.
- **Zero Lock Contention:** Competing worker processes skip rows locked by peers, enabling linear horizontal scaling without deadlocks or serialization aborts.
- **Strict Out-of-Transaction Execution:** The claim transaction commits *before* user handler code executes. Database connections and locks are held only for microseconds during claim and result commit, completely preventing connection pool starvation during slow tasks.

### 2.3 Lease Tokens, Heartbeats & Stale Worker Fencing
- **Cryptographic Leases:** Every claim generates a cryptographically random UUID lease token with an explicit expiration (`clock_timestamp() + lease_duration`).
- **Periodic Lease Renewal:** Running workers renew active leases every 20 seconds (1:3 renewal ratio against a 60-second default lease).
- **Stale Worker Fencing:** Updates require `WHERE id = $1 AND lease_token = $token AND lease_expires_at > clock_timestamp()`. If a worker stalls past its lease expiry and another worker reclaims the job, the stale worker's late commit matches 0 rows, raises `ErrLeaseLost`, and safely aborts.
- **Autonomous Recovery Loop:** A dedicated background loop scans for `status = 'running' AND lease_expires_at <= clock_timestamp()` every 5 seconds, closes abandoned attempts as `lease_expired`, releases the tenant concurrency slot, and requeues the job or transitions it to `dead`.

### 2.4 Exponential Backoff with Full Jitter & Error Classification
- **Error Types:**
  - `PermanentError`: Bypasses retries and transitions directly to `dead` (e.g. malformed payload schema).
  - `RetryableError`: Default behavior for transient faults (e.g. database disconnect, third-party 503).
- **Full Jitter Formula:**
  $$\text{upper} = \min(\text{cap}, \text{base} \times 2^{\text{attempt}-1})$$
  $$\text{delay} = \text{uniform\_random}(0, \text{upper})$$
- **Persisted Due Date:** The calculated delay is stored once in `jobs.run_at`. Process restarts do not reset or alter the retry schedule.

### 2.5 Deterministic Canonical Idempotency Engine
- **Key Retention:** 24-hour tenant-scoped idempotency keys (`QF_IDEMPOTENCY_TTL`).
- **Key-Sorted Canonical JSON:** Payload JSON is canonicalized by recursively sorting dictionary keys before hashing, ensuring whitespace and property ordering variations produce identical hashes.
- **UTC & Immediate Sentinels:** If `run_at` is omitted, an explicit `"immediate"` string sentinel is hashed rather than `time.Now()`. This prevents identical retries sent seconds later from producing hash mismatches.
- **Responses:**
  - Identical retry $\rightarrow$ `200 OK`, `Idempotent-Replay: true` header, returns original job without consuming quota.
  - Conflicting payload with same key $\rightarrow$ `409 Conflict` (`idempotency_key_reused`).

### 2.6 Delayed Work & Recurring Cron Scheduling
- **Durable Delayed Work:** Jobs with future `run_at` remain `queued` until database `clock_timestamp()` passes `run_at`.
- **5-Field Cron Parsing:** Standard five-field cron expressions parsed via `robfig/cron/v3` with explicit IANA timezone support.
- **Occurrence Uniqueness:** Enforced by partial unique index `(schedule_id, scheduled_for)`. Even under 10 concurrent scheduler replicas, exactly 1 job is created per occurrence.
- **Misfire Policies:**
  - `skip`: Omits missed runs, updates `last_skipped_at`, and advances `next_run_at` to the first future occurrence.
  - `catch_up_one`: Creates exactly one job for the most recent missed occurrence, then advances cursor to future.

### 2.7 Multi-Tenant Quotas & Fairness Guardrails
- **Layer 1 — Token Bucket Rate Limiter:** PostgreSQL-backed token bucket in `tenant_rate_buckets` refilled dynamically via elapsed database time. Over-limit requests return `429 Too Many Requests` with a calculated `Retry-After` header.
- **Layer 2 — Pending Job Quotas:** Atomic counter `tenant_usage.outstanding_jobs` strictly caps non-terminal backlog per tenant (`max_pending_jobs`).
- **Layer 3 — Physical Execution Slots:** Database-backed rows in `tenant_execution_slots` cap concurrent `running` jobs (`max_concurrent_jobs`). Prevents one tenant from monopolizing worker capacity.

### 2.8 Dead-Letter Queue & Audited Manual Replay
- **Dead-Letter View:** Exhausted or permanent failures are queryable via `GET /v1/dead-letters`.
- **Audited Replay:** `POST /v1/jobs/{id}/replay` resets status to `queued`, increments `replay_generation`, resets `attempts_started = 0`, preserves all prior attempt history, and inserts an audit log into `job_audit_events`.

### 2.9 Cooperative Cancellation
- **Queued Work:** Atomically cancelled; immediately becomes ineligible for worker claims.
- **Running Work:** Sets `cancellation_requested_at`. Worker renewal checks observe the flag and cancel the Go `context.Context`, allowing handlers to shut down cooperatively.

### 2.10 Observability, Metrics & Dashboard
- **Prometheus Metrics:** Standard Prometheus scrape endpoint at `/metrics` with strictly bounded label cardinality (no job IDs, tenant IDs, or payloads in labels).
- **Structured JSON Logging:** Zero plain-text logs; all events emit JSON with request IDs, job IDs, worker IDs, and attempt numbers.
- **Operator Dashboard:** Dark-mode static SPA served at `/ui/` featuring real-time 3s polling, jobs filtering, detail drawers, cron manager, dead-letter redrive, and secure in-memory tab authentication.

### 2.11 Official Python Client SDK & Real-World Integration
- **Zero-Dependency Python SDK:** Pure Python `QueueForgeClient` using `urllib.request` with typed exceptions (`AuthenticationError`, `IdempotencyConflictError`, `RateLimitError`, `QuotaExceededError`).
- **Campus Placement Alert Bot:** Production dogfooding application [`examples/placement-alert-bot/bot.py`](examples/placement-alert-bot/bot.py) orchestrating high-priority placement alerts, idempotent retry deduplication, daily cron digests, and queue statistics queries.

---

## 3. Full Database Schema & Index Blueprint

The underlying schema consists of 8 relational tables with explicit referential integrity and check constraints:

```sql
-- 1. Tenants
CREATE TABLE tenants (
  id uuid PRIMARY KEY,
  name text NOT NULL,
  status text NOT NULL CHECK (status IN ('active', 'suspended')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

-- 2. API Keys (Cryptographic SHA-256 Storage)
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

-- 3. Tenant Limits & Token Bucket
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

CREATE TABLE tenant_usage (
  tenant_id uuid PRIMARY KEY REFERENCES tenants(id),
  outstanding_jobs bigint NOT NULL DEFAULT 0 CHECK (outstanding_jobs >= 0),
  updated_at timestamptz NOT NULL DEFAULT now()
);

-- 4. Tenant Concurrency Slots
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

-- 5. Jobs Table
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

-- 6. Execution Attempts
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

-- 7. Idempotency Keys
CREATE TABLE idempotency_keys (
  tenant_id uuid NOT NULL REFERENCES tenants(id),
  idempotency_key text NOT NULL,
  request_hash bytea NOT NULL,
  job_id uuid NOT NULL REFERENCES jobs(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, idempotency_key)
);

-- 8. Recurring Schedules
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

-- Partial Indexes
CREATE INDEX jobs_claim_idx ON jobs (priority DESC, run_at ASC, created_at ASC, id ASC) WHERE status = 'queued';
CREATE INDEX jobs_running_lease_idx ON jobs (lease_expires_at ASC) WHERE status = 'running';
CREATE UNIQUE INDEX jobs_schedule_occurrence_uq ON jobs (schedule_id, scheduled_for) WHERE schedule_id IS NOT NULL;
```

---

## 4. Limitations & Operational Trade-offs

A core value of QueueForge is honest engineering: no fabricated throughput claims or hidden architectural compromises. The v1 release carries the following explicit design limitations:

1. **At-Least-Once Delivery (No Global Exactly-Once Side Effects):**
   - If a worker crashes after dispatching an external HTTP API call or bank transfer, but *before* its success transaction commits in PostgreSQL, the recovery loop re-queues the job.
   - External handlers must be designed to be idempotent or supply third-party idempotency keys.
2. **Strict Priority Starvation Risk (ADR-005):**
   - Priority 9 jobs always claim before Priority 0 jobs. Under sustained saturation of high-priority work, low-priority tasks will wait indefinitely. 
   - *Mitigation:* Operators must partition traffic into separate queues (`notifications`, `background`, `maintenance`) or monitor queue wait histograms.
3. **Throughput Bounded by PostgreSQL ACID Transactions (ADR-001):**
   - Single-node throughput reaches ~400–1,000 enqueues/sec depending on disk I/O and fsync settings. It will not reach in-memory Redis rates of 50,000 ops/sec.
   - *Mitigation:* Scaling beyond 10,000 ops/sec requires horizontal table partitioning or introducing Kafka/Redis through a dedicated ADR.
4. **Tenant Token Bucket Serialization (TDD §13.1):**
   - Row-locking `tenant_rate_buckets` serializes enqueues for the *same* tenant. While different tenants execute concurrently, a single tenant cannot exceed ~400–500 req/s on a single thread.
5. **JSON Payloads Only (No Binary Blob Storage):**
   - Maximum payload size is capped at 256 KiB in JSONB; large files, video buffers, or binary data must be uploaded to object storage (e.g., AWS S3) and referenced by URL.
6. **No Seconds-Precision Cron:**
   - Recurring schedules support standard five-field cron (1-minute granularity). Sub-minute recurring loops are rejected by validation.
7. **Demo Dashboard Security Model (TDD §19):**
   - The v1 web dashboard stores API keys strictly in-memory per browser tab. It does not provide server-side session management, OAuth2, or OIDC login.

---

## 5. Measured Benchmark & Reliability Results

See [`docs/benchmark-reports/report.md`](docs/benchmark-reports/report.md) for the current numbers, methodology, hardware, and known limits. Headline (1-vCPU container, Postgres co-located): drain throughput **16 -> 387 jobs/s**, idle enqueue->done p50 **222 -> 6.8 ms**, real `kill -9` recovery **3.1 s** with a 3 s lease, zero duplicates/losses/leaks in every run.

Earlier numbers in this file (384 enqueues/s from a 100-job run, and "8-15 jobs/s bounded by handler work") were superseded: the 100-job run was too small to be meaningful, and the worker figure was actually a one-claim-per-tick bug (see `docs/ENGINEERING_LOG.md`, F-3).

---

## 6. Resume, Interview & Behavioural Material

Moved to [`docs/RESUME_AND_INTERVIEW.md`](docs/RESUME_AND_INTERVIEW.md) so that every number on a resume is tied to a test you can run.

---

## 7. Verification Checklist

- [x] PostgreSQL schema with foreign keys, `CHECK` constraints that make illegal job states unrepresentable, and partial indexes.
- [x] `go build ./...`, `go vet ./...`, `gofmt -l .` clean; CI derives the Go version from `go.mod`.
- [x] Unit + integration + chaos + regression tests pass under `-race` against real PostgreSQL. (Pass rate, not coverage; run `go test -cover ./...` for coverage.)
- [x] Chaos suite includes a **real SIGKILL of a separate worker process**.
- [x] One regression test per bug found in review, each shown failing on the original code first.
- [x] Python SDK with typed exceptions and tests.
- [x] 13 ADRs and 5 runbooks.
- [ ] **Deployed with real users.** The placement-alert bot is a demo integration until it runs against a deployed instance; update this line when that is true.
