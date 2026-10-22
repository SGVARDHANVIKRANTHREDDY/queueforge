# Interview Prep Built Around This Project

Companion to `RESUME_AND_INTERVIEW.md`. Rule zero still applies: only say what you can reconstruct from scratch.

## 1. System-design drills that reuse your own system
For each: state requirements, give a back-of-envelope estimate, sketch the design, then name the trade-offs. Practise aloud, 35 minutes each.

| Prompt | What to reuse from QueueForge | Numbers to reason with |
|---|---|---|
| Design a **job scheduler / cron service** | `job_schedules`, unique `(schedule_id, scheduled_for)` for exactly-once occurrences, misfire policy (ADR-006) | 1M schedules, 10k firing per minute, tick interval, catch-up storms |
| Design a **notification / email service** | Queue with per-tenant rate limit and quota, idempotency keys, retries with full jitter, dead-letter queue, provider failover | 50M messages/day = ~600/s average, 10x peaks; provider limits |
| Design a **webhook delivery system** | At-least-once + idempotency headers, exponential backoff, per-endpoint concurrency limit (your tenant slots), signing, replay from DLQ | slow endpoints must not block others (head-of-line: your claim bug!) |
| Design a **rate limiter** | Token bucket in the database (your enqueue statement), hot-row contention (800/s per tenant), sharded buckets as the fix | per-tenant vs global, accuracy vs cost |
| Design a **distributed lock / leader election** | Leases with fencing tokens; why a lease alone is not enough (stale holder) | clock skew: the DB-clock rule |
| Design **Dropbox-style background processing / thumbnailing** | Priority queues, worker pools per task type (your foreign-backlog finding), autoscaling on *oldest runnable job age* | queue depth is a poor signal; age is better |

For every drill, finish with: *"How would I know it is broken at 3 AM?"* and answer with a metric and an alert.

## 2. Questions you will be asked (answer from the code and the measurements)
1. **Why `SKIP LOCKED`, and what is the re-check after the lock for?** Candidates are chosen before locking; another worker may claim one in between; the `status='queued'` re-check under the lock prevents a double lease. (There is a test that fails 2 of 3 runs without it.)
2. **Why is the claim one statement now?** Per-job claims cost ~1 ms each in probes; a batch pays once. Also fewer round trips and commits. +16-20% throughput.
3. **Why `statement_timestamp()` instead of `clock_timestamp()`?** Same database clock; `clock_timestamp()` is volatile so it cannot bound an index scan. This one change turned a 653 ms scan into a 1 ms probe.
4. **How do you prove tenant isolation?** Every query is scoped; a fuzzer throws 400 cross-tenant requests and checks nothing succeeds, no ids leak, and the victim's data hash is unchanged. I mutation-tested it by removing the tenant filter.
5. **What does the property test check, and how do you know it can fail?** Database versus a model after every step, plus invariants. I injected 6 bugs; it caught them, and the two it missed taught me to fix the generator.
6. **Why is River faster than you, and when would you use it instead?** It does less per job and batches completions. If you do not need tenant quotas/slots, idempotency semantics or an attempt audit trail, use River.
7. **What is at-least-once, concretely?** A handler can run twice (crash after the side effect, before the commit). The database records one success (fencing), but the side effect is the handler's problem: make it idempotent.
8. **What breaks first at 10x load?** Per-tenant hot rows (~800 enqueues/s per tenant), then the single primary. Mitigations: sharded buckets, batch completion, partitioning, read replicas for stats.
9. **How does retention work and what went wrong originally?** Oldest-first batches drained each tick via a partial index; originally 100 per hour, a full scan per pass, and swallowed errors.
10. **What would you do first with two weeks and real users?** Deploy, observe, then optimise what the metrics say (likely completion batching).

## 3. Claims you may make / must not make
| May say (with evidence in the repo) | Must not say |
|---|---|
| "Claim latency stays flat from 0 to 400k unrelated queued jobs (measured 653 ms to ~1.4 ms)" | "Handles N jobs/second in production" |
| "~830 jobs/s drain on one shared vCPU; tuned River does ~1,490 on the same box" | "Faster than River/Redis" |
| "Recovers a `kill -9`'d worker in 3.1 s with a 3 s lease; survives a 10 s Postgres outage with zero loss" | "Exactly-once" |
| "Found 24 defects in review, each with a failing-first test; mutation-tested the guarantees" | "Used by N users" / "99.9% uptime" (until true) |
| "Property test: 5,000 random operations verified against a model" | "100% coverage" (it is 66%) |

## 4. If you used AI assistance
Say so plainly, then demonstrate understanding: open `claim.go` and explain each CTE; break the lease check on purpose and show the property test fail; explain why the first probe design was rejected (5x slower in the common case). Interviewers reward honesty plus depth and penalise recited stories.

## 5. Weekly routine (14 weeks, adjust to your date)
| Share | Activity |
|---|---|
| ~55% | DSA, daily; review mistakes weekly |
| ~20% | This project: deploy, users, one optimisation, notes in `OPERATIONS_LOG.md` |
| ~10% | System design drills (section 1) |
| ~10% | Mock interviews; record yourself giving the 2/5/15-minute pitches |
| ~5% | Applications and referrals (specific questions to engineers, not "please refer me") |
