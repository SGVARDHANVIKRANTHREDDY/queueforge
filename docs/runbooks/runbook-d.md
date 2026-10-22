# Runbook D — Database Unavailable

## 1. Symptoms & Alerts
- API `/readyz` probe fails with HTTP 503.
- Application error logs flood with `connection refused` or `pgxpool: acquire connection failed`.
- Prometheus scrape fails or reports zero database connections.

## 2. Diagnostic Steps
1. **Verify PostgreSQL Process Status:**
   ```bash
   docker ps --filter "name=postgres"
   docker logs --tail 100 queueforge_postgres
   ```
2. **Check Port Connectivity & Authentication:**
   ```bash
   pg_isready -h localhost -p 5435 -U postgres
   ```
3. **Inspect Disk Space & Memory:**
   Verify host has not run out of disk space (`df -h`). Exhausted disk space causes PostgreSQL to enter read-only safety mode.

## 3. Resolution & Mitigation
- **Restore PostgreSQL:** Restart container or fix storage allocation (`docker compose restart postgres`).
- **Fail-Closed Behavior:** QueueForge API automatically fails closed during DB downtime; no false 202 Accepted responses are returned to clients.
- **Worker Reconnection:** Worker processes and API servers will automatically reconnect once PostgreSQL recovers with exponential backoff.
- **Recovery of Abandoned Leases:** Once restored, the background recovery loop automatically scans for leases that expired during the outage and safely reschedules them.

## Retention falling behind (`QueueForgeRetentionFallingBehind`)

Symptom: `queueforge_retention_backlog_jobs` is large and not shrinking, `queueforge_retention_deleted_total{kind="jobs"}` is flat, and the `jobs` table keeps growing.

1. Is a worker running at all? Retention runs inside worker processes (`queueforge_workers_alive`).
2. Look for failures: `docker logs <worker> | grep "retention cleanup"`. A single poisoned row is isolated automatically (the rest of the batch is deleted one by one) and logged as `job <id>: ...`; fix or remove that row.
3. Is the rate simply too high? One pass drains up to 200 batches of 1,000 jobs (200k jobs) every `QF_RETENTION_INTERVAL` (default 10 minutes). If you create more than ~300 jobs/second sustained, shorten the interval or the retention window (`QF_TERMINAL_JOB_RETENTION`).
4. Is the index present? `\d jobs` must list `jobs_terminal_completed_idx`. If a deployment skipped migration 00006, the pass scans the whole table: re-run `admin migrate`.
5. Emergency disk relief: shorten `QF_TERMINAL_JOB_RETENTION` (for example `168h`) and restart the workers; the next pass deletes in oldest-first batches.
