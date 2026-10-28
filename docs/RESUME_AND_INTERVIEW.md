# Resume & Interview Kit

## Rule zero: numbers must come from YOUR run
The figures below are measured results from the benchmark report. They were collected on a
1-vCPU Linux container with PostgreSQL co-located; quote the environment and limits with the
numbers. Re-run them on your own machine before claiming a different result.

```bash
QF_BENCH_JOBS=1200 QF_BENCH_WORKERS=4 go test -v -run TestBenchmark_DrainThroughput ./tests/load/
go test -v -run TestBenchmark_IdleLatency ./tests/load/
go test -v -run RealKill9 ./tests/chaos/
```
To get an honest "before" number, check out the first commit, copy `tests/load/throughput_test.go` onto it, and run the same command.

## Resume bullets (XYZ), use only numbers you can explain
**QueueForge: multi-tenant job queue on PostgreSQL** (Go, PostgreSQL, Docker, Prometheus, GitHub Actions)
- Cut claim latency from **653 ms to 1.1–1.7 ms** under a 400k-job unrelated or delayed backlog by replacing the volatile time predicate with a stable database-clock predicate and batching candidate probes; confirmed with `EXPLAIN (ANALYZE, BUFFERS)`. Also raised drain throughput from **16 to 811–833 jobs/s** and reduced idle pickup latency from **222 ms to 9.3 ms** with `LISTEN/NOTIFY`.
- Verified correctness with a model-based property test covering **25 seeds × 200 operations**, real worker `kill -9`, PostgreSQL-outage, network-fault, and Kubernetes container-kill drills; recovery completed in **3.1 s** for the local 3-second lease, with zero leaked slots or duplicate database successes in the recorded runs.
- Reproduced **9 of 12 original mutation-table rows plus 3 additional mutations**, including stale-lease fencing, slot/counter accounting, tenant isolation, auth throttling, audit middleware, and metrics checks; benchmarked against River and Asynq and documented that tuned River is ~1.8x faster on drain throughput.

Do **not** claim "used in production" or any user count until it is true. Replace every number with one you measured on your own machine.

## Pitches
**30 seconds:** "It's a multi-tenant job queue built on PostgreSQL. Workers claim jobs with `SKIP LOCKED` and hold fenced leases, so if a worker dies the job is recovered and a late stale worker cannot overwrite the result. I profiled the claim path, fixed scaling defects, and measured 811 to 833 jobs per second on a one-vCPU container. I verify crash recovery by actually killing a worker process and checking leases, slots, attempts, and final outcomes."

**5 minutes:** problem (reliable background work without Redis), data model (state machine enforced by CHECK constraints), claim path (priority, per-tenant slots, `SKIP LOCKED`), failure path (lease expiry, `settleFailedAttempt`, backoff with full jitter, DLQ), then two bugs you found and how a failing test proved each, then what you measured and its limits.

**15 minutes:** add idempotency (request hash, 409 on mismatch), cron occurrence uniqueness, fairness trade-offs (strict priority can starve low priority: ADR-005), NOTIFY trade-offs (ADR-011), what you would do next (batch claims, partitioning `jobs`, outbox).

## Questions you WILL be asked (answer from the code)
1. **Why `SKIP LOCKED`?** Workers must not block each other on the same row; a locked row is simply someone else's work. Lock held only for the short claim tx; handlers run outside any transaction.
2. **Walk through `ClaimJob`.** Candidate query (priority, run_at, created_at order; `EXISTS` free slot; excluded tenants) -> lock slot `SKIP LOCKED` (if contended, exclude tenant and retry, ADR-013) -> UPDATE job to running with lease -> reserve slot -> insert attempt -> commit.
3. **Why `clock_timestamp()` not `now()`?** `now()` is the transaction start time and is frozen inside a tx; `clock_timestamp()` is wall time at call. Using the DB clock everywhere also avoids worker clock skew.
4. **Exactly-once?** No. At-least-once. A crash after the side effect but before commit means a retry; handlers must be idempotent. The idempotency key protects submission, not execution.
5. **What if the worker is alive but slow and its lease expires?** Another worker may start the job; the first worker's completion is rejected because `lease_token` no longer matches (fencing). Its side effects are not undone.
6. **What happens if Postgres restarts?** Workers' claims/completions fail and retry on the next poll; nothing is lost because all state is durable; leases simply expire and recover.
7. **How does NOTIFY fail?** Best-effort: missed if the listener is disconnected. That is why polling remains. Correctness never depends on it.
8. **Why did the old benchmark say 16 jobs/s?** One claim per 250 ms tick per worker. (Tell the story of questioning the "bounded by handler work" claim.)
9. **What does not scale?** Single Postgres primary; per-tenant rate bucket row lock serialises one tenant's enqueues; global `NOTIFY` lock at very high enqueue rates; `jobs` table growth (retention job + partitioning plan).
10. **Why not Redis/Kafka/SQS?** ADR-001. Transactional enqueue + quota + idempotency in one commit beats dual writes at this scale; re-evaluate with measurements.

## Amazon Leadership Principles mapping
| LP | Story |
|---|---|
| Dive Deep | F-1 poison-pill transaction abort |
| Invent & Simplify | F-3 drain loop + NOTIFY with polling fallback |
| Are Right, A Lot | Questioned "bounded by handler work" with data |
| Earn Trust | Rewrote the report with limits and the 20% enqueue regression |
| Ownership | Real `kill -9` chaos test; runbooks |
| Deliver Results | 16 -> 811–833 jobs/s, 222 ms -> 9.3 ms |
| Insist on the Highest Standards | Regression test per bug, shown failing first |

## Be honest about how you built it
Interviewers care whether you *understand* the system, not who typed each line. If you used AI assistance for part of this, say so plainly and then show you understand it: open `ClaimJob` and `settleFailedAttempt` and explain them, break something on purpose and fix it live. Do not tell a story you could not retell from scratch. The best preparation is to re-implement one fix yourself (for example F-2) from the failing test, without looking at the solution.
