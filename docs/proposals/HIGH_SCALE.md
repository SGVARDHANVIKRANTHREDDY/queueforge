# High-scale proposals (NOT implemented, NOT wired in, NOT tested)

Batch completion IS implemented (`QF_BATCH_COMPLETION=true`, off by default; see ENGINEERING_LOG F-30).
The two items below change the schema or the quota semantics, so they were deliberately left as written
designs: a mistake in them corrupts data, and they cannot be checked without a database and the
property test. Do them one at a time, each behind a measurement.

## A. Partition `jobs` by completion time (makes retention a `DROP PARTITION`)

Why: at ~373 bytes per finished job, 10k jobs/s is hundreds of GB/day; `DELETE`-based retention then
competes with autovacuum for I/O.

What breaks and must be solved first:
1. A partitioned table's primary key and every unique index must include the partition key. `jobs.id`
   is referenced by foreign keys from `job_attempts`, `idempotency_keys` and `tenant_execution_slots`;
   those FKs would have to carry the partition key too or be dropped and replaced by application-level
   checks plus a verifying test.
2. Partition by `completed_at` is awkward because live rows have `completed_at IS NULL`. A usual answer
   is a two-level scheme: keep live (queued/running) jobs in a small unpartitioned table and move
   terminal jobs to a partitioned `jobs_history` in the same transaction that completes them.
3. Partition creation and dropping need a maintenance job (daily create next N partitions, drop expired
   ones) plus an alert when the newest partition is less than 2 days ahead.

Measure first: run the 2M-row retention scenario from BENCHMARK_REPORT section 2 and confirm retention
I/O is actually the bottleneck at your target rate before taking on this complexity.

## B. Remove the hot `tenant_usage.outstanding_jobs` row

Why: every enqueue and every completion updates one row per tenant, which serialises a tenant at
~800 enqueues/s.

Options, in order of risk:
1. **Sharded counter**: N rows per tenant (e.g. 16); enqueue/complete picks `hash(job_id) % N`; quota
   check sums the N rows. Hot-row contention drops about N times. The quota becomes approximate by at
   most the in-flight transactions, which must be documented.
2. **Derive lazily**: count queued+running jobs on demand with a partial index; cheap only while the
   per-tenant backlog is small.
Either option changes what the property test's `outstanding_jobs == queued+running` invariant means;
update the model first, and keep the old invariant as "sum of shards".

## C. Tenant sharding across several PostgreSQL databases
Route by `hash(tenant_id)`. Needs a tenant-to-shard directory, a worker per shard (or a multi-shard
claim loop) and cross-shard admin tooling. Only worth building after A and B are measured.
