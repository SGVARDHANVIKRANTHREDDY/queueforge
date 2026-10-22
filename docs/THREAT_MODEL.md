# Threat Model

Method: STRIDE over the data-flow below. Each threat states the mitigation, **how it is verified**, and the residual risk. "Gap" means unmitigated; those are listed again at the end so nobody has to hunt.

## 1. System and trust boundaries

```
 Tenant client ──HTTPS──▶ [Caddy TLS] ──▶ API ──┐
 Operator ──SSH──▶ admin CLI ───────────────────┼──▶ PostgreSQL ◀── Workers ──▶ job handlers
 Prometheus ──(private net)──▶ API/worker ops ──┘                              (tenant-supplied
                                                                                 task logic runs here)
```
Boundaries: (B1) Internet → Caddy/API; (B2) tenant ↔ tenant (same API, same database); (B3) API/worker ↔ PostgreSQL; (B4) operator ↔ system; (B5) worker ↔ the code that runs inside a job.

**Assets:** tenant job payloads and results (may contain business data), API key secrets, the audit log, availability of the queue, the integrity of job state (a job must not be lost, run by two owners, or mis-attributed).

**Assumptions:** PostgreSQL and the host are trusted and patched; operators are trusted but audited; TLS terminates at the reverse proxy; the handlers registered in `cmd/worker` are the operator's own code (this is **not** a sandbox for untrusted code).

## 2. Threats

| # | STRIDE | Threat | Mitigation | Verified by | Residual risk |
|---|---|---|---|---|---|
| T1 | Spoofing | Stolen or guessed API key | 256-bit random secrets stored only as SHA-256 hashes; expiry (DB clock); revocation; rotation with overlap (`create-api-key -expires-in`) | `TestExpiredAndRevokedKeysAreRejected`, `TestKeyRotationWithOverlap` | A stolen key is valid until revoked or expired. Use short expiries. |
| T2 | Spoofing | Brute-forcing keys | 20 failed attempts/min/client then HTTP 429 before any DB work; `X-Forwarded-For` trusted only when configured | `TestBruteForceIsThrottledBeforeTouchingTheDatabase`, `TestClientKey_ForwardedForOnlyWhenTrusted`, `TestBruteForceCannotBeBypassedWithSpoofedForwardingHeaders` (added after a code review found chi's `RealIP` middleware was rewriting the client address from spoofable headers regardless of `QF_TRUST_FORWARDED_FOR`; it is now installed only when that flag is set) | In-memory, per API process (N replicas = N x limit). 256-bit keys make guessing infeasible; the limiter is for cost control. |
| T3 | Tampering | One tenant reads or modifies another's jobs/schedules | Every repository query is tenant-scoped; unknown and foreign ids both return 404 (no existence oracle) | `TestTenantIsolationFuzz` (400 randomized cross-tenant attacks + listing checks + state hash), mutation-checked | Only the HTTP surface is fuzzed; direct database access bypasses it (see T14). |
| T4 | Tampering | A stale or buggy worker overwrites a job now owned by another worker | Lease tokens fence every state change (`WHERE lease_token = $2`) | `TestStateMachine` (stale completions after re-claim), mutation-checked; `TestChaos_RealKill9...` | None known. |
| T5 | Tampering | Duplicate submission (client retry) creates duplicate work | Idempotency keys with request-hash comparison (same key + different body = 409) | integration tests; `deploy/smoke.sh` | Protects submission only. Execution is at-least-once: handlers must be idempotent. |
| T6 | Repudiation | "Who cancelled / replayed / deleted that?" | Append-only `audit_log` (trigger blocks UPDATE/DELETE) with actor key, route, target, request id, remote address; operator actions from the CLI are recorded | `TestAuditLogRecordsControlPlaneActionsOnly`, `TestAuditLogIsAppendOnly` | A database superuser can still alter history; ship the log to external storage for tamper-evidence (**gap**). |
| T7 | Information disclosure | Infrastructure details leak through `/v1/workers`; job payloads in logs; secrets echoed in handler errors; internal error text in 500s | Tenants get liveness only unless `workers:read`; worker/API logs carry ids, not payloads; `domain.RedactSecrets` masks tokens, bearer headers, URL credentials and `password=` style pairs before an error is stored (unit-tested); 500 responses no longer include internal error text | `TestWorkersEndpointDoesNotLeakInfrastructure` | Error messages may include handler error text; handlers must not put secrets in errors (**gap**: no redaction layer). |
| T8 | Information disclosure | Metrics/ops endpoints exposed publicly | Served on a separate loopback/private listener, 404 on the public port, blocked again at Caddy | `deploy/smoke.sh` ("/metrics not public") | Ops endpoints are unauthenticated and rely on network placement. |
| T9 | Denial of service | Request flood / oversized bodies | `MaxBytesReader` (request and payload limits), per-tenant token bucket + pending quota, per-tenant execution slots, per-client-address token bucket in memory before any DB work (`QF_CLIENT_RATE_PER_SECOND`; off by default, on in the prod compose and k8s files) | integration tests (quota, rate limit) | No global per-IP rate limit on authenticated traffic (**gap**: do it at the proxy). |
| T10 | Denial of service | Noisy tenant starves others | Slot limits, pending quota, set-based fair claim | `TestClaim_SaturatedTenantDoesNotStarveOthers`, `TestStateMachine` | Bounded by the pending quota (ADR-015, "known remaining limit"). |
| T11 | Denial of service | Backlog of one task type slows every worker pool | Claim cost independent of unrelated backlog | `TestClaim_CostIndependentOf*` | None known at tested scales. |
| T12 | Denial of service | Database outage or network faults | Workers survive, retry commits, leases recover work | `TestChaos_PostgresOutageMidWorkload`, `TestChaos_NetworkFaults...` | Single PostgreSQL primary = single point of failure; recovery is restore-from-backup (documented, not automated). |
| T13 | Elevation of privilege | Key with `jobs:write` calls admin operations | Per-route permission checks (`RequirePermission`); there is no admin API (admin is CLI-only) | integration tests | Permission strings are free text; a typo silently grants nothing. |
| T14 | Elevation of privilege | Database credentials with superuser rights used by the app | Optional split: `QF_MIGRATE_DATABASE_URL` (schema owner, migrations only) and a DML-only app role created by `deploy/sql/least_privilege.sql` (no DDL; no UPDATE/DELETE on `audit_log`) | **Not yet verified by a test or a real deploy** (written without a database to run against) | Still opt-in: if you do not set the two URLs the app runs as the owner. Run the script and the smoke test before trusting it. |
| T15 | Tampering | Time skew between machines corrupts scheduling and rate limits | Database is the only clock | `TestNoApplicationClockInPostgresLayer` (ADR-014) | Database clock itself must be sane (monitor NTP). |
| T16 | Supply chain | Vulnerable dependency or base image | CI runs `govulncheck`, `trivy`, `gitleaks` (see `.github/workflows/ci.yml`); images pinned; container runs as non-root | CI configuration | Gates run in CI; **they could not be executed in the authoring sandbox** (no access to the vulnerability database). Enable and read the first report yourself. |
| T17 | Information disclosure | Secrets in git history | `.env.prod` ignored; `gitleaks` in CI | CI configuration | Run `gitleaks detect` once locally over full history before publishing. |
| T18 | Tampering | Malicious job handler (B5) | Out of scope: handlers are operator code. Timeouts and lease fencing limit blast radius of a *hung* handler, not a malicious one | - | Do not run untrusted code in workers; use a sandbox (container per job) if you ever need to. |

## 3. Open gaps (ordered by value)
1. **Verify the least-privilege roles** (T14): run `deploy/sql/least_privilege.sql`, point `QF_DATABASE_URL` at `qf_app` and `QF_MIGRATE_DATABASE_URL` at `qf_migrate`, then run the smoke test and the integration suite against it.
2. **Database TLS** (`sslmode=verify-full`) as soon as PostgreSQL is not on the same private network.
3. **Ship audit logs off-host** for tamper evidence (T6).
4. **Per-IP / per-key rate limiting at the proxy** for authenticated floods (T9).
5. **Secrets management**: environment files are acceptable for one VPS, not for a team (Vault/SOPS/cloud secrets).
6. **Automated dependency and image scanning actually executed and triaged** (T16).
7. An **external review**. This document is the author's own analysis; it is a starting point, not an audit.
