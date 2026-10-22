# Engineering Log: Bugs Found in Review

Every entry has a regression test that **failed on the original code and passes now**. Run them all with `go test -race ./tests/regression/... ./internal/config/...`.

| ID | Bug | Impact | Found by | Test | Fix |
|---|---|---|---|---|---|
| F-1 | `RecoverExpiredLeases` recovered a whole batch in **one** transaction and ignored statement errors (`_, _ = tx.Exec`) | One failing row aborts the Postgres transaction; `COMMIT` becomes a rollback; **every** recovery in the batch fails on **every** tick (poison pill that stalls crash recovery) | Code reading | `TestRecovery_PoisonJobDoesNotBlockHealthyJobs` (a trigger makes one row's update fail) | One transaction per job, re-check expiry under row lock, errors joined and returned ([ADR-012](ADRs/ADR-012.md)) |
| F-2 | `ClaimJob` chose a candidate, then `SKIP LOCKED`-locked its tenant's slot; if the slot was contended it returned "no work" | Worker idles while other tenants have runnable jobs (head-of-line blocking) | Code reading | `TestClaim_DoesNotIdleWhenTopTenantSlotIsContended` | Skip the contended tenant and pick the next-best candidate in the same tx ([ADR-013](ADRs/ADR-013.md)) |
| F-3 | `claimLoop` claimed **one job per 250 ms tick** | Hard ceiling of 4 jobs/s per worker; the old report blamed "handler work" although handlers were no-ops | Writing an honest benchmark (4 workers measured exactly 16 jobs/s) | `TestBenchmark_DrainThroughput` (16 -> 387 jobs/s) | Drain loop, bounded concurrency, `LISTEN/NOTIFY` wake-ups, polling kept as fallback ([ADR-011](ADRs/ADR-011.md)) |
| F-4 | `ClaimJob` never selected `run_at`, so `Job.RunAt` was the zero time | `job_queue_wait_seconds` observed ~2000 years for every job: the metric used by Runbook A was meaningless | Reading how the metric was computed | `TestClaim_ReturnsRealRunAt` | Select `run_at`; skip negative waits |
| F-5 | Env var read as `QF_DATABASE_MAX_CONns` (typo) | Documented `QF_DATABASE_MAX_CONNS` silently ignored; pool size not tunable | Reading config | `TestLoad_DatabaseMaxConnsEnvVarIsHonoured` | Fix the name |
| F-6 | "SIGKILL" chaos test backdated a lease in SQL instead of killing anything | Report overclaimed what was tested | Reading the test | `TestChaos_RealKill9_WorkerProcessRecovery` | Builds the worker binary, SIGKILLs a real process mid-job, second process recovers; measured 3.1 s |
| F-7 | Retry-vs-dead logic duplicated in `CompleteJobFailure` and recovery | Two copies that could drift (they already differed in subtle ways) | Code reading | covered by existing + new tests | Single `settleFailedAttempt` used by both paths |
| F-8 | Repo hygiene: root `Dockerfile` pinned Go 1.24 while `go.mod` needs 1.27; CI hard-coded Go 1.24; `gofmt` failed on 2 files; duplicate `benchmark_reports/`, `placement_alert_bot/` | Builds could fail; CI check would fail | `go build`, `gofmt -l`, `diff` | CI | CI uses `go-version-file: go.mod`; stray files removed |
| F-9 | `QF_METRICS_ADDR` was defined but never used: the **worker process exposed no metrics**, and Prometheus only scraped the API | Claims, queue wait, lease recoveries and job durations (everything the runbooks rely on) were invisible | Tracing where metrics are registered vs where they are served | smoke test `worker exposes queueforge_jobs_claimed_total`, `internal/observability` ops test | Shared private ops server (`/metrics`, `/healthz`, `/readyz`) in API and worker |
| F-10 | `/metrics` was served unauthenticated on the public API port | Operational detail leaked to the internet | `deploy/smoke.sh` flagged it on first run | smoke test `/metrics not public` | Metrics moved to a separate listener bound to loopback by default |
| F-11 | Deployment files: DB port published on all interfaces, `container_name` + `replicas` (invalid), `prometheus:latest`, containers ran as root, no healthchecks, no restart policy | Insecure and non-reproducible deploys | Review | compose YAML validated; prod stack added | `docker-compose.prod.yml`, Caddy TLS, non-root image, pinned images |
| F-12 | Claim cost was O(unrelated backlog): 7.6 / 132 / **653 ms** at 10k / 100k / 400k queued jobs of other task types (or delayed jobs) ahead in priority order | One worker pool's backlog slowed every other pool | `EXPLAIN (ANALYZE, BUFFERS)` on a 2M-row table (`Rows Removed by Filter: 400000`) | `TestClaim_CostIndependentOf*` (ratio-based: fails 18-21x on old code) | Single-statement batch claim with per-(queue,type,priority) index probes ([ADR-015](ADRs/ADR-015.md)): flat 1-2.6 ms, and 16-20% faster end to end |
| F-13 | `run_at <= clock_timestamp()`: a volatile function can never be an index bound | Every claim read every delayed row | Plan showed a `Filter:` where an `Index Cond:` was expected | same tests | `statement_timestamp()`: same DB clock, stable ([ADR-014](ADRs/ADR-014.md)) |
| F-14 | Enqueue stamped `run_at`, token refill and cron "now" with the **API server's clock** (7 sites) while claims used the DB clock | Clock drift hides jobs, runs delayed jobs early, breaks rate limits | Reading the code against the DB-clock design | `TestNoApplicationClockInPostgresLayer` (fails on 7 sites in the original) | DB is the only clock |
| F-15 | Metrics `jobs_queued`, `jobs_running`, heartbeat age declared but **never updated**; `JobRetriesTotal` never incremented; dead-lettered jobs never counted; Runbook A cited `queueforge_db_pool_connections`, which did not exist | The headline backlog alert could never fire | A test that every declared metric is updated and every metric named in a runbook/alert exists | `TestEveryDeclaredMetricIsUpdatedSomewhere`, `TestAlertsAndRunbooksOnlyReferenceMetricsThatExist`, `TestWorkerMetricsCountRetriesAndDeadLetters` | DB-backed `QueueStatsCollector` (incl. **oldest runnable job age**) + pool collector; worker counts outcomes; 10 alert rules |
| F-16 | `/v1/workers` returned every worker (hostnames, versions, handled task types) to any tenant | Infrastructure and other tenants' task names disclosed | Reviewing endpoints for tenant scoping | `TestWorkersEndpointDoesNotLeakInfrastructure` | Liveness only unless `workers:read` ([ADR-016](ADRs/ADR-016.md)) |
| F-17 | `last_used_at` rewritten in a new goroutine on **every** authenticated request | Write amplification on one hot row; DoS lever for any valid key | Reading the auth path | `TestLastUsedAtIsNotRewrittenOnEveryRequest` (checks the row's `xmin` does not change) | At most once a minute per key, one update in flight |
| F-18 | Unlimited wrong-key guesses; key expiry judged by the API server's clock; admin CLI could not create expiring keys (rotation impossible) | Brute-force cost, skew, no rotation | Review | `TestBruteForce...`, `TestExpiredAndRevokedKeysAreRejected`, `TestKeyRotationWithOverlap` | Failure limiter (429 before any DB work), DB-clock expiry, `create-api-key -expires-in` |
| F-19 | If recording a finished job's result failed transiently, the worker only logged it; the lease expired and the whole job re-ran | Long jobs re-executed after a one-second blip | Network-fault chaos test measured it: **16 wasted executions of 256** | `TestChaos_NetworkFaults...` (guard: <=10) | Commit retry with backoff while the lease renewer keeps running: 4-7 wasted |
| F-20 | Tests shared one database; the scheduler/recovery/workers are global by design | Leftover rows from a failed run broke later runs ("expected 0, got 1") | A cascade of confusing failures | every suite now uses `testutil.IsolatedDatabase` | Private database per test; integration suite 2x faster |
| F-21 | Cold planner statistics on a fresh database: 269 vs 682 jobs/s | An apparent 45% "regression" that was an artifact; also a real deployment risk | Same code, three conditions | benchmark runs `ANALYZE` (opt out with `QF_BENCH_ANALYZE=0`) | Migration 00002: autovacuum/analyze tuned for churn tables |
| F-22 | **Retention**: one batch of 100 per hourly tick (max 2,400 deletions/day); no index on `completed_at` (every pass scanned all 2M rows: 48,812 buffers, 129x slower); errors swallowed inside a transaction | The table and disk grow without bound; periodic I/O spikes; failures invisible | Coverage report showed 0% for retention | `TestRetention_*` (4 tests; 3 fail on old code) | Drain loop, partial index (migration 00006), poison isolation, errors returned, `retention_backlog_jobs` gauge + alert |
| F-23 | `internal/service`: 193 lines of dead code | Misleading coverage, maintenance burden | Coverage report (0%) + no importers | - | Deleted |
| F-24 | My own tests had blind spots: stale-lease generator rarely hit "re-claimed under a new lease"; double-claim race unreachable sequentially | Mutations survived | **Mutation testing** of every guarantee | strengthened generator; `TestClaim_ConcurrentWorkersNeverDoubleClaim` | See report section 4 |


| F-25 | **No panic recovery around task handlers.** A panic in any handler terminates the whole worker process (Go does not contain goroutine panics), killing every in-flight job on it; the job is then retried elsewhere and can crash that worker too | Poison-payload crash loop across the fleet until `max_attempts` | Code review (grep for `recover()` found none outside the HTTP router) | `TestSafeCall_PanicBecomesError` | `safeCall` converts a panic into a retryable error and logs the stack |
| F-26 | `middleware.RealIP` installed unconditionally: client address came from spoofable headers even with `QF_TRUST_FORWARDED_FOR=false`; the brute-force limiter and the audit log's remote address were forgeable. The existing test called `ClientKey` in isolation, never through the router | Documented control (THREAT_MODEL T2/T6) was not actually enforced | Code review | `TestBruteForceCannotBeBypassedWithSpoofedForwardingHeaders` | `RealIP` only when `TrustForwardedFor` is set |
| F-27 | "Transactional enqueue" was claimed in DESIGN.md but no API existed to enqueue inside a caller's transaction; the worker runtime lived under `internal/`, so no external module could register handlers without forking | A headline differentiator was unavailable; library unusable as a library | Code review | `TestEnqueueJobTx_*` | `EnqueueJobTx` (savepoint-based); public `workerkit` package; `QF_MIGRATE_DATABASE_URL` + `deploy/sql/least_privilege.sql` for T14 |

| F-28 | Handler errors (which can echo connection strings or tokens) were stored verbatim and returned to tenants; 500 responses included raw internal error text | Credential leakage through the job error field | Code review (THREAT_MODEL T7 gap) | `TestRedactSecrets*` | `domain.RedactSecrets` applied before persisting; generic 500 message plus server-side log |
| F-29 | No per-client request limit on authenticated traffic; enqueue only one job per request | Flood of cheap requests reached the DB; high-volume producers paid one commit per job | Code review (T9 gap, head-to-head gap) | `clientlimit_test.go`, `batch_test.go` | `ClientRateLimiter`; `POST /v1/jobs/batch` (atomic, up to 100, batch-level idempotency); Go SDK `EnqueueBatch` |

| F-30 | Every successful job cost its own statement and commit; per-job writes were the main throughput gap versus River (docs/HEAD_TO_HEAD) | ~1.8x slower drain than tuned River | Head-to-head benchmark | `TestCompleteJobsSuccessBatch_MatchesSingleCompletionSemantics` | Group-commit completer: `CompleteJobsSuccessBatch` (one statement for N jobs, per-job lease fencing, per-job stale detection); falls back to the single-job statement on any error. Opt-in (`QF_BATCH_COMPLETION=true`). **Gain not yet measured.** |

> F-25 to F-30 were found and fixed in an external code review **without a Go toolchain or database**, so none of those changes or tests has been compiled or run. Run `gofmt -l .`, `go vet ./...` and the full test suite before relying on them.

## More STAR stories (this round)

**Dive Deep / Invent and Simplify: the claim query (F-12, F-13)**
- *Situation:* I profiled a 2M-row table and found claim latency grew linearly with unrelated queued jobs (653 ms at 400k).
- *Action:* `EXPLAIN` showed a `Filter` where I expected an `Index Cond`: the time comparison used a *volatile* function, which PostgreSQL cannot use as an index bound. My first fix made the normal case 5x slower, so I measured it, rejected it, and redesigned the query to pay the probe cost once per **batch** in a single statement.
- *Result:* flat 1-2.6 ms at any backlog and 16-20% higher end-to-end throughput. I wrote ratio-based tests that fail 18-21x on the old code.

**Are Right, A Lot / Earn Trust: testing my own tests (F-24)**
- I injected bugs into my own code (a stale worker overwriting a job, a double claim, missing slot release). Two of them survived my test suite, which exposed blind spots in my generator and the absence of a concurrency test. I fixed both and recorded the full mutation table.

**Customer Obsession / Ownership: the alert that could never fire (F-15)**
- Writing alert rules, I checked that each metric existed and found three gauges that were declared but never updated, a runbook citing a nonexistent metric, and dead-letter counts never incremented. I added tests so that class of bug cannot recur: every metric named in an alert or runbook must exist, and every declared metric must be updated somewhere.

**Bias for Action with data: retention (F-22)**
- Coverage showed 0% on retention. Reading it: 2,400 deletions/day maximum, a full table scan per pass, swallowed errors. I measured the table scan at 2M rows, wrote failing tests first, and fixed all of it.

## STAR stories (adapt to your own words, and only tell what you can defend)

**Dive Deep: poison pill in crash recovery (F-1)**
- *Situation:* My job queue recovers jobs whose workers crashed by scanning for expired leases.
- *Task:* Make sure one bad row can never block recovery of the others.
- *Action:* I reproduced it with a Postgres trigger that makes one row's UPDATE fail. I learned that in Postgres a failed statement aborts the entire transaction, so my "continue on error" loop was silently doing nothing and the final COMMIT rolled back every recovery. I moved to one short transaction per job with a re-check under `FOR UPDATE SKIP LOCKED`, and returned the joined errors so the failure is visible.
- *Result:* Test went from 0 recovered to healthy job recovered; poison row is reported instead of hidden.

**Invent & Simplify / Deliver Results: 24x throughput (F-3)**
- *Situation:* My benchmark said 16 jobs/s and my report said "bounded by handler work".
- *Action:* I questioned that because my handlers were no-ops. 4 workers x 4/s matched one claim per 250 ms tick exactly. I changed the loop to drain while work exists, bounded concurrency with a semaphore, and added `LISTEN/NOTIFY` as a wake-up hint while keeping polling as the source of truth.
- *Result:* 16 -> 387 jobs/s; idle p50 222 ms -> 6.8 ms; duplicates/missing/leaked slots stayed 0 in every run.

**Are Right, A Lot / Earn Trust: correcting my own claim (F-3, F-6)**
- I replaced a misleading report: the 100-job benchmark and the "SIGKILL" test that did not kill anything. I rewrote the report with methodology, hardware, a measured enqueue **regression** (~20% slower from `pg_notify`), and a "known limits" section.

**Ownership: reliability you can demo (F-6)**
- Built a chaos test that builds the real worker binary, SIGKILLs it mid-job, and asserts exactly two attempts on two different workers, zero leaked slots, quota counter back to zero. Measured recovery 3.1 s on a 3 s lease.

**Ownership / Dive Deep: "my own smoke test caught my own leak" (F-9, F-10)**
- Wrote a post-deploy smoke script, ran it against a live local stack, and it immediately failed on `/metrics` being public. Fixing that exposed that worker metrics were never served at all. Result: private ops listener, 13/13 smoke checks.

## Not yet done (so nobody has to guess)
- Multi-core / separate-Postgres scaling is **unmeasured**; the 1-vCPU box flattens at 1-2 workers.
- No batch-claim optimisation yet (about 5 statements to claim and 5 to complete per job).
- Not deployed with real users yet.
