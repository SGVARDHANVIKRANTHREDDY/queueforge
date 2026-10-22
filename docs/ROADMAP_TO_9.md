# Roadmap: From 7.5 to 9/10 for Amazon and Google

## STATUS (updated after the build-out): what is done, what is not, what only you can do

**Done and verified in the build environment** (each item has a test, a measurement, or a document in this repo):

| Phase | Item | Evidence |
|---|---|---|
| 3 Performance | Profiled with `pg_stat_statements` and `EXPLAIN (ANALYZE, BUFFERS)` | report sections 1-2, ENGINEERING_LOG F-12/13/21/22 |
| 3 | Batch claiming + single-statement completion | drain 16 to ~830 jobs/s; claim flat 1-2.6 ms under 400k backlog |
| 3 | 2M-row scale test, delayed/foreign backlog, retention at scale | report section 2 (found and fixed 3 O(n) defects) |
| 3 | Soak test | **partial**: 432 s / 64.8k jobs (sandbox restarted); nightly 15-minute CI job added |
| 4 Reliability | Model-based property test (5,000 random ops, invariants after every step) + mutation checks | `tests/property`, report section 4 |
| 4 | Network fault injection (own TCP proxy: latency, resets, black hole) | `tests/chaos/netfault_test.go`; wasted executions 16 to 4-7 |
| 4 | Real `kill -9`, Postgres outage, poison-row isolation | `tests/chaos`, `tests/regression` |
| 4 | Clock skew | enforced **structurally** (`TestNoApplicationClockInPostgresLayer`, 7 violations found); not behaviourally testable from Go |
| 4 | Alert rules as code + runbooks that only cite real metrics | `deploy/alerts.yml` (10 rules), metrics tests |
| 4 | Tracing | **partial**: W3C-compatible trace id stored, returned (`X-Trace-Id`), logged. No OpenTelemetry spans/exporter |
| 4 | Coverage measured | 66.4% (report section 4) |
| 5 Security | Threat model, key expiry/rotation, auth throttling, audit log, isolation fuzzer, worker-endpoint leak fixed | `docs/THREAT_MODEL.md`, `tests/security`, ADR-016 |
| 5 | CI security gates (govulncheck, staticcheck, gitleaks, trivy, promtool) | **configured, not executed here** (no access to vulnerability databases) |
| 6 Differentiators | Head-to-head vs River and Asynq | `bench/headtohead` (honest: tuned River is ~1.8x faster) |
| 6 | Go SDK with idempotency-safe retries | `sdk/go/queueforge` (test: lost response creates exactly 1 job) |
| 6 | Design write-up, demo script | `docs/DESIGN.md`, `docs/DEMO_SCRIPT.md` |
| 7 | System-design drills, story bank | `docs/INTERVIEW_PREP.md`, ENGINEERING_LOG STAR stories |

**Not done (could not be done in a sandbox, or would be dishonest to fake):**

| Item | Why | What you do |
|---|---|---|
| Phase 1: live deployment, 30 days uptime, uptime monitor, canary, a real incident + COE, SLO error-budget graph | No infrastructure, no calendar time | Follow `docs/DEPLOYMENT.md`; keep `docs/OPERATIONS_LOG.md` |
| Phase 2: real external users, usage chart, user quote | Requires actual people | Wire the placement bot to your live instance; onboard 3-5 tenants |
| Phase 3: separate-hardware scaling curve (app and database on different machines, 4+ vCPU) | One shared vCPU cannot show it | Rent two small VMs; rerun `tests/load` and `bench/headtohead` |
| Phase 3: 1-hour soak | Sandbox restarts every few minutes | Let the nightly CI job run, then read its logs |
| Phase 4: Postgres replica failover drill, disk-full drill, TLA+ model | Needs multi-node setup / is a stretch item | Optional; see below |
| Phase 4: OpenTelemetry spans | Dependency access | Adopt the stored trace id with the OTel SDK |
| Phase 5: executing govulncheck/trivy/gitleaks; least-privilege DB roles; DB TLS | Not runnable / not applicable here | Enable CI, read the first reports, fix findings |
| Phase 6: merged open-source PRs, blog post, demo *video* | Needs maintainers and your voice | Two small PRs to River/pgx/asynq docs or tests; record the demo from `docs/DEMO_SCRIPT.md` |
| Phase 7: mock interviews, applications, referrals, DSA | Entirely human | Follow phase 7; keep DSA at ~55% of prep time |
| Phase 0: understanding it all | Cannot be transferred | Re-implement a fix from its failing test, unaided (ENGINEERING_LOG lists 24) |

**Honest re-score** (the scorecard below shows the original targets):

| Dimension | Before | Now | To reach target |
|---|---|---|---|
| Correctness & data safety | 8 | **9** | - |
| Failure handling | 7 | **8.5** | failover drill |
| Testing | 7 | **9** | long soak results |
| Observability | 6 | **8** | live dashboards, OTel spans |
| Performance & scale | 4 | **6.5** | separate-hardware curve; batch completion |
| Security | 4 | **7** | DB roles/TLS, executed scans |
| Operations | 4 | **4.5** | **deploy it** (nothing about this changes until then) |
| Real-world validation | 2 | **2** | **users** |
| **Production readiness (overall)** | ~5.5 | **~6.5** | deployment + users are the whole gap |
| **Resume / interview value** | 7.5 | **8.5** | deployment, users, one merged PR |

The remaining distance to 9 is almost entirely things only time and other people can provide. More code will not close it.

---

**Premise.** A "9" is not more features. It is *verifiable evidence* that you built it, run it, broke it, and understand it. Every phase below ends in an **exit gate** that a stranger can verify in two minutes (a URL, a graph, a merged PR, a test name).

**Honest limits.**
- A project is roughly a quarter to a third of the outcome. **DSA decides the online assessment and the loops.** Budget your time: **DSA 55%, this project 25%, CS fundamentals/system design 10%, applications/networking 10%.** If the project starts eating DSA time, cut scope, not DSA.
- Nothing here guarantees an offer. Google new-grad hiring from a tier-3 college is genuinely hard and mostly runs through referrals and off-campus applications; Amazon is more reachable but still competitive. Check each company's careers page for current new-grad openings and timelines, and do not plan around a single company.
- Do not add Kubernetes, Kafka, microservices or a blockchain "for the resume". Depth in one system beats a buzzword list, and you must be able to defend every component.

## Scorecard

| Dimension | Now | Target | Phase |
|---|---|---|---|
| Correctness & data safety | 8 | 9 | 4 |
| Failure handling | 7 | 9 | 4 |
| Testing | 7 | 9 | 4 |
| Observability | 6 | 9 | 1, 4 |
| Performance & scale | 4 | 8 | 3 |
| Security | 4 | 8 | 5 |
| Operations | 4 | 9 | 1 |
| Real-world validation | 2 | 8 | 1, 2 |
| **Production readiness (overall)** | **~5.5** | **~8.5** | |
| **Resume/interview value** | **7.5** | **9** | all |

Timeline: ~14 weeks, phases overlap. Pace yourself to your actual graduation date; compress by dropping Phase 6 first, never Phases 1-2.

---

## Phase 0 (Week 1): Make it yours
You cannot defend what you did not write. Do this before anything else.
- [ ] Re-run all benchmarks and tests on **your** machine; replace every number in README/resume with your own.
- [ ] Without looking at the solution, re-implement fix F-2 (contended slot) from `TestClaim_DoesNotIdleWhenTopTenantSlotIsContended`. Then F-1.
- [ ] Draw the state machine (queued/running/succeeded/dead/cancelled) and the claim/complete/recover sequence **from memory** on paper. Photograph it; it goes in the README.
- [ ] Re-author the commits you are comfortable owning; keep honest history (see `RESUME_AND_INTERVIEW.md`).

**Exit gate:** you can explain `ClaimJob`, `settleFailedAttempt` and the lease-fencing argument for 10 minutes with the code closed.

## Phase 1 (Weeks 1-3): Deploy and operate it
Follow `docs/DEPLOYMENT.md`. Operations is where a student project becomes a service.
- [ ] Live HTTPS URL; smoke test green from outside.
- [ ] Backups running **and one restore drill passed** (`deploy/restore-drill.sh`).
- [ ] External uptime monitor + a canary job every 5 minutes that alerts to your phone.
- [ ] Grafana (or Prometheus) dashboard screenshots: throughput, queue depth, queue-wait p95, lease recoveries, dead letters.
- [ ] `docs/OPERATIONS_LOG.md` with every incident (you will have some: the first one is free material).
- [ ] Write **one Correction-of-Error (COE)** for a real incident: timeline, root cause, "5 whys", fixes, what prevents recurrence.
- [ ] Define two SLOs and track them, for example "99% of jobs start within 5 s of becoming runnable" and "99.5% of `/v1/jobs` requests succeed", with a 30-day error-budget graph.

**Exit gate:** 30 consecutive days up, one real incident documented, one restore verified.

## Phase 2 (Weeks 2-6, in parallel): Real users
This is the single biggest jump (Real-world validation 2 -> 8). Even three users count if they are real.
- [ ] Wire the placement-alert bot to the live instance for a real audience: your placement cell, a college club, a hostel group, a class WhatsApp/Telegram broadcast.
- [ ] Onboard 3-5 external users/projects (clubs, friends' side projects, a local small business) each with their **own tenant and key**.
- [ ] Ship the 2 features users actually ask for (e.g. a webhook callback on completion, a simple retry button in the dashboard). Record the request, the decision, the result.
- [ ] Track: jobs/day, distinct tenants, failure rate, median pickup latency. Put a real usage chart in the README.
- [ ] Get one short written quote from a user, with permission.

**Exit gate:** a graph of real jobs from users who are not you. Only then may the resume say "used by N". Never inflate this number: interviewers probe it, and Amazon's "Earn Trust" is not a slogan.

## Phase 3 (Weeks 4-8): Performance evidence
Currently the headline is ~390 jobs/s on one shared vCPU. Show you can find and remove bottlenecks.
- [ ] Rent two VMs (app and database separated, 4 vCPU on the DB) and re-run `TestBenchmark_DrainThroughput` for 1/2/4/8/16 workers. Publish the **scaling curve**, whatever it shows.
- [ ] Profile with `pprof` and `EXPLAIN (ANALYZE, BUFFERS)` on the claim and complete queries. Document the top three costs.
- [ ] **Batch claiming:** claim up to N jobs per transaction (one `UPDATE ... FROM (SELECT ... LIMIT N FOR UPDATE SKIP LOCKED)`), keeping fairness and slot rules correct. Re-measure. Hypothesis: round trips per job drop from ~10 toward ~3. Report the real result, even if smaller.
- [ ] Collapse the completion path (`job`, `attempt`, `slot`, `usage` updates) into fewer round trips (a single CTE or a stored function) and prove invariants still hold with the existing suites.
- [ ] 1-hour **soak test** at a steady rate: flat memory, flat latency, no slot leaks, no table bloat surprises (check autovacuum, dead tuples).
- [ ] **10 million row** test: enqueue 10M jobs, show the claim query still uses the right index, measure retention/cleanup, and decide whether partitioning is needed (ADR with data).
- [ ] Find the true limit: single-tenant enqueue (rate-bucket row lock), global NOTIFY lock, connection pool size. Write them up as measured limits.

**Exit gate:** one chart (workers vs jobs/s on separated hardware), one flame graph, and an ADR that says "bottleneck was X, fix was Y, gain was Z".

## Phase 4 (Weeks 6-10): Reliability depth (the Google-flavoured work)
Move from "I tested crashes" to "I can reason about correctness".
- [ ] **Model-based/property tests** of the job state machine (`pgregory.net/rapid` or Go fuzzing): random interleavings of enqueue/claim/complete/fail/recover/cancel must never violate invariants (running iff lease present; slots <= limit; outstanding counter == non-terminal jobs; no job completes twice).
- [ ] **Network fault injection** with Toxiproxy between workers and Postgres: latency, timeouts, connection resets, half-open connections. Add the scenarios to the chaos suite.
- [ ] **Clock skew** test (workers' clocks off by minutes) proving the DB-clock design holds.
- [ ] **Disk full / connection exhaustion** drills and the runbook entries they produce.
- [ ] **Failover drill:** run Postgres with a streaming replica (or a managed instance with failover), fail the primary, measure unavailability and confirm no lost *committed* job. Document replication-lag risk.
- [ ] **Stretch (high signal, optional):** a small **TLA+** (or PlusCal) spec of the lease protocol, model-checked for "no two workers can both successfully complete the same attempt". Explaining a model checker run to an interviewer is memorable.
- [ ] **Alert rules** as code (`deploy/alerts.yml`) for queue backlog, lease-recovery rate, dead-letter spike, worker heartbeat loss, each linking to its runbook.
- [ ] **Tracing:** propagate a trace id from the enqueue request through claim to completion (OpenTelemetry), so one job's life is a single trace.
- [ ] Measure and publish **test coverage** (`go test -cover`) and raise it deliberately on `job_repo.go` and `api.go`.

**Exit gate:** a "chaos matrix" table in the README (fault -> expected behaviour -> test name -> result) with every row green and at least one bug found and fixed *by this phase* (and its regression test).

## Phase 5 (Weeks 8-11): Security
- [ ] One-page **threat model** (STRIDE): assets, trust boundaries, top 8 threats, mitigations, residual risk.
- [ ] API key **expiry and rotation** (overlapping validity), plus per-key rate limits and **auth-failure throttling**.
- [ ] **Audit log** for tenant/key/replay/cancel actions (who, what, when, request id).
- [ ] Postgres over TLS (`verify-full`) if the DB leaves the private network; a least-privilege DB role (no superuser for the app; migrations role separate).
- [ ] CI gates: `govulncheck`, `trivy` image scan, `gosec`/`staticcheck`; fail the build on high findings.
- [ ] Verify tenant isolation with a **fuzz/property test**: random cross-tenant requests must never read or mutate another tenant's data.
- [ ] Secrets only via env/secret store; scan git history for leaks (`gitleaks`).

**Exit gate:** threat model + green security CI + an isolation fuzz test.

## Phase 6 (Weeks 10-13): Differentiators (pick two, not all)
1. **Head-to-head benchmark** against existing queues (for example River or Asynq, plus Celery/Sidekiq if you can): same hardware, same workload, published methodology and code. "I measured mine against the real ones, and here is where I lose and why" is a very strong interview story.
2. **Open-source contributions:** two merged PRs to projects you used (pgx, goose, river, asynq, Prometheus client, chi). Even docs/test fixes count; reviewers' comments are signal.
3. **Go client SDK** generated from `api/openapi.yaml`, with examples; plus the Python SDK published to PyPI (test index first).
4. **Design write-up:** an Amazon-style 6-pager (problem, tenets, options, decision, risks) and a 2-page "lessons learned". Publish on a blog or the repo.
5. **Demo video (3-5 min):** enqueue 10k jobs, `kill -9` a worker live, watch recovery on the dashboard, then show the Postgres outage test. Link it at the top of the README.

**Exit gate:** at least two of the above publicly visible.

## Phase 7 (Weeks 12-14): Convert it into offers
- [ ] **Story bank:** 8 STAR stories from real events (a bug you found, an incident, a user request, a benchmark surprise, a decision you reversed), each mapped to Leadership Principles and Googleyness-style themes (ownership, ambiguity, collaboration, learning from failure).
- [ ] **System design drills** using your own system as the seed: design a rate limiter, a notification service, a scheduler, a webhook delivery service, an email queue. Practise explaining trade-offs aloud, with numbers.
- [ ] **Mock interviews** weekly (peers, Pramp/Interviewing.io-style platforms, seniors on LinkedIn). Record yourself explaining the project in 2/5/15 minutes.
- [ ] **Applications:** referrals first (cold message engineers with a *specific* question about their work and a link to your repo, not "please refer me"), then company portals, then startups that pay well. Track in a spreadsheet. Apply to many places so a single rejection is not the plan.
- [ ] Keep **DSA daily** the entire time (aim for steady daily practice; mix topics; review mistakes). It is the gate to every loop.

---

## Tailoring

**For Amazon** (Leadership Principles, ownership, operations)
- Lead with operations: the live service, SLOs, the COE, the canary, the restore drill. "I carry the pager for my own system" is on-brand.
- Bring numbers and customers: users, jobs/day, latency, cost. Frame decisions as trade-offs with data ("Dive Deep", "Frugality": one VPS, measured cost per 1,000 jobs).
- Practise the 6-pager style and LP-tagged stories. Expect an online assessment (coding + work simulation) before any project conversation.

**For Google** (algorithms, correctness, engineering rigour)
- Lead with correctness: invariants, the lease-fencing proof sketch, property tests, optionally the TLA+ model.
- Lead with performance engineering: profile -> hypothesis -> change -> measurement, and the honest scaling curve.
- Expect heavier DSA and design-of-code questions; the project mostly earns you a credible conversation and a resume screen pass, not a shortcut.

## What a "9" looks like in two minutes (the README top section)
1. Live URL + status badge + uptime history.
2. Usage chart from real external users.
3. Scaling curve on separated hardware, plus the head-to-head comparison.
4. Chaos matrix, all green, including `kill -9`, DB outage, network faults, clock skew.
5. Link to the COE and the threat model.
6. Two merged open-source PRs and a demo video.

## Anti-patterns to avoid
- Padding: adding technologies you cannot explain in depth.
- Inflating: any user count, throughput, or "production" claim you cannot show a graph for.
- Rewriting instead of measuring: change code only when a measurement or a failing test says to.
- Skipping the boring parts: backups, alerts and restore drills are what separates a system from a demo.
- Letting the project crowd out DSA. A 9/10 project with weak DSA fails the online assessment before anyone opens your repo.
