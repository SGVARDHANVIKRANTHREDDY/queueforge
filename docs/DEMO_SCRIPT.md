# 5-Minute Demo Script

Goal: show **reliability under failure**, then **how it was verified**. Record it with any screen recorder; keep a terminal and the dashboard (`/ui/`) side by side.

**Setup (before recording):** `docker compose -f deploy/docker-compose.yml up -d --build --scale worker=2`; create a tenant and key with the admin CLI (see DEPLOYMENT.md section 4).

## 0:00 What it is (20 s)
"A multi-tenant job queue on PostgreSQL. Jobs are never lost, never owned by two workers, and one tenant cannot starve another. I'll break it live."

## 0:20 Load (45 s)
```bash
# 5,000 jobs that each take ~1 second
for i in $(seq 1 5000); do curl -s -XPOST $URL/v1/jobs -H "Authorization: Bearer $KEY" \
  -d '{"task_type":"test.sleep","payload":{"ms":1000}}' >/dev/null & done; wait
```
Show queue depth and workers on the dashboard / `curl $URL/v1/queues/default/stats`.

## 1:05 Kill a worker mid-job (60 s)
```bash
docker kill queueforge-worker-1            # SIGKILL: no cleanup possible
```
Narrate: "Its jobs are still `running` because a dead process cannot say otherwise." Watch the lease expire; the other worker recovers them. Show one job's attempts: `lease_expired` then `succeeded`, two different worker ids.

## 2:05 Kill the database (60 s)
```bash
docker stop queueforge-db; sleep 10; docker start queueforge-db
```
"Workers stay up, retry, and nothing is dropped." Show `queueforge_oldest_runnable_job_age_seconds` spike and fall.

## 3:05 Stale worker cannot overwrite (30 s)
Show the test `TestStateMachine` mutation: "I removed the lease check on purpose; my own property test failed in 20 steps."

## 3:35 How it was verified (60 s)
Open `docs/ENGINEERING_LOG.md`: 24 findings, each with a failing-first test. Pick two: the claim query that was O(backlog) (653 ms to 1.4 ms), and the alert that could never fire.

## 4:35 Honest limits (25 s)
"Tuned River is about 1.8x faster; I measured it. Single Postgres, no failover. At-least-once, so handlers must be idempotent." Point to `bench/headtohead/README.md`.

**Do not claim** user counts, uptime, or production use unless true.
