# Runbook B — High Lease Recovery Rate

## 1. Symptoms & Alerts
- Prometheus alert: Rate of `queueforge_job_lease_recoveries_total` exceeds normal baseline (> 1 recovery/min).
- Attempt history logs show frequent `worker_lease_expired` error codes.

## 2. Diagnostic Steps
1. **Check Worker Container Lifecycles:**
   Inspect container exit codes and restart counts:
   ```bash
   docker ps -a --filter "name=worker"
   ```
   Check system kernel logs for Out-Of-Memory (OOM) killer terminations (`dmesg -T | grep -i oom`).
2. **Review Lease Configuration Ratios:**
   Confirm that `QF_DEFAULT_JOB_TIMEOUT_SECONDS` is strictly <= `QF_MAX_JOB_TIMEOUT_SECONDS` and that `QF_LEASE_RENEW_INTERVAL` (20s) is materially smaller than `QF_DEFAULT_LEASE_DURATION` (60s).
3. **Inspect Database Latency & Stalls:**
   If PostgreSQL query latency spikes above 20 seconds, worker lease renewal queries will time out before committing, triggering false-positive lease expiries.
4. **Audit Handler Context Compliance:**
   Check whether handlers are executing blocking uncooperative code that fails to yield to context cancellation.

## 3. Resolution & Mitigation
- **Memory Limits:** Increase container memory limits if workers are being killed by OOM.
- **Tune Lease Duration:** If tasks legitimately take multiple minutes and database load is high, adjust `QF_DEFAULT_LEASE_DURATION=120s` and `QF_LEASE_RENEW_INTERVAL=30s`.
- **Database Vacuum & Indexing:** Ensure PostgreSQL autovacuum is active on `jobs` and `job_attempts` to avoid index scan degradation during renewals.
