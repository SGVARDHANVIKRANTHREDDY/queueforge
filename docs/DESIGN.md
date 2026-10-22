# QueueForge: Design Document

*Format: a condensed Amazon-style narrative. Numbers come from `docs/benchmark-reports/report.md`.*

## 1. Problem
Applications need to run work in the background (send email, resize an image, call a slow API) **reliably**: the work must not be lost if a process crashes, must not run twice when a client retries, and one customer's flood must not starve everyone else. Teams usually reach for Redis or a cloud queue, then discover they need transactional enqueue (job and business row commit together), per-customer limits, and the ability to *query* the queue.

## 2. Tenets (in priority order, used to break ties)
1. **Never lose an accepted job.** Durability beats speed.
2. **Never let two workers own a job at once.** Correctness beats throughput.
3. **One customer cannot hurt another.** Fairness beats peak utilisation.
4. **Operable at 3 AM.** Every failure mode has a metric, an alert, and a runbook.
5. **Boring technology.** One PostgreSQL, no new infrastructure.

## 3. Non-goals
Exactly-once *execution* (impossible without cooperating handlers), sandboxing untrusted code, multi-region active-active, millions of jobs per second.

## 4. Design
```
 client --HTTP--> API --(one transaction)--> PostgreSQL <--claim/complete-- workers (N processes)
                     idempotency + rate limit + quota + job row + NOTIFY      lease + heartbeat + recovery
```
- **Job state machine**, enforced by `CHECK` constraints so illegal states cannot be stored: queued, running (requires worker, lease token, expiry), succeeded, dead, cancelled.
- **Claiming**: workers lease a *batch* of jobs in one statement (`FOR UPDATE SKIP LOCKED`), choosing by priority then FIFO across tenants, limited by each tenant's free execution slots (ADR-015).
- **Leases and fencing**: a lease has a random token; every completion is `WHERE lease_token = $1`. A worker that stalled and lost its lease can still finish its handler, but its result is rejected (ADR-004). A background loop requeues expired leases (each in its own transaction, ADR-012).
- **Idempotent submission**: a client key plus a hash of the request; same key + same body returns the original job, same key + different body is a 409.
- **Multi-tenancy**: per-tenant token bucket, pending-job quota, and concurrent-execution slots; every query is tenant-scoped; foreign ids are indistinguishable from missing ones.
- **Time**: the database is the only clock (ADR-014).
- **Wake-ups**: `LISTEN/NOTIFY` for latency, polling as the source of truth (ADR-011).
- **Observability**: DB-backed gauges (depth, *oldest runnable job age*, live workers), outcome counters, pool saturation, trace ids, an append-only audit log.

**Transactional enqueue, precisely.** Over HTTP an enqueue is its own transaction (job + idempotency key + rate bucket + quota + NOTIFY commit atomically), but it *cannot* share a transaction with your business data. Code that embeds QueueForge in the same PostgreSQL database can call `JobRepo.EnqueueJobTx(ctx, tx, params)`: it runs inside a savepoint on *your* transaction, so the job exists if and only if your transaction commits, and a quota or rate-limit rejection does not poison your transaction. This path is covered by `tests/regression/enqueue_tx_test.go`, which has not yet been run (no Go toolchain was available when it was written).

## 5. Alternatives considered
| Option | Why not (for this scope) |
|---|---|
| Redis (Asynq/Sidekiq style) | 3-14x faster in measurement, but enqueue cannot share a transaction with business data; durability needs AOF `fsync always` (then ~2,500 jobs/s, still fast); no SQL visibility |
| SQS / cloud queue | Excellent, but no per-tenant concurrency limits, no transactional enqueue, vendor lock-in; this project's goal was to build and understand the mechanism |
| Kafka | A log, not a work queue: no per-message acknowledgement/retry/delay semantics; large operational footprint |
| River (Postgres) | The closest alternative and **faster when tuned** (~1.8x drain). It lacks the tenant quotas/slots, idempotency-key semantics and audit trail built here. If you do not need those, use River |

## 6. Results
Drain 16 to ~830 jobs/s; idle pickup 222 to ~9 ms; claim latency flat from 0 to 400k unrelated queued jobs; `kill -9` recovery in 3.1 s; Postgres outage survived with zero loss; 5,000 randomized operations verified against a model; every guarantee mutation-tested. Full tables in the benchmark report.

## 7. Risks and mitigations
| Risk | Mitigation | Residual |
|---|---|---|
| Single PostgreSQL = single point of failure | Backups + restore drill documented | No automated failover (not tested) |
| Hot per-tenant rows serialise one tenant's enqueues (~800/s) | Many tenants scale; token bucket and usage in one statement | A single huge tenant is capped |
| Handlers running twice | Idempotency guidance, fenced results, attempt audit | Side effects are the handler's responsibility |
| Retention falling behind | Drain loop, partial index, backlog gauge + alert | Needs the alert to be watched |
| Throughput below Redis/River | Documented trade-off; batch completion identified as next optimisation | Not implemented |

## 8. Open questions
1. Is a **batch completion** path (one statement for many finished jobs, like River's completer) worth its added latency? Measure first.
2. Should `outstanding_jobs` be derived lazily to remove a hot row per job?
3. Partition `jobs` by completion time to make retention a `DROP PARTITION`? Needs measurement at >100M rows.
4. Opt-in **lite mode** that skips per-attempt rows for tenants who do not need an audit trail?

## 9. FAQ
**Why not exactly-once?** Because a handler's side effect and its commit cannot be made atomic. What *is* guaranteed: a job completes at most once in the database (fencing) and runs at least once.
**What happens if the database restarts mid-job?** Workers retry the commit with backoff while the lease renewer runs; if the outage outlasts the lease, the job is recovered and retried. Tested with a 10-second outage.
**What would you do with two more weeks?** Deploy it with real users first (nothing teaches more), then batch completion, then a separate-hardware scaling curve.
