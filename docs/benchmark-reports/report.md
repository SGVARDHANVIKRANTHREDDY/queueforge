# QueueForge: Benchmark, Scale & Reliability Report

Every number below is produced by a test or script in this repository; commands are at the bottom. Nothing is estimated. Where a number is partial, noisy, or unflattering, it says so.

## Environment (read before quoting anything)

| | |
|---|---|
| Machine | **1 vCPU, 4 GB RAM** Linux container. The load generator, the workers **and the database** all share that one core |
| PostgreSQL | 16, same machine, default config, `fsync` on (durable commits) |
| Go / Redis | Go 1.27.2 / Redis 7.0.15 (comparison only) |
| Noise | Single-core scheduling makes results noisy (+-10-20% between runs). Medians of 3 trials are used wherever it matters |

Absolute numbers are conservative and **cannot demonstrate multi-core or multi-node scaling**. Re-run on your own hardware and quote your own numbers.

## 1. Throughput and latency over time

Same benchmark code (`tests/load/throughput_test.go`), no-op handlers, exactly-once verified on every run.

| Stage | Conditions | Drain jobs/s | Idle enqueue-to-done p50 |
|---|---|---|---|
| Original code | 4 workers, 1,200 jobs | **16** | 222 ms |
| + drain loop, bounded concurrency, `LISTEN/NOTIFY` (ADR-011) | 4 workers, 1,200 jobs | 387 | 6.8 ms |
| Previous commit, per-job claim | 1 worker, 3,000 jobs, warm statistics | 480-698* | - |
| + single-statement completion, claim batching | 1 worker, 3,000 jobs | 698 | - |
| + single-statement **batch claim** (ADR-015) | 1 worker, 3,000 jobs | **811-833** | 9.3 ms (p95 13.6) |

\* the lower figure was measured before the completion statement was collapsed; rows are separate experiments, not one controlled series. The point is the direction and the cause, not the third digit.

- **Why 16 jobs/s originally:** one claim per 250 ms poll tick (4 jobs/s per worker). The old report blamed "handler work" although handlers were no-ops.
- **Enqueue:** ~1,000/s in-process (p99 35-45 ms). One tenant tops out near 800/s because every enqueue locks that tenant's rate-bucket and usage rows until commit; 16 tenants reach ~1,140/s, where the single core, not lock contention, is the limit.
- **A trap worth knowing:** on a freshly created database (no planner statistics) the same code ran at **269 jobs/s versus 682 after `ANALYZE`** (2.5x). Migration 00002 tunes autovacuum/analyze for the churn tables so production does not live in the cold-statistics regime.
- Honest regression: enqueue got ~10-20% slower when `pg_notify` was added (extra statement); the trade is a 33x idle-latency improvement.

## 2. Scale behaviour (`EXPLAIN (ANALYZE, BUFFERS)` on a 2,000,000-row `jobs` table, 746 MB)

Claim query latency, median of 7:

| Scenario | 10k | 100k | 400k | After fix (ADR-015) |
|---|---|---|---|---|
| **A.** completed-job history grows to 2M rows | - | **0.15-0.18 ms, flat** | - | 0.2 ms (unchanged) |
| **B.** queued jobs of a task type this worker doesn't handle, ahead in priority | 7.6 ms | 132 ms | **653 ms** | **1.1-1.4 ms flat** (371 buffers vs 403,000) |
| **C.** delayed high-priority jobs (run_at tomorrow) ahead of due jobs | 24 ms | 149 ms | **635 ms** | **1.2-1.7 ms flat** |

- Root causes: the filter columns weren't in the index key, and `run_at <= clock_timestamp()` is *volatile*, so PostgreSQL could never use it as an index bound. `statement_timestamp()` (same database clock, but stable) can.
- A first fix (probe per priority, per job) made the *normal* case 5x slower (0.22 to 1.1 ms). The final design pays the probe cost once per **batch** in one statement, and is also **16-20% faster end to end** than the per-job claim.
- **Retention at scale:** a steady-state pass (nothing to delete) scanned all 2,000,000 rows (48,812 buffers, ~400 MB of reads), **129x slower than on a small table**; now an index range scan, flat. Retention also deleted at most **2,400 jobs/day** (100 per hourly tick); it now drains any backlog each tick.
- Storage: ~373 bytes per terminal job including indexes (about 3.7 GB per 10 million rows), so retention is not optional.

## 3. Head-to-head (details and caveats in `bench/headtohead/README.md`)

3,000 no-op jobs, one worker instance (16 concurrent handlers), medians of 3:

| System | Enqueue /s | Drain jobs/s | Idle p50 | Durability |
|---|---|---|---|---|
| **QueueForge** | ~1,020 | **~833** | **9.3 ms** | Postgres fsync |
| River, defaults | ~2,400 | 159 | ~71 ms | Postgres fsync |
| River, `FetchCooldown=10ms` | ~2,200 | **~1,490** | ~2.5 ms | Postgres fsync |
| Asynq, defaults / `poll=50ms` | ~24,000 / ~27,000 | ~11,500 / ~8,300 | ~930 ms / ~28 ms | Redis memory |
| Asynq `poll=50ms` + AOF `fsync always` | ~13,000 | ~2,500 | ~26 ms | Redis fsync |

**QueueForge loses to tuned River (same database, same durability) by ~1.8x on drain and ~2x on enqueue**, because it does more per job (attempt row, tenant slot reserve/release, usage counter, token bucket) in exchange for per-tenant concurrency limits, quotas, rate limits and an attempt audit trail. It has the best **out-of-the-box** idle latency of the three. Redis is 3x (fsync) to ~14x (no fsync) faster, as expected from a single-purpose in-memory structure.

## 4. Correctness verification

- **Model-based property test** (`tests/property`): 25 seeds x 200 random operations (enqueue, claim batch, complete, fail, cancel, renew, lease expiry + recovery, stale completions). After **every** step the database is compared with an independent in-memory model and these invariants are checked: job status and attempt counts match; `outstanding_jobs` equals queued+running jobs per tenant; busy execution slots equal running jobs and never exceed the limit; every busy slot points at a running job holding that lease; unfinished attempts equal running jobs. A failing seed prints a replay command.
- **Coverage:** 66.4% of statements across `internal/` and the SDK (short mode). The lowest files (retention/recovery/scheduler loop wrappers, worker runtime) are exercised by the real-process chaos tests, which `go test -cover` cannot attribute; the claim, recovery, completion, auth, audit and retention logic they call is covered directly.
- **Mutation checking**: each guarantee below was deliberately broken and the suite had to fail. This found two blind spots in my own tests (a stale-lease generator that rarely hit the dangerous case; a double-claim race a sequential test cannot reach), both fixed.

| Injected bug | Caught by |
|---|---|
| success path forgets to decrement the usage counter | property test (7-21 steps) |
| failure path never releases the execution slot | property test |
| success/failure accepted with a stale lease token | property test (after strengthening the generator) |
| two jobs of a tenant share one execution slot | property test |
| no "still queued?" re-check after the row lock | concurrent double-claim test (6-10 duplicate leases; 2 of 3 runs) |
| tenant isolation hole in `GetJob` | cross-tenant fuzzer |
| `/v1/workers` leaks hostnames/task types | security test |
| `last_used_at` rewritten on every request | write-amplification test |
| auth-failure limiter disabled | brute-force test |
| audit middleware removed | audit test |
| naive SDK retry (new idempotency key per attempt) | SDK test: **3 duplicate jobs** vs 1 |
| declared-but-never-updated metric; dead-letter/retry counters mislabelled | metrics tests |

## 5. Fault tolerance

| Scenario | Test | Result |
|---|---|---|
| Worker `kill -9` mid-job | `TestChaos_RealKill9_WorkerProcessRecovery` (real binary, separate process) | recovered **3.1 s** after the kill (lease 3 s); exactly 2 attempts on different workers; 0 leaks |
| Postgres stopped ~10 s mid-workload | `TestChaos_PostgresOutageMidWorkload` (opt-in) | workers survived; 80/400 done when the DB returned; 16 in-flight jobs retried; all 400 done 10 s later; 0 dead, 0 leaks |
| Network faults via fault-injecting proxy: +250 ms latency, 4 connection resets, 8 s black hole (longer than the lease) | `TestChaos_NetworkFaults...` | all 240 jobs succeed; 0 dead, 0 leaks. **Wasted executions: 16 before commit-retry; 3-7 after when run alone; 14 once when four heavy test packages competed for the single vCPU** (so the strict <=10 guard is enforced only for isolated runs, `QF_CHAOS_STRICT=1`, as CI does) |
| Poison row in lease recovery / retention | regression tests | healthy rows still processed; failure reported |
| Stale worker completes after losing its lease | property + chaos | rejected (`ErrLeaseLost`) |

## 6. Soak (partial, honest)

`TestSoak`, 150 jobs/s sustained, 2% transient and 0.2% permanent failures injected. The sandbox restarted at **432 s, 64,831 jobs**, so this is a partial run (the nightly CI job runs 15 minutes):

| | first third | last third |
|---|---|---|
| enqueue p99 | 16.1 ms | 19.5 ms |
| queue wait p95 | 10.8 ms | 13.6 ms |
| goroutines | 24.8 | 23.9 |
| heap | 5.0 MB | 6.1 MB |

Zero errors logged; queue depth never above 3; dead tuples followed a healthy autovacuum sawtooth (600 to 17,700). **Latency drifted up ~25% over 7 minutes**; that is small and inconclusive but unexplained, which is exactly what the nightly run exists to watch.

## 7. Known limits and what is not measured
- At-least-once delivery. Handlers must be idempotent; idempotency keys protect submission only.
- A handler that ignores cancellation keeps running after its lease is lost; its result is fenced off but its side effects are not undone.
- Claim work per call is bounded by the candidate pool plus saturated tenants' pending quotas (ADR-015).
- Single PostgreSQL primary: no failover tested (recovery = restore from backup).
- **Not measured:** multi-core scaling, separate database host, network latency between nodes, runs longer than 7 minutes, load above ~1,000 jobs/s, multi-node workers.

## Batch completion (written, not yet measured)
`QF_BATCH_COMPLETION=true` groups successful completions into one statement. To measure it, run the
drain benchmark twice on the same machine, 3 trials each, and report medians:
```bash
QF_BATCH_COMPLETION=false QF_BENCH_JOBS=3000 QF_BENCH_WORKERS=1 go test -v -run TestBenchmark_Drain ./tests/load/
QF_BATCH_COMPLETION=true  QF_BENCH_JOBS=3000 QF_BENCH_WORKERS=1 go test -v -run TestBenchmark_Drain ./tests/load/
```
Do not quote an improvement until you have these numbers. Design proposals for higher scale are in
`docs/proposals/HIGH_SCALE.md`.

## Reproduce
```bash
export QF_DATABASE_URL='postgres://postgres:postgres@localhost:5432/queueforge?sslmode=disable'
QF_BENCH_JOBS=3000 QF_BENCH_WORKERS=1 go test -v -run 'TestBenchmark_Drain|TestBenchmark_Idle' ./tests/load/   # BENCH_RESULT lines
go test -race ./tests/property/ ./tests/regression/ ./tests/security/ ./sdk/...
go test -v -run RealKill9 ./tests/chaos/
QF_CHAOS_PG_STOP='service postgresql stop' QF_CHAOS_PG_START='service postgresql start' \
  go test -v -p 1 -run PostgresOutage ./tests/chaos/        # stops YOUR local Postgres: run alone
QF_SOAK_SECONDS=900 QF_SOAK_RATE=150 go test -v -timeout 40m -run TestSoak ./tests/load/
cd bench/headtohead && go mod tidy && go build -o h2h .       # River / Asynq comparison
```

## Reproduced on 2026-10-22

Environment: Windows 11 host, Intel Core i7-13650HX, 14 physical cores, 20 logical
processors, 7.6 GiB Docker Desktop memory, Go 1.27.0, PostgreSQL 16 in Docker.
The load tests used the disposable `qf-pg` container.

Three-run drain medians with 3,000 jobs and one worker:

| Mode | Drain runs (seconds) | Median drain jobs/s |
|---|---:|---:|
| Per-job completion | 3.05, 2.45, 2.45 | 1,224 |
| Batch completion | 3.15, 2.70, 2.55 | 1,111 |

Batch completion was not faster in this reproduction, so it should remain disabled
by default for this environment.

Idle latency: 150 samples, p50 14.9 ms, p95 21.9 ms, p99 24.2 ms.

The raw outputs are in `evidence/rerun-20261022-1409/bench-baseline.txt`,
`bench-batch.txt`, and `bench-idle.txt`.

The same soak run completed with `enqueued=271084`, `enqueue_errors=0`,
`succeeded=270541`, `dead=543`, `open=0`, `leaked_slots=0`, and
`outstanding=0`. There were zero successful jobs without exactly one successful
attempt. Heap drift was 7.4 MB to 8.6 MB, goroutines were 24 to 24, and
queue-wait p95 was 10 ms to 6 ms. The test passed.
