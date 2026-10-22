# Runbook C — Dead-Letter Spike

## 1. Symptoms & Alerts
- Prometheus alert: `queueforge_jobs_completed_total{outcome="dead"}` spikes significantly.
- Dashboard Dead Letters badge displays a high failure count.

## 2. Diagnostic Steps
1. **Query Dead-Letter Queue:**
   Fetch recent dead-letter jobs:
   ```bash
   curl -s -H "Authorization: Bearer <API_KEY>" "http://localhost:8080/v1/dead-letters?limit=25"
   ```
2. **Group by Task Type and Error Code:**
   Identify if failures are concentrated in one task type (e.g. `placement_notice.send`) with a shared error code (e.g. `permanent_error` or HTTP `502 Bad Gateway` from an external webhook).
3. **Inspect Attempt History:**
   Query the job detail for an affected job:
   ```bash
   curl -s -H "Authorization: Bearer <API_KEY>" http://localhost:8080/v1/jobs/<JOB_ID>
   ```
   Check attempt progression, duration, and sanitized error messages.

## 3. Resolution & Mitigation
- **Pause Originating Schedules:** If a recurring schedule is feeding bad jobs into the queue, pause it via `PATCH /v1/schedules/<ID>` with `{"paused": true}`.
- **Fix Upstream Dependency:** Resolve downstream API auth or outage issues.
- **Replay Failed Jobs:** Once the root cause is resolved, safely replay dead jobs:
  - Single replay: `POST /v1/jobs/<ID>/replay`
  - Replay transitions status back to `queued`, increments `replay_generation`, and creates an auditable record in `job_audit_events`.
