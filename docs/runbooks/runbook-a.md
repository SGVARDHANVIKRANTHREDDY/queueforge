# Runbook A — Queue Backlog Rising

## 1. Symptoms & Alerts
- Prometheus alert: `queueforge_jobs_queued{queue="default"} > 1000` for more than 5 minutes.
- Latency alert: `queueforge_job_queue_wait_seconds` p95 exceeding SLA threshold (> 30s).
- Dashboard overview showing queued jobs growing faster than completion rate.

## 2. Diagnostic Steps
1. **Check Worker Registry:**
   Verify worker instances are online and their heartbeats are fresh (< 30s):
   ```bash
   curl -s -H "Authorization: Bearer <API_KEY>" http://localhost:8080/v1/workers
   ```
2. **Inspect Queue Statistics:**
   Determine whether a specific queue or task type is accumulating:
   ```bash
   curl -s -H "Authorization: Bearer <API_KEY>" http://localhost:8080/v1/queues/default/stats
   ```
3. **Check Database Pool Utilization:**
   Inspect `queueforge_db_pool_connections{state="acquired"}` vs `max_conns`. If workers are starving for DB connections, claims will stall.
4. **Identify Slow Handlers:**
   Check `queueforge_job_execution_duration_seconds` to see if a handler is hanging on slow external network calls or CPU operations.
5. **Inspect Tenant Execution Slots:**
   Verify whether a single tenant has hit its `max_concurrent_jobs` ceiling while other tenants' jobs are queued.

## 3. Resolution & Mitigation
- **Horizontal Scaling:** If DB CPU and pool connections have headroom, scale worker replicas (`docker compose up -d --scale worker=4`).
- **Adjust Tenant Concurrency:** If one tenant is monopolizing slots, increase their limit via `admin update-tenant-limits --tenant-id <id> --max-concurrent-jobs 8`.
- **Enforce Handler Timeouts:** If handlers are blocked on third-party HTTP dependencies, ensure handlers pass context deadlines to outbound HTTP clients.
- **Queue Partitioning:** Move heavy background tasks to dedicated queues (`background`, `maintenance`) to protect high-priority queues (`notifications`).
