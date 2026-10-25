#!/usr/bin/env bash
# Local restore drill (Docker only; nothing is installed and no host paths are mounted).
#
# What it proves: a database backup of a QueueForge system with FINISHED and QUEUED jobs can be restored
# into a brand-new PostgreSQL, the data matches what was backed up, usage counters need no repair, and the
# restored system drains the remaining work correctly. It also shows the RPO: jobs created after the
# backup are NOT in the restored system.
#
# Run in Git Bash, WSL or Linux, from anywhere inside the repo:   bash scripts/drills/restore-drill-local.sh
# Requires: docker, bash, curl is NOT needed (a throwaway curl container is used). First run pulls
# postgres:16-alpine and curlimages/curl. If the image queueforge:local does not exist it is built (takes minutes).
# This is a LOCAL drill with disposable containers: it demonstrates the procedure and timings on your
# laptop; it is not production evidence. Real RTO/RPO need your real data size and a real server.
# Env: IMG (default queueforge:local), KEEP=1 to keep the containers afterwards for inspection.
set -euo pipefail
export MSYS_NO_PATHCONV=1 MSYS2_ARG_CONV_EXCL='*'   # Git Bash: do not rewrite /app/... style arguments
cd "$(git rev-parse --show-toplevel)"

IMG="${IMG:-queueforge:local}"
NET=qf-restore-net; SRC=qf-restore-src; DST=qf-restore-dst; API=qf-restore-api; WRK=qf-restore-worker
PGPW=restorepass; DB=queueforge
SRC_URL="postgres://postgres:$PGPW@$SRC:5432/$DB?sslmode=disable"
DST_URL="postgres://postgres:$PGPW@$DST:5432/$DB?sslmode=disable"
DUMP="$(mktemp)"
mkdir -p evidence
OUT="evidence/restore-drill-$(date -u +%Y%m%dT%H%M%SZ).txt"
exec > >(tee -a "$OUT") 2>&1

cleanup() {
  rm -f "$DUMP"
  if [[ "${KEEP:-0}" != "1" ]]; then
    docker rm -f "$SRC" "$DST" "$API" "$WRK" >/dev/null 2>&1 || true
    docker network rm "$NET" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT
now() { date +%s.%N; }
secs() { awk -v a="$1" -v b="$2" 'BEGIN { printf "%.1f", b - a }'; }
psql_src() { docker exec "$SRC" psql -U postgres -d "$DB" -Atc "$1"; }
psql_dst() { docker exec "$DST" psql -U postgres -d "$DB" -Atc "$1"; }
curlnet() { docker run --rm --network "$NET" curlimages/curl:8.10.1 -s "$@"; }
admin() { local url="$1"; shift; docker run --rm --network "$NET" -e QF_DATABASE_URL="$url" "$IMG" /app/admin "$@"; }
fail() { echo "RESTORE_DRILL_RESULT verdict=FAIL reason=$*"; exit 1; }

wait_pg() { for _ in $(seq 1 60); do docker exec "$1" pg_isready -U postgres -d "$DB" >/dev/null 2>&1 && return 0; sleep 1; done; fail "postgres $1 did not become ready"; }
wait_http() { for _ in $(seq 1 60); do [[ "$(curlnet -o /dev/null -w '%{http_code}' "http://$API:8080/readyz" || true)" == "200" ]] && return 0; sleep 1; done; fail "api not ready"; }
# wait_eq <description> <timeout-seconds> <psql-function> <sql> <expected>
wait_eq() { local end=$(( $(date +%s) + $2 )) got; while true; do got=$($3 "$4" | tr -d '[:space:]' || true); [[ "$got" == "$5" ]] && return 0; (( $(date +%s) > end )) && fail "$1 (got '$got', wanted '$5')"; sleep 1; done; }

body() { # body <ms> -> a batch request of 100 jobs
  local i b='{"jobs":['
  for i in $(seq 1 100); do b+="{\"task_type\":\"test.sleep\",\"payload\":{\"ms\":$1}}"; (( i < 100 )) && b+=','; done
  echo "${b}]}"
}
post_batch() { # post_batch <key> <ms>
  local code; code=$(curlnet -o /dev/null -w '%{http_code}' -X POST "http://$API:8080/v1/jobs/batch" \
    -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" -H "Idempotency-Key: $1" -d "$(body "$2")")
  [[ "$code" == "202" || "$code" == "200" ]] || fail "batch $1 returned HTTP $code"
}
snapshot() { # prints jobs|succeeded|queued|attempts|api_keys|outstanding for the tenant
  "$1" "SELECT (SELECT count(*) FROM jobs), (SELECT count(*) FROM jobs WHERE status='succeeded'), (SELECT count(*) FROM jobs WHERE status='queued'), (SELECT count(*) FROM job_attempts), (SELECT count(*) FROM api_keys), (SELECT outstanding_jobs FROM tenant_usage WHERE tenant_id='$TID')"
}

echo "== local restore drill $(date -u +%FT%TZ), image $IMG =="
docker image inspect "$IMG" >/dev/null 2>&1 || docker build -f deploy/Dockerfile -t "$IMG" .
docker rm -f "$SRC" "$DST" "$API" "$WRK" >/dev/null 2>&1 || true
docker network rm "$NET" >/dev/null 2>&1 || true
docker network create "$NET" >/dev/null

echo "-- 1. source database, schema, tenant and key"
docker run -d --name "$SRC" --network "$NET" -e POSTGRES_PASSWORD="$PGPW" -e POSTGRES_DB="$DB" postgres:16-alpine >/dev/null
wait_pg "$SRC"
admin "$SRC_URL" migrate >/dev/null
TID=$(admin "$SRC_URL" create-tenant -name restore-demo -rate 100000 -burst 100000 -pending 100000 -concurrent 16 | grep -Eo '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}' | sed -n 1p)
[[ -n "$TID" ]] || fail "could not read the tenant id from the admin output"
KEY=$(admin "$SRC_URL" create-api-key -tenant-id "$TID" -name restore-drill | grep -Eo 'qf_live_[A-Za-z0-9_-]+' | awk '{ if (length($0) > length(m)) m = $0 } END { print m }')
[[ -n "$KEY" ]] || fail "could not read the API key from the admin output"
echo "tenant=$TID (key not printed)"

echo "-- 2. create state: 300 finished jobs and 200 still queued"
docker run -d --name "$API" --network "$NET" -e QF_DATABASE_URL="$SRC_URL" -e QF_HTTP_ADDR=0.0.0.0:8080 -e QF_METRICS_ADDR=0.0.0.0:9090 "$IMG" /app/api >/dev/null
wait_http
for b in 1 2 3; do post_batch "restore-drill-a$b" 0; done
docker run -d --name "$WRK" --network "$NET" -e QF_DATABASE_URL="$SRC_URL" -e QF_METRICS_ADDR=0.0.0.0:9090 "$IMG" /app/worker >/dev/null
wait_eq "300 jobs to succeed" 120 psql_src "SELECT count(*) FROM jobs WHERE status='succeeded'" 300
docker stop -t 30 "$WRK" >/dev/null   # graceful stop; from now on new jobs stay queued
for b in 1 2; do post_batch "restore-drill-b$b" 0; done
BEFORE=$(snapshot psql_src); echo "BEFORE backup  (jobs|succeeded|queued|attempts|api_keys|outstanding) = $BEFORE"
[[ "$BEFORE" == "500|300|200|300|1|200" ]] || fail "unexpected state before backup: $BEFORE"

echo "-- 3. take the backup (plain SQL dump)"
t0=$(now)
docker exec "$SRC" pg_dump -U postgres -d "$DB" --no-owner --no-privileges > "$DUMP"
t1=$(now)
DUMP_BYTES=$(wc -c < "$DUMP" | tr -d ' ')
echo "RESTORE_DRILL_RESULT backup_seconds=$(secs "$t0" "$t1") backup_bytes=$DUMP_BYTES"

echo "-- 4. work that arrives AFTER the backup (this is the RPO: it will be lost)"
post_batch "restore-drill-after-backup" 0   # 100 more jobs, queued
LOST=$(psql_src "SELECT count(*) FROM jobs" | tr -d ' ')
echo "source now has $LOST jobs; the backup contains 500"

echo "-- 5. DISASTER: the source database and API are destroyed"
t_disaster=$(now)
docker rm -f "$API" "$SRC" "$WRK" >/dev/null

echo "-- 6. restore into a brand-new PostgreSQL"
docker run -d --name "$DST" --network "$NET" -e POSTGRES_PASSWORD="$PGPW" -e POSTGRES_DB="$DB" postgres:16-alpine >/dev/null
wait_pg "$DST"
t2=$(now)
docker exec -i "$DST" psql -U postgres -d "$DB" -v ON_ERROR_STOP=1 -q < "$DUMP" >/dev/null || fail "restore failed"
t3=$(now)
echo "RESTORE_DRILL_RESULT restore_seconds=$(secs "$t2" "$t3")"
AFTER=$(snapshot psql_dst); echo "AFTER restore  (same columns) = $AFTER"
[[ "$AFTER" == "$BEFORE" ]] || fail "restored data differs from the backed-up state ($BEFORE vs $AFTER)"
echo "data identical to the backup moment: yes; the $((LOST - 500)) post-backup jobs are absent as expected"

echo "-- 7. usage counters need no repair"
OUT_BEFORE=$(psql_dst "SELECT outstanding_jobs FROM tenant_usage WHERE tenant_id='$TID'" | tr -d ' ')
RECON=$(admin "$DST_URL" reconcile-usage --tenant-id "$TID" 2>&1 || true); echo "reconcile-usage said: $RECON"
OUT_AFTER=$(psql_dst "SELECT outstanding_jobs FROM tenant_usage WHERE tenant_id='$TID'" | tr -d ' ')
echo "RESTORE_DRILL_RESULT outstanding_before_reconcile=$OUT_BEFORE outstanding_after_reconcile=$OUT_AFTER"
[[ "$OUT_BEFORE" == "$OUT_AFTER" ]] || fail "reconcile-usage changed the counter: the backup had drift"
admin "$DST_URL" migrate >/dev/null || fail "migrate against the restored database failed"

echo "-- 8. start the system on the restored database; the 200 queued jobs must drain"
docker run -d --name "$API" --network "$NET" -e QF_DATABASE_URL="$DST_URL" -e QF_HTTP_ADDR=0.0.0.0:8080 -e QF_METRICS_ADDR=0.0.0.0:9090 "$IMG" /app/api >/dev/null
docker run -d --name "$WRK" --network "$NET" -e QF_DATABASE_URL="$DST_URL" -e QF_METRICS_ADDR=0.0.0.0:9090 "$IMG" /app/worker >/dev/null
wait_http
code=$(curlnet -o /dev/null -w '%{http_code}' "http://$API:8080/v1/jobs/00000000-0000-0000-0000-000000000000" -H "Authorization: Bearer $KEY" || true)
[[ "$code" == "404" ]] || fail "the original API key does not authenticate on the restored system (HTTP $code)"
wait_eq "all 500 jobs to succeed" 180 psql_dst "SELECT count(*) FROM jobs WHERE status='succeeded'" 500
t4=$(now)
echo "RESTORE_DRILL_RESULT rto_seconds_disaster_to_all_work_done=$(secs "$t_disaster" "$t4")"

echo "-- 9. integrity after the drain"
dead=$(psql_dst "SELECT count(*) FROM jobs WHERE status='dead'" | tr -d ' ')
busy=$(psql_dst "SELECT count(*) FROM tenant_execution_slots WHERE current_job_id IS NOT NULL" | tr -d ' ')
unfinished=$(psql_dst "SELECT count(*) FROM job_attempts WHERE finished_at IS NULL" | tr -d ' ')
outstanding=$(psql_dst "SELECT outstanding_jobs FROM tenant_usage WHERE tenant_id='$TID'" | tr -d ' ')
total=$(psql_dst "SELECT count(*) FROM jobs" | tr -d ' ')
not_one=$(psql_dst "SELECT count(*) FROM jobs j WHERE j.status='succeeded' AND (SELECT count(*) FROM job_attempts a WHERE a.job_id=j.id AND a.outcome='succeeded') <> 1" | tr -d ' ')
echo "RESTORE_DRILL_RESULT jobs=$total dead=$dead busy_slots=$busy unfinished_attempts=$unfinished outstanding=$outstanding not_exactly_one_success=$not_one"
[[ "$total" == "500" && "$dead" == "0" && "$busy" == "0" && "$unfinished" == "0" && "$outstanding" == "0" && "$not_one" == "0" ]] || fail "integrity check failed"
echo "RESTORE_DRILL_RESULT verdict=PASS rpo_jobs_lost=$((LOST - 500)) (everything created after the backup)"
