# Head-to-head: QueueForge vs River vs Asynq

A separate Go module (so the comparison libraries never touch the main module's dependencies) that
drives each system through its **native client** with the same workload shape as
`tests/load/throughput_test.go`:

1. **drain**: 16 concurrent inserters enqueue N no-op jobs, *then* one worker instance (16 concurrent
   handlers) starts; time to run them all, with an exactly-once check on every job
2. **idle latency**: queue empty, workers running, enqueue one job at a time, time enqueue -> handler ran

```bash
cd bench/headtohead && go mod tidy && go build -o h2h .
./h2h -system asynq -redis 127.0.0.1:6379 -jobs 3000 -workers 1 -idle 40 [-asynq-poll 50ms]
BENCH_DATABASE_URL='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' \
  ./h2h -system river -jobs 3000 -workers 1 -idle 40 [-river-cooldown 10ms]
# QueueForge itself, same shape:  QF_BENCH_JOBS=3000 QF_BENCH_WORKERS=1 go test -v -run 'TestBenchmark_Drain|TestBenchmark_Idle' ./tests/load/
```

## Results (3 trials each, medians; 1 vCPU / 4 GB container shared by the load generator, workers AND the database or Redis)

| System | Enqueue /s | **Drain jobs/s** | Idle pickup p50 | Idle p95 | Durability of an accepted job |
|---|---|---|---|---|---|
| **QueueForge** | ~1,020 | **~833** | **9.3 ms** | 13.6 ms | PostgreSQL commit (fsync) |
| River, defaults | ~2,400 | 159 | ~71 ms | 88-590 ms | PostgreSQL commit (fsync) |
| River, `FetchCooldown=10ms` | ~2,200 | **~1,490** | **~2.5 ms** | ~3.8 ms | PostgreSQL commit (fsync) |
| Asynq, defaults | ~24,000 | ~11,500 | ~930 ms | ~1,400 ms | Redis memory (no fsync) |
| Asynq, `TaskCheckInterval=50ms` | ~27,000 | ~8,300 (7,900-14,900) | ~28 ms | ~54 ms | Redis memory (no fsync) |
| Asynq, 50 ms + Redis AOF `fsync always` | ~13,000 | ~2,500 | ~26 ms | ~53 ms | Redis fsync per write |

No duplicates, losses, or leaked slots in any run of any system.

## Honest reading

**Where QueueForge loses, and why**
- **vs tuned River (the fair comparison: same database, same durability): ~1.8x slower drain, ~2x slower enqueue, ~3.7x higher idle latency.** River does less per job. Per job QueueForge writes the job row, an *attempt* row, a tenant *execution slot* row (twice: reserve and release), and a tenant *usage counter* (twice: enqueue and complete), and on enqueue also locks and updates a token-bucket row. River transitions one row per state and batches completions. Those extra writes are the price of per-tenant concurrency limits, pending quotas, rate limits, and a per-attempt audit trail, which River does not have.
- **vs Redis (Asynq):** 3x slower even when Redis fsyncs every write, ~14x slower than Redis without fsync. A single-purpose in-memory structure beats a transactional relational design at raw throughput, as expected. What Redis does not give you: enqueue in the **same transaction** as your business write, SQL visibility into the queue, and durability by default.
- Asynq's default idle latency (~930 ms) is a *polling interval*, not a throughput limit; River's default drain (159/s) is its fetch cooldown. Both are config, and both are shown tuned. QueueForge's defaults need no tuning because of `LISTEN/NOTIFY` plus a drain loop.

**Where QueueForge is competitive**
- Out of the box (no tuning) it has the lowest idle latency of any *default* configuration here (9 ms vs 71 ms River, 930 ms Asynq) and 5x River's default drain throughput.
- It is the only one of the three with built-in multi-tenant quotas/fairness, request-hash idempotency keys, an append-only audit log, and fenced leases verified by a `kill -9` test.

**What this means for the project's story:** it is not the fastest queue and should not claim to be. The defensible claim is: *"a multi-tenant, transactional queue on PostgreSQL that is within ~2x of a mature Postgres queue on throughput while providing tenant isolation features that queue lacks, with every correctness property tested under crash, network and outage faults."*

**Known optimisations that would close part of the gap (not done):** batch the success-completion path (collect completions for ~5 ms and flush with one `unnest` statement, as River's completer does); a batch-enqueue API; fewer hot-row writes per job (for example derive `outstanding_jobs` lazily). Expect the first two to matter most; measure before believing.

## Limits of this comparison
- One machine, **one vCPU shared with the database/Redis**: absolute numbers are conservative and unrepresentative of production, and it cannot show multi-core or multi-node scaling for any system.
- Single worker instance, no-op handlers, small payloads. Real handlers shift the bottleneck.
- Defaults vs tuned reflect each library's documented knobs, not exhaustive tuning of any of them.
- Library versions: asynq v0.26.0, river v0.49.0, Redis 7.0.15, PostgreSQL 16.
