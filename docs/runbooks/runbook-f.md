# Runbook F: Host disk space low or filling fast

**Alerts:** `HostDiskSpaceLow` (under 15% free, ticket), `HostDiskSpaceCritical` (under 8% free, page),
`HostDiskFillingFast` (projected to fill within 6 hours), `HostDiskInodesLow`, `HostDiskMetricsMissing`.

**Why it matters:** PostgreSQL stops accepting writes (and can fail to start) when its disk is full.
That stops enqueue, claiming and completion at once. Treat Critical as an outage in progress.

## 1. Confirm and size the problem (2 minutes)

```bash
df -h /                         # how full, and how much is left
docker system df -v | head -40  # images, containers, volumes, build cache
sudo du -xhd1 /var/lib/docker | sort -h | tail
```

Then see what inside PostgreSQL is big:

```bash
qf exec postgres psql -U postgres -d queueforge -c "SELECT relname, pg_size_pretty(pg_total_relation_size(oid)) AS size FROM pg_class WHERE relkind='r' AND relnamespace='public'::regnamespace ORDER BY pg_total_relation_size(oid) DESC LIMIT 8;"
```

(`qf` is the compose alias from docs/DEPLOYMENT.md.) The usual suspects, in order of likelihood:

| Biggest item | Likely cause | Go to |
|---|---|---|
| `jobs`, `job_attempts`, `job_audit_events` | Retention is not keeping up, or retention period is long for your volume | step 2 |
| Docker logs under `/var/lib/docker/containers` | json-file logging without `max-size` / `max-file` | step 3 |
| Backups in `/var/backups/queueforge` | Dumps kept on the same disk with no pruning | step 4 |
| Images and build cache | Many deploys without pruning | step 5 |
| Something unrelated | Not QueueForge: find it with the `du` output above | your own investigation |

## 2. Database growth: is retention behind?

In Prometheus, look at `queueforge_retention_backlog_jobs` (expired terminal jobs waiting to be
deleted). If it is large or rising, retention is not keeping up: check `qf logs worker | grep -i retention`
for errors first. If it is near zero, the data is simply not yet old enough to delete: the table is
big because you are creating jobs faster than your retention period expires them. Then either add disk
(best) or shorten the retention period (a deliberate data-loss decision: record it).

Note that deleting rows does not shrink the files: PostgreSQL reuses the space for new rows, but the disk
only gets space back after `VACUUM FULL` (takes an exclusive lock: do it only in a maintenance window,
with enough free disk for a full copy of the table) or after a dump and restore.

## 3. Container logs

```bash
sudo du -sh /var/lib/docker/containers/*/*-json.log | sort -h | tail
```

Set limits in the compose file for every service (`logging: {driver: json-file, options: {max-size: "10m", max-file: "3"}}`),
redeploy, and truncate the biggest log (`sudo truncate -s 0 <file>`) only after reading what is in it.

## 4. Backups

Confirm the newest dump has been copied OFF the machine, then delete old local dumps. Never delete the only
copy of the only recent backup.

## 5. Images and cache

```bash
docker image prune -a --filter "until=168h"
docker builder prune --filter "until=168h"
```

## 6. If the disk is already at 100%

1. Free a few hundred MB first (step 5, old backups) so PostgreSQL can write again.
2. `qf ps` and check `postgres` is healthy; if it crashed, `qf restart postgres` once space exists.
3. Check workers drained any stuck outcome writes: `queueforge_jobs_runnable` should fall back to normal; the
   jobs workers could not record will be re-run after their leases expire (that is why handlers must be idempotent).
4. Add disk (resize the volume or the VPS) before you do anything else clever.

## 7. After the incident

Write it in `docs/OPERATIONS_LOG.md` (time, size when detected, cause, fix, prevention). Typical prevention:
log limits, an off-machine backup job that prunes local copies, a bigger disk, and a lower alert threshold
if you had too little warning.
