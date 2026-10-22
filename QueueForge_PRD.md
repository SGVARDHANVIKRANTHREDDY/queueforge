# QueueForge — Product Requirements Document (PRD)

**Document status:** Draft for implementation  
**Version:** 1.0  
**Date:** 16 October 2026  
**Product type:** Developer infrastructure / distributed job queue and scheduler  
**Working name:** QueueForge (rename before public release if desired)  
**Primary implementation target:** Go service and worker runtime, PostgreSQL as the authoritative store  

> **How to use this document:** Treat numbered requirements and acceptance criteria as the product contract. The TDD defines one proposed implementation of that contract. If implementation evidence requires a change, update both documents and record the decision rather than silently drifting from the specification.

## 1. Executive summary

QueueForge is a self-hostable job queue and scheduling platform that lets applications submit asynchronous work through a versioned REST API and lets separately deployed worker processes execute registered task handlers. It supports delayed and recurring work, priorities, retry policies, idempotent submission, dead-letter inspection and replay, tenant-level controls, worker health, operational metrics, and a dashboard.

The first release deliberately uses PostgreSQL for durable job state, coordination, idempotency, scheduling, and rate-limit state. Workers claim work using short database transactions and row-level locking. Redis is not required for v1; it may be considered only after benchmarks identify a clear need. The platform uses **at-least-once execution semantics**, not a promise of exactly-once side effects. Workers may retry after a crash or an ambiguous outcome, so task handlers that perform external side effects must be designed to be idempotent where possible.

QueueForge is intended to be a rigorous engineering portfolio project and a useful small-team service. Its strongest proof points are correct behavior under concurrency, explicit failure semantics, repeatable benchmarks, documented operational trade-offs, and one real integration—not the raw count of features.

## 2. Problem statement

Applications commonly need work that should not run inside a user-facing request: sending reminders, processing uploaded files, sending notifications, scraping notices, generating reports, synchronizing external systems, and running recurring maintenance. Implementing this repeatedly inside every application leads to ad hoc retries, cron scripts, invisible failures, duplicated work, and poor operational visibility.

A queue service can centralize the durable acceptance, scheduling, execution lifecycle, retry policy, and observability of this work. However, a naive queue can lose work, execute a job concurrently in multiple workers, retry forever, starve tenants, or claim reliability guarantees it cannot actually provide.

QueueForge will make these behaviors explicit and testable. It will start as a single-region, PostgreSQL-backed platform, with clear extension points for future transports or storage improvements.

## 3. Product vision and principles

### 3.1 Vision

Provide a small, understandable, developer-friendly job platform that makes asynchronous work observable and recoverable without requiring a team to operate a large infrastructure stack.

### 3.2 Product principles

1. **Durability before speed claims.** A successful enqueue response means the job and its idempotency record were committed to PostgreSQL.
2. **Explicit semantics.** Explain delivery, retry, cancellation, scheduling, and idempotency guarantees in the documentation and API.
3. **Fail visibly.** Failed and exhausted jobs remain inspectable; errors are not silently discarded.
4. **Short coordination transactions.** Never hold a database transaction open while user task code is executing.
5. **Secure by default.** Tenant identity comes from authenticated credentials, not an untrusted request field. The system never runs arbitrary user-supplied code.
6. **Measure before adding infrastructure.** PostgreSQL is the initial source of truth; additional components require evidence.
7. **Useful without the UI.** API, worker runtime, SDK/client contract, and observability are primary; the dashboard is an operator aid.
8. **Honest claims.** Benchmark numbers must include workload, topology, duration, and failure conditions.

## 4. Goals, non-goals, and scope

### 4.1 Product goals

- **G1 — Durable submission:** accept jobs through a stable REST API and persist them before acknowledging success.
- **G2 — Concurrent processing:** support multiple independent worker processes safely claiming work from the same PostgreSQL database.
- **G3 — Failure recovery:** retry eligible failures and recover work whose worker lease expires.
- **G4 — Scheduling:** support delayed jobs and recurring cron-based schedules.
- **G5 — Tenant isolation:** scope jobs, schedules, idempotency keys, API keys, and administrative operations to a tenant; enforce tenant quotas.
- **G6 — Operational visibility:** expose queue depth, job history, failure/retry details, worker health, and Prometheus metrics.
- **G7 — Demonstrable reliability:** include automated concurrency, crash-recovery, overload, and database-interruption tests.
- **G8 — Real adoption:** make integration straightforward enough that at least one real application can use QueueForge.

### 4.2 Non-goals for v1

- Exactly-once execution or a universal guarantee that external side effects happen only once.
- Multi-region active-active operation, cross-region replication, or global ordering.
- A workflow/DAG engine, saga coordinator, Temporal-style event history, or long-running human approval workflow.
- Arbitrary code upload, remote code execution, user-provided shell commands, or a generic plugin marketplace.
- Binary/object payload storage; payloads are JSON in PostgreSQL for v1.
- Strict FIFO ordering across all jobs. Priority and eligibility take precedence.
- Guaranteed preemption of a running handler when a job is cancelled.
- Mandatory gRPC, Redis, Kafka, or Kubernetes in v1.
- A public multi-customer SaaS with billing, self-service sign-up, legal/compliance attestations, and on-call support commitments.
- A claim of production-grade availability or a fixed maximum throughput before benchmarks validate it.

### 4.3 Scope by release

| Capability | V1 release requirement | Later enhancement |
|---|---|---|
| REST API to submit/query/cancel jobs | Required | API rate-limit policies per endpoint |
| PostgreSQL durable queue and safe claiming | Required | Partitioning or alternate storage only if measured |
| Go worker runtime with registered handlers | Required | Language-neutral worker protocol |
| Retries, exponential backoff with jitter | Required | Per-error custom retry policies |
| Idempotent submission keys | Required | SDK helpers for more languages |
| Worker leases and crash recovery | Required | More advanced lease diagnostics/fencing integrations |
| Delayed jobs | Required | Large-volume timing wheel or separate timer service if needed |
| Cron schedules | Required | Seconds-level cron and advanced misfire strategies |
| Priority 0–9 | Required | Weighted fairness / starvation prevention policies |
| Per-tenant enqueue rate, pending-job and concurrency limits | Required | Dynamic quotas, billing-based plans |
| Dead-letter inspection and manual replay | Required | Bulk replay, automatic redrive policies |
| API-key authentication and multi-tenancy | Required | OIDC/SSO and organization RBAC |
| Metrics and health endpoints | Required | Tracing and log correlation via OpenTelemetry |
| Operator dashboard | Required, deliberately simple | Rich analytics and custom dashboards |
| Python submission SDK | Small official client or reference implementation | Full async SDK, additional language SDKs |
| gRPC | Not in v1 | Consider if justified by consumers |
| Redis | Not in v1 | Add only after benchmark-backed ADR |
| Deployment via Docker Compose + one VPS | Required | Helm/Kubernetes production deployment |

## 5. Target users and personas

### P1 — Application developer

Builds a web application and wants to submit slow or recurring tasks without maintaining a bespoke background-job subsystem. Needs a simple API, predictable payload format, documentation, and job status lookup.

### P2 — Worker/task developer

Implements task handlers in Go and needs typed registration conventions, context cancellation, retry classification, logs, and a local way to run workers against development PostgreSQL.

### P3 — Operator / maintainer

Deploys QueueForge, provisions tenants and API keys, watches queue depth and failure rates, diagnoses stuck work, and replays dead-letter jobs. Needs health checks, metrics, safe controls, and clear runbooks.

### P4 — Tenant administrator

Manages an application's credentials and sees only that tenant's jobs, schedules, and queue statistics. Must not be able to read or modify another tenant's records.

## 6. Core user journeys

### Journey A — Submit and observe a job

1. A client calls `POST /v1/jobs` with a task type, JSON payload, queue, and optional idempotency key.
2. The API authenticates the API key, derives tenant identity, validates limits, and commits the job and idempotency record in one transaction.
3. The API returns `202 Accepted` with a job ID and status URL.
4. A worker registered for that task type claims the job and executes it.
5. The client polls `GET /v1/jobs/{id}` or uses the dashboard to see the final state and attempt history.

### Journey B — Recover from a worker crash

1. A worker claims a job and receives a lease token and expiry.
2. The worker is terminated before it records a result.
3. The lease expires. A recovery loop records the attempt outcome as lease-expired and schedules a retry or marks the job dead, according to the policy.
4. A worker claims the job again when it becomes eligible.
5. The job history displays the original attempt and the recovery attempt. The system does not claim that an external side effect could not have occurred during the original attempt.

### Journey C — Use a recurring schedule

1. A tenant creates a cron schedule with a task type, payload, timezone, retry policy, and misfire policy.
2. The scheduler claims due schedule records in a short transaction.
3. It inserts one job per intended occurrence and advances the schedule's next-run time in the same transaction.
4. A uniqueness constraint ensures that multiple scheduler instances cannot create duplicate jobs for the same schedule occurrence.
5. The dashboard shows the schedule, upcoming occurrence, last generated job, and pause state.

### Journey D — Resolve a permanently failing job

1. A handler returns a permanent error or the job exhausts its configured attempts.
2. The job enters the `dead` state and remains available for inspection.
3. An authorized tenant administrator inspects the sanitized error details and attempts.
4. The administrator may replay the job as a new execution after correcting the underlying issue. Replay must preserve audit history and apply a documented payload/key policy.

## 7. Domain model and terminology

- **Tenant:** an isolation and quota boundary representing one application/team.
- **API key:** a secret credential scoped to one tenant and a set of permissions.
- **Job:** one unit of asynchronous work with a payload, task type, queue, schedule time, priority, retry policy, and execution history.
- **Task type / handler:** a named function registered in a trusted worker binary. QueueForge does not execute user-uploaded code.
- **Queue:** a logical grouping used for routing and metrics. V1 queue names are selected from an operator-configured global allowlist (initially `default`, `notifications`, `background`, `maintenance`), not independent broker processes or arbitrary tenant-created names.
- **Attempt:** one actual worker execution claim, including its start/end and outcome.
- **Lease:** a time-limited ownership claim represented by a random token and expiration timestamp. Expired leases permit recovery; they do not prove that old code stopped running.
- **Idempotency key:** a tenant-scoped client token used to make repeat submissions return the original job when the semantic request is the same.
- **Schedule:** a recurring cron definition that generates individual jobs.
- **Dead-letter job:** a job whose retry budget is exhausted or which has been classified as permanently failing.
- **At-least-once execution:** a job can execute more than once after failures or ambiguous outcomes. The system aims not to discard an accepted job silently within the documented failure model.

## 8. Job lifecycle and business rules

### 8.1 States

V1 persisted states are:

- `queued`: persisted and eligible once `run_at <= now()`; includes delayed work.
- `running`: claimed by one lease token and one worker.
- `succeeded`: handler returned success and the result/state transition was committed.
- `dead`: automatic retries are exhausted or a permanent failure is recorded.
- `cancelled`: queued work was cancelled before execution, or a running handler acknowledged a cancellation request before finishing.

A separate cancellation-request timestamp/flag is used for running jobs. It is not a terminal state by itself.

### 8.2 Legal transitions

| From | To | Cause |
|---|---|---|
| `queued` | `running` | Worker atomically claims eligible job and reserves tenant concurrency slot |
| `queued` | `cancelled` | Authenticated tenant requests cancellation before claim |
| `running` | `succeeded` | Current lease owner commits successful result |
| `running` | `queued` | Retryable handler error or expired lease with attempts remaining; `run_at` moves to retry time |
| `running` | `dead` | Permanent error or retry budget exhausted |
| `running` | `cancelled` | Worker sees cancellation request and ends handler cooperatively |
| `dead` | `queued` | Authorized manual replay/requeue action, with an audit record and documented attempt reset policy |

Terminal states do not silently transition back to queued. Replay is an explicit auditable action. All state changes must be conditional on the current state and, for worker-originated updates, the current lease token.

### 8.3 Delivery and acknowledgement semantics

- The API only returns successful acceptance after a database commit.
- A worker's success acknowledgement is the durable `running -> succeeded` transaction, not merely the return of a function.
- If the process crashes after an external side effect but before the completion transaction commits, the job can be retried and the effect may occur again.
- Therefore: **at-least-once execution with idempotency support; not exactly-once side effects**.
- A stale worker with an expired/replaced lease token cannot update job state as if it still owns the job.
- The lease token protects QueueForge state. It cannot prevent stale code from continuing a remote side effect unless that destination also supports idempotency/fencing.

## 9. Functional requirements

Priority definitions: **P0** blocks V1; **P1** is required for a complete first public demo but may follow the core queue; **P2** is future work.

### FR-01 — Job submission (P0)

The API shall accept a validated job request with `task_type`, JSON `payload`, optional `queue`, `priority`, `run_at`, `max_attempts`, execution timeout, and optional idempotency key.

Acceptance criteria:
- A valid request produces a UUID job ID and `202 Accepted` response.
- Payload and validated job metadata are committed transactionally before the response is returned.
- Invalid JSON, unknown fields where strict decoding is enabled, unsupported task types only when a server-side policy requires registry validation, invalid timestamps, over-limit payloads, and out-of-range options return a structured 4xx error.
- Database failures return a 5xx error and never claim that the job was accepted.
- Each job is bound to the tenant derived from the API key; the client cannot select a different tenant through request JSON.

### FR-02 — Job retrieval and listing (P0)

Clients shall retrieve one job by ID and list jobs by bounded filters, status, queue, task type, creation-time range, and pagination cursor.

Acceptance criteria:
- Tenant scoping applies to every query.
- Unknown and cross-tenant job IDs must not disclose whether another tenant's job exists; respond consistently (normally `404`).
- Pagination is bounded and stable enough for dashboard use; do not allow unbounded result sets.
- Public job responses omit lease tokens, internal worker secrets, and sensitive stack traces.

### FR-03 — Worker execution (P0)

Workers shall poll PostgreSQL and claim jobs only for task types registered in the trusted worker process.

Acceptance criteria:
- Multiple workers can run concurrently without the same unexpired lease being successfully assigned to two workers by QueueForge.
- Claiming and state change happen in a short transaction.
- Task code executes outside the database transaction.
- A missing handler has an explicit outcome (configuration/permanent failure or queue-specific routing), not an infinite invisible retry loop.
- Handler timeouts and process shutdown use contexts and bounded graceful-shutdown behavior.

### FR-04 — Retry policy and backoff (P0)

The system shall support `max_attempts`, exponential backoff, jitter, retryable errors, and permanent errors.

Default policy for first release: maximum 5 total attempts (including the first), base delay 1 second, cap 15 minutes, full jitter. Defaults must be configurable and be returned in documentation.

Acceptance criteria:
- A failure is recorded in the attempt history before the next attempt becomes eligible.
- Retry time is persisted; process restarts do not reset the retry delay.
- An attempt that fails due to worker lease expiry consumes an attempt.
- A permanent error moves the job to `dead` without using the remaining automatic retries.
- Once total attempts are exhausted, the job becomes `dead` and is not automatically claimed again.

### FR-05 — Idempotent submission (P0)

The API shall support tenant-scoped idempotency keys with a configurable retention window; initial default: 24 hours.

Acceptance criteria:
- Repeating the same key and semantically identical request returns the original job ID with `200 OK` and `Idempotent-Replay: true`; it does not create a second job.
- Repeating the same key with a different semantic request returns `409 idempotency_key_reused`.
- Concurrent identical submissions produce one durable job.
- Key handling is atomic with job creation.
- The documentation distinguishes idempotent submission from idempotent task execution.

### FR-06 — Delayed jobs (P0)

A job may have `run_at` in the future.

Acceptance criteria:
- Workers do not claim a job before its persisted due time.
- Timestamps are stored as `timestamptz` and normalized to UTC internally.
- The API rejects `run_at` values beyond the configured future scheduling horizon. A timestamp already in the past is accepted as immediately eligible; all stored timestamps are normalized to UTC.
- Scheduler/poller restarts do not lose delayed jobs.

### FR-07 — Recurring cron schedules (P1)

Tenants shall create, inspect, pause, resume, update, and delete/disable recurring schedules.

Acceptance criteria:
- V1 supports standard five-field cron expressions; seconds are not supported.
- Each schedule has an explicit IANA timezone and one of the supported misfire policies (`skip` or `catch_up_one`).
- Schedule definition and job generation are transactional.
- A unique schedule occurrence cannot generate two jobs when multiple scheduler instances run.
- Updates affect future occurrences; already generated jobs remain independent jobs.
- Invalid expressions and invalid timezone identifiers are rejected with useful validation errors.
- Daylight-saving behavior is documented and tested.

### FR-08 — Priorities (P1)

Jobs support integer priorities 0–9, with 9 highest and default 5.

Acceptance criteria:
- Eligible jobs are selected in deterministic order: priority descending, then due time, creation time, and ID.
- Priority is not advertised as a hard completion-time guarantee.
- The documentation states that strict priority may starve low-priority jobs; a fairness policy can be added later.

### FR-09 — Tenant quotas and rate limiting (P1)

The system shall enforce per-tenant enqueue rate, maximum outstanding jobs, and maximum concurrent executions.

Acceptance criteria:
- API submission rate limits are shared across API replicas (not only in process memory).
- Exceeded request-rate limits return `429 Too Many Requests` with a documented retry hint where available.
- Outstanding-job quota is checked transactionally with enqueue.
- Worker claiming reserves a tenant concurrency slot atomically with job state change.
- One tenant at its concurrency ceiling does not permanently block eligible work belonging to another tenant.
- Limits are configurable per tenant and have safe platform defaults.

### FR-10 — Worker heartbeat and lease recovery (P0)

Workers shall report process liveness and renew active job leases.

Acceptance criteria:
- A worker heartbeat updates a worker registry row with last-seen time and metadata that excludes secrets.
- Lease renewals require the current lease token and a still-valid lease; an already expired lease cannot be resurrected by a late worker.
- Recovery atomically closes the abandoned attempt, clears its concurrency reservation, and requeues the job or marks it dead according to retry rules.
- Heartbeat absence is an operational signal; lease expiry is the authoritative criterion for job recovery.
- Lease duration and renewal interval are configurable with validation that avoids obviously unsafe combinations.

### FR-11 — Dead-letter queue (P0)

Jobs that exhaust retries or fail permanently shall be visible through a dead-letter view/API.

Acceptance criteria:
- Operator/tenant can inspect safe error summary, timestamps, attempt count, task type, queue, and replay eligibility.
- A replay is an explicit audited operation; it does not erase prior attempt history.
- Replay policy says whether payload is reused, whether attempt count resets for the new run, and whether an idempotency key is retained or replaced. Recommended V1 behavior: replay the same job record to `queued`, preserve old attempts, reset the automatic attempt counter into a new replay generation, and require a distinct replay audit event.
- Tenant isolation and permissions apply to dead-letter operations.

### FR-12 — Cancellation (P1)

Clients may cancel a queued job. For running jobs, cancellation is cooperative and best-effort.

Acceptance criteria:
- Queued jobs become cancelled atomically and are no longer eligible for claims.
- Running jobs receive a cancellation-request flag; workers propagate it to the handler context on renewal/check.
- The API explains that external effects already performed cannot be rolled back and some handlers may ignore cancellation.
- Cancellation and success races result in one well-defined committed terminal state and a corresponding audit/history event.

### FR-13 — Observability (P0)

The system shall expose Prometheus metrics, structured logs, health/readiness endpoints, and per-job attempt history.

Acceptance criteria:
- Metrics include enqueue count/rate, claims, job outcomes, retries, lease recoveries, current queue depth, execution duration, enqueue-to-start wait, API request latency, DB pool state, and worker heartbeat age.
- Metric labels are bounded-cardinality (e.g. queue, outcome, route); never label metrics by job ID, tenant ID, API key, or arbitrary task payload.
- Liveness indicates the process is alive; readiness indicates required dependencies are available.
- Logs include request/correlation IDs, job ID, tenant-safe identifiers where necessary, worker ID, attempt number, and outcome, but never API-key secrets or complete sensitive payloads.

### FR-14 — API key authentication and authorization (P0)

All business endpoints shall require API-key authentication, except explicitly configured liveness endpoints and restricted internal metrics endpoints.

Acceptance criteria:
- API keys are high-entropy, shown to the creator only once, stored only as cryptographic hashes plus a non-secret prefix/ID, and can be revoked.
- Tenant ID and permissions are derived from the stored key record.
- Each job/schedule read or mutation performs object-level tenant authorization.
- Rate limiting and input-size limits reduce abuse risk.
- Administrative provisioning is a separate operator-only workflow and cannot be invoked using an ordinary tenant API key.

### FR-15 — Multi-tenancy (P0)

The platform shall isolate tenant-owned data, quotas, API keys, schedules, job history, and dead-letter operations.

Acceptance criteria:
- Every tenant-owned table has a tenant relationship or is reachable through a tenant-owned parent.
- Repository methods require tenant context for public API reads and writes.
- Cross-tenant access tests cover guessed IDs, pagination, schedule IDs, cancellation, replay, and statistics.
- Workers execute trusted handler code; they do not trust request-supplied tenant IDs inside payloads for authorization.

### FR-16 — Dashboard (P1)

Provide a small web interface for overview, jobs, job details, schedules, dead letters, and operational health.

Acceptance criteria:
- The dashboard is usable on desktop and mobile widths and has clear empty/loading/error states.
- It displays queue depth, recent outcomes, retry/recovery activity, waiting time, and worker heartbeat status.
- Tenant UI calls the API with tenant-scoped credentials; secrets are not embedded in the frontend bundle.
- Destructive actions such as replay and cancellation require confirmation and visibly report results.
- Dashboard numbers are derived from the same source of truth/metrics as the API and are not presented as exact if they are sampled or eventually refreshed.
- V1 dashboard is private/demo-only. A manually entered API key is held in browser memory for the active tab and is never persisted to local storage; a public multi-user dashboard requires a server-side session/BFF or OIDC and is out of scope for V1.

### FR-17 — Python client / integration example (P1)

Provide a lightweight Python client or reference example for submission and status retrieval.

Acceptance criteria:
- Supports base URL, API key, connection/read timeouts, JSON payloads, idempotency keys, and clear typed errors.
- Retries only safe operations automatically. For POST submission, it reuses the exact same idempotency key on a retry.
- A documented example dogfoods the queue for placement notices, campus reminders, or another genuine workload.

### FR-18 — Documentation and operations (P0)

Deliver an API reference, architecture overview, local setup instructions, configuration reference, limitations, failure-mode documentation, and runbooks.

Acceptance criteria:
- A new contributor can start PostgreSQL, apply migrations, create a tenant/key, run the API and worker, submit a sample job, and observe completion by following the README.
- Every configuration variable has type, default, sensitivity classification, and validation behavior documented.
- Benchmark results include environment and workload details.
- No unmeasured throughput or zero-loss statement is presented as a production guarantee.

## 10. API contract summary

The full API schema is maintained in OpenAPI. The API is versioned under `/v1` and uses JSON. Business responses return structured error objects with a stable machine-readable `code`, a human-safe `message`, and a request ID.

| Method and route | Purpose | Typical permission |
|---|---|---|
| `POST /v1/jobs` | Enqueue a job | `jobs:write` |
| `GET /v1/jobs/{job_id}` | Read job and safe execution summary | `jobs:read` |
| `GET /v1/jobs` | Filter/paginate tenant jobs | `jobs:read` |
| `POST /v1/jobs/{job_id}/cancel` | Cancel queued job / request running cancellation | `jobs:write` |
| `POST /v1/schedules` | Create recurring schedule | `schedules:write` |
| `GET /v1/schedules` | List schedules | `schedules:read` |
| `GET /v1/schedules/{schedule_id}` | Read schedule | `schedules:read` |
| `PATCH /v1/schedules/{schedule_id}` | Pause/resume/update allowed fields | `schedules:write` |
| `DELETE /v1/schedules/{schedule_id}` | Disable future occurrences | `schedules:write` |
| `GET /v1/dead-letters` | List dead jobs | `jobs:read` or `deadletters:read` |
| `POST /v1/jobs/{job_id}/replay` | Explicit replay | `deadletters:write` |
| `GET /v1/queues/{queue}/stats` | Queue statistics | `metrics:read` |
| `GET /healthz` | Process liveness | Public/configurable |
| `GET /readyz` | Dependency readiness | Internal/monitoring |
| `GET /metrics` | Prometheus scrape endpoint | Private network or metrics credential |

### 10.1 Enqueue request example

```json
{
  "task_type": "placement_notice.send",
  "queue": "notifications",
  "payload": {
    "notice_id": "notice_123",
    "recipient_group": "cse-2028"
  },
  "priority": 5,
  "run_at": "2026-10-16T13:00:00Z",
  "max_attempts": 5,
  "timeout_seconds": 300
}
```

`Idempotency-Key: <client-generated opaque key>` is sent as a header. The `tenant_id` is intentionally not accepted in the public request body.

### 10.2 Successful response example

```json
{
  "id": "7d3851d5-654c-42da-a812-0b2cd0c9068e",
  "status": "queued",
  "created_at": "2026-10-16T12:30:00Z",
  "run_at": "2026-10-16T13:00:00Z",
  "status_url": "/v1/jobs/7d3851d5-654c-42da-a812-0b2cd0c9068e"
}
```

### 10.3 Error envelope example

```json
{
  "error": {
    "code": "idempotency_key_reused",
    "message": "The idempotency key was already used for a different request.",
    "request_id": "req_01J..."
  }
}
```

### 10.4 HTTP status conventions

- `202` — accepted asynchronous operation.
- `200` — successful read/update action with a response body.
- `204` — successful action with no body where appropriate.
- `400` — malformed input.
- `401` — absent/invalid credentials.
- `403` — authenticated but missing permission.
- `404` — resource absent or not visible to this tenant.
- `409` — idempotency conflict or invalid state transition.
- `413` — payload/request exceeds configured size.
- `422` — structurally valid JSON but semantically invalid values.
- `429` — tenant/platform rate or quota exceeded.
- `500` — unexpected application error.
- `503` — required dependency unavailable / API not ready.

## 11. Initial limits and defaults

These are proposed safe defaults for the first implementation, not universal capacity claims. Make them configurable and validate them at startup.

| Setting | Initial default | Notes |
|---|---:|---|
| Maximum request body | 1 MiB | Reject oversized requests before decoding into memory. |
| Maximum JSON job payload | 256 KiB | Store structured JSON only. |
| Maximum job result | 64 KiB | Larger outputs should be stored externally by the handler and represented by a reference. |
| Queue names | Global allowlist | Initial names: `default`, `notifications`, `background`, and `maintenance`; no arbitrary tenant-created names. |
| Priority range | 0–9 | 9 highest; default 5. |
| Default maximum attempts | 5 total | Includes the initial attempt. |
| Retry base delay | 1 second | Exponential delay with full jitter. |
| Retry delay cap | 15 minutes | Cap before jitter. |
| Default handler timeout | 5 minutes | A job can set a smaller/larger value within the configured maximum. |
| Default lease duration | 60 seconds | Renewed more frequently while executing. |
| Lease renewal interval | 20 seconds | Must be materially shorter than lease duration. |
| Idempotency record retention | 24 hours | Same key can be reused only after expiry; document this. |
| Terminal job retention | 30 days | Attempt/audit history shares this V1 retention window to preserve referential integrity. |
| Attempt-history retention | 30 days | Same window as terminal jobs in V1; child records are purged safely with their parent. |
| Default tenant enqueue rate | 60 requests/second, burst 120 | Demonstration default; adjustable per tenant. |
| Default max concurrent executions | 4 per tenant | Enforced through database-backed slots. |
| Cron precision | 1 minute | Five-field cron; seconds not accepted. |
| Schedule misfire grace | 30 seconds | Separates normal polling lag from catch-up/skip policy. |
| Maximum active schedules | 100 per tenant | Paused schedules count; disabled/deleted schedules do not count. |
| Maximum pending jobs | 10,000 per tenant | Queued + running work; enforced transactionally. |
| Maximum future scheduling horizon | 365 days | Reject `run_at` values beyond this horizon; past times are immediately eligible. |
| Maximum per-attempt timeout | 3,600 seconds | Deployment upper bound; default handler timeout is 300 seconds. |
| Pagination limit | 100 records | Default 25; hard cap 100. |

Before a public release, tune these defaults using test results on the intended deployment. The project must document how changing job retention interacts with idempotency, recurrence, and audit history.

## 12. Non-functional requirements

### NFR-01 — Reliability

- Job creation, idempotency record creation, schedule occurrence generation, and claim state changes are transactional.
- Automated tests simulate process crashes at defined points and reconcile expected records.
- Recovery guarantees are stated against a defined failure model: process crashes and temporary connection interruptions with PostgreSQL eventually returning. Hardware loss, database corruption, operator deletion, storage exhaustion, and external side-effect rollback are not silently included in a “zero loss” claim.

### NFR-02 — Performance and scale

- Include a reproducible load generator and report enqueue throughput, completion throughput, p50/p95/p99 latency, error rate, CPU/RAM, DB connections, queue depth, and test duration.
- Establish performance baselines before optimization.
- Initial non-binding benchmark hypothesis: sustain 100 enqueue requests/second for five minutes with a no-op/small-payload workload and report p95/p99 latency and error rate. Passing this hypothesis is not a substitute for fault tests; missing it triggers profiling and documentation rather than fabricated claims.
- Benchmark multiple worker counts and identify saturation behavior. Do not put high-cardinality tenant/job identifiers into Prometheus labels.

### NFR-03 — Security

- TLS for public network deployment; database connections use TLS where supported by the hosting environment.
- Secrets are configured through environment variables/secret files and never committed.
- API keys are random, scoped, hash-stored, revocable, and shown only once.
- Validate every object-level authorization boundary and protect against cross-tenant access.
- Enforce request/payload limits, rate limits, timeouts, and safe error responses.
- Do not execute arbitrary commands from payloads. Task types map to handlers that are installed in a trusted worker binary.

### NFR-04 — Maintainability

- Separate API, domain/state machine, PostgreSQL repository, worker runtime, scheduler, metrics, and dashboard concerns.
- Database schema changes use versioned migrations and are tested from an empty database and the previous migration version.
- Public API changes are versioned; OpenAPI is checked in and validation is in CI.
- Lint, unit tests, integration tests, race detection, vulnerability checks, and container build run in CI.

### NFR-05 — Observability

- Structured logs with a request ID and job/attempt context.
- Prometheus counters, gauges, and histograms use consistent units and bounded labels.
- `/healthz` must not reveal secrets. `/readyz` verifies the dependencies necessary to accept/process work.
- Provide alerts/examples for stale workers, old queued jobs, rising dead-letter count, and high DB pool utilization.

### NFR-06 — Usability and developer experience

- Local setup is possible with Docker Compose and documented commands.
- A sample handler and end-to-end smoke test are included.
- Errors explain corrective action without leaking sensitive data.
- The dashboard remains intentionally simple and works without a complex frontend backend-for-frontend.

### NFR-07 — Portability

- API and worker run as containerized processes on Linux, including a small VPS.
- Configuration is externalized.
- PostgreSQL is the only required stateful service in V1; Prometheus/Grafana are optional local observability services and Redis is not required.

## 13. Dashboard requirements

### 13.1 Overview

Display queued/running/succeeded/failed-dead counts for a selectable interval, enqueue and completion rates, p95 waiting/execution times, retry counts, expired leases, and last-seen worker instances. Clearly identify whether a metric is instantaneous, windowed, or approximate.

### 13.2 Jobs explorer

Filter by status, queue, task type, time range, and text/job ID. Paginate server-side. Do not expose the complete JSON payload by default; show it with a clearly marked expandable view and avoid logging it to analytics.

### 13.3 Job detail

Show status timeline, schedule/run time, sanitized payload, attempt list, duration, safe error summary, worker ID, next retry time, and cancellation/replay controls based on permissions.

### 13.4 Schedules

List cron expression, timezone, next run, last generated job, pause status, and misfire policy. Editing a schedule must clarify that already generated jobs are not retroactively modified.

### 13.5 Dead letters

Display failed terminal jobs and allow an authorized user to inspect attempts and explicitly replay them. Confirm replay and record the action.

### 13.6 Error and empty states

Provide clear states for no jobs, no worker heartbeat, API/database unavailable, permission denial, stale dashboard data, and request failure. Never imply the queue is healthy merely because the dashboard loaded.

## 14. Security, privacy, and abuse cases

- **Cross-tenant object access:** every endpoint receiving a job/schedule ID must enforce tenant ownership in the query or transaction.
- **Credential theft:** API keys are high entropy, hash-stored, scoped, revocable, and never logged.
- **Unbounded resource use:** payload-size limits, pagination caps, enqueue token bucket, pending-job quotas, execution concurrency slots, job timeout, and retry caps are enforced.
- **Payload data exposure:** payloads and results can contain sensitive data. The API should expose only tenant-scoped records; logs should store summaries, not whole payloads by default. Operators must document retention and backup handling.
- **Task-type abuse:** user payloads choose only from handler names accepted by trusted worker code; payloads never contain executable modules or arbitrary shell strings that the platform runs directly.
- **Secret leakage in error messages:** sanitize stack traces in tenant-facing responses; keep detailed diagnostics in restricted logs.
- **Metrics leakage:** metrics are private or protected; do not put tenant identifiers or secrets in labels.
- **Replay abuse:** manual replay requires permission, is rate-limited, and is audited.
- **Cron abuse:** cap number of schedules and validate expression/timezone; avoid unbounded catch-up bursts.
- **Dependency security:** pin dependencies, run vulnerability scanning, update supported runtime versions, and document how to rotate credentials.

## 15. Failure and recovery expectations

| Failure | Expected behavior | Evidence required |
|---|---|---|
| API exits after DB commit but before returning response | Client may retry using same idempotency key and receives original job | API integration test |
| Two workers contend for the same queue | Each job has at most one current valid lease in DB | Concurrent integration test with many workers |
| Worker dies while running | Lease expires; attempt is closed and job is retried or dead-lettered | Kill-process test and DB reconciliation |
| Worker performs side effect then dies before success commit | Job may execute again; consumer idempotency mitigates duplicate side effect | Documented test using idempotent sample handler |
| Database temporarily unavailable | API fails closed; workers do not pretend work succeeded; reconnect/backoff occurs | Fault-injection test |
| Lease renewal arrives after expiration | Renewal is rejected; stale worker cannot commit job state | Unit/integration test |
| Scheduler replicas process same occurrence | One job per schedule occurrence due unique constraint + transaction | Multi-scheduler concurrency test |
| Tenant reaches concurrency ceiling | Additional work for that tenant remains queued while other tenant work proceeds | Multi-tenant integration test |
| Tenant submits too quickly | API returns 429; shared limiter state applies across API replicas | Rate-limit test |
| Retry budget exhausted | Job reaches `dead`, and attempts remain inspectable | Retry-policy test |
| User requests cancellation while running | Request is propagated cooperatively; no claim that external effects are undone | Race test and docs |
| PostgreSQL disk/connection pool saturated | Readiness/metrics expose problem; no false successful acknowledgement | Load/fault test where feasible |

## 16. Release acceptance checklist

V1 may be called **demo-ready** only when all P0 requirements pass, integration tests run against PostgreSQL, tenant-isolation tests pass, and a documented end-to-end demo works. It may be called **reliability-demonstrated** only after the crash, concurrent-claim, database-interruption, schedule-deduplication, and retry exhaustion tests pass and benchmark conditions are published. It should not be called production-ready simply because the dashboard and deployment work.

- [ ] Fresh environment can start the service using documented steps.
- [ ] Migrations work from empty DB.
- [ ] API key creation/revocation and tenant scoping work.
- [ ] Duplicate and conflicting idempotency tests pass, including concurrent submissions.
- [ ] Multiple workers claim jobs without conflicting active leases.
- [ ] Lease expiry and stale worker updates are tested.
- [ ] Retry backoff, permanent errors, and dead-letter behavior are tested.
- [ ] Delayed jobs and recurring schedule uniqueness are tested.
- [ ] Per-tenant rate, pending, and concurrent-execution limits are tested.
- [ ] Metrics avoid high-cardinality identifiers and health endpoints behave correctly.
- [ ] Dashboard actions are tenant-scoped and authorized.
- [ ] Dependency vulnerability check and race detector pass.
- [ ] Load test outputs machine-readable raw results and a methodology report.
- [ ] One genuine application integrates with the queue.
- [ ] Limitations, runbooks, and design decisions are documented.

## 17. Milestones and work breakdown

### M0 — Design and local foundations

Deliver: reviewed PRD/TDD, repository skeleton, configuration loader, Docker Compose PostgreSQL, migrations, CI baseline, request ID/logging conventions.

### M1 — Durable queue vertical slice

Deliver: enqueue/get/list API, tenant/auth skeleton, PostgreSQL schema, one handler, worker claiming, successful state transition, smoke test.

### M2 — Correctness under failure

Deliver: idempotency, retries/jitter, attempt history, leases, lease recovery, permanent errors, dead-letter listing/replay, concurrent worker tests.

### M3 — Scheduling and quotas

Deliver: delayed jobs, cron schedules, occurrence uniqueness, pause/resume, tenant rate limiter, pending quota, concurrency slots.

### M4 — Observability and UI

Deliver: metrics, worker registry, health/readiness, basic dashboard, API docs and runbooks.

### M5 — Performance and dogfooding

Deliver: load generator, reproducible benchmark report, deliberate failure tests, one real client integration, before/after optimization report.

## 18. Product risks and mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Feature scope becomes too large | Core correctness is never finished | Build vertical slices; P0 first; postpone gRPC/Redis/advanced UI. |
| Incorrect recovery semantics | Duplicate or stranded execution | Model state machine explicitly; lease-token conditional updates; fault tests. |
| Tenant isolation bug | Data exposure or unwanted job control | Tenant context mandatory in repository APIs; cross-tenant security tests. |
| Scheduler duplicates jobs | Duplicate scheduled effects | Unique occurrence constraint and atomic schedule advancement. |
| Strict priority starves jobs | Poor fairness | Document limitation; benchmark distribution; implement weighted fairness only if needed. |
| PostgreSQL becomes bottleneck | Throughput ceiling | Capture query plans, pool metrics and contention; optimize before adding Redis. |
| Benchmark claims become misleading | Loss of project credibility | Publish environment, workload and raw output; report limitations. |
| Overly broad task payloads/logging leak private data | Security/privacy issue | Size limits, tenant scoping, redaction, retention, payload-free metric labels. |
| Learning Go and distributed systems simultaneously | Slower implementation/debugging | Learn language foundations; implement one simple path before adding concurrency complexity. |

## 19. Definition of done for every feature

A feature is complete only when it has:

1. A documented contract, including failure behavior.
2. Unit tests for validation and business rules.
3. PostgreSQL integration tests for transactional behavior where relevant.
4. Authorization tests for all tenant-owned resources.
5. Logs/metrics appropriate to operational diagnosis without sensitive leakage.
6. API/OpenAPI and user documentation updates.
7. Migration and rollback/forward-recovery notes if schema changed.
8. A review showing no unverified reliability claim was introduced.

## 20. References and implementation notes

The following authoritative references inform, but do not replace, the design choices in the TDD:

- PostgreSQL `SELECT` locking behavior and `SKIP LOCKED` suitability for queue-like access: https://www.postgresql.org/docs/current/sql-select.html and https://www.postgresql.org/docs/current/explicit-locking.html
- PostgreSQL partial indexes, including unique indexes over constrained subsets: https://www.postgresql.org/docs/current/indexes-partial.html
- Go release support and current release history: https://go.dev/doc/devel/release
- Prometheus naming and cardinality guidance: https://prometheus.io/docs/practices/naming/
- OWASP API Security Top 10, including object-level authorization and resource consumption risks: https://api-security.owasp.org/editions/2023/en/0x11-t10/
- Amazon SQS visibility timeout and at-least-once delivery behavior as a useful comparison point: https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-visibility-timeout.html

These sources were consulted on 16 October 2026. Verify runtime and dependency versions again when creating the repository and before deploying.
