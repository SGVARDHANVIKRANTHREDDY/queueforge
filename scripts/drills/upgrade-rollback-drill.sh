#!/usr/bin/env bash
# Upgrade / rollback / failed-migration drill (local, Docker only, disposable containers).
#
# It answers three questions with measurements:
#   1. Can you deploy a NEW version that carries a database migration while jobs are flowing, without
#      losing, duplicating or stranding any job? (expand-then-roll order: migrate first, then workers, then API)
#   2. Can you ROLL BACK to the previous code while the new column still exists? (migrations are
#      forward-only, so the old binary must start against the newer schema; this is the part most
#      projects never test)
#   3. If a migration FAILS, is the database left untouched, and does the running system carry on?
#
# A client keeps submitting batches the whole time, and retries a batch with the SAME idempotency key
# until it is accepted, exactly like the SDK does. At the end every submitted job must exist exactly once
# and have succeeded exactly once.
#
# What it builds (from the COMMITTED state of the repo, `git archive HEAD`, so commit first):
#   qf-drill-old : the code as committed                           (the "previous release")
#   qf-drill-new : the same code + migration 00007 (adds a nullable column)   (the "new release")
#   qf-drill-bad : like new + migration 00008 that FAILS halfway             (the "broken release")
#
# Run in Git Bash, WSL or Linux from anywhere inside the repo:  bash scripts/drills/upgrade-rollback-drill.sh
# Takes about 10 to 20 minutes (three image builds the first time). Env: KEEP=1 keeps containers for
# inspection; BAD_MIGRATION=0 skips the failed-migration phase; PHASE_SECONDS (default 12) is how long
# traffic runs in each phase.
# This is a LOCAL drill: it proves the procedure and the compatibility; real deployments still need their
# own rehearsal with real data volumes.
set -euo pipefail
export MSYS_NO_PATHCONV=1 MSYS2_ARG_CONV_EXCL='*'
cd "$(git rev-parse --show-toplevel)"

NET=qf-upg-net; PG=qf-upg-pg; API=qf-upg-api; WRK=qf-upg-worker; LOAD=qf-upg-load
OLD=qf-drill-old; NEW=qf-drill-new; BAD=qf-drill-bad
PGPW=upgradepass; DB=queueforge
URL="postgres://postgres:$PGPW@$PG:5432/$DB?sslmode=disable"
PHASE_SECONDS="${PHASE_SECONDS:-12}"
BAD_MIGRATION="${BAD_MIGRATION:-1}"
mkdir -p evidence
OUT="evidence/upgrade-drill-$(date -u +%Y%m%dT%H%M%SZ).txt"
exec > >(tee -a "$OUT") 2>&1

cleanup() {
  if [[ "${KEEP:-0}" != "1" ]]; then
    docker rm -f "$PG" "$API" "$WRK" "$LOAD" >/dev/null 2>&1 || true
    docker network rm "$NET" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT
now() { date +%s.%N; }
secs() { awk -v a="$1" -v b="$2" 'BEGIN { printf "%.1f", b - a }'; }
hostpath() { if command -v cygpath >/dev/null 2>&1; then cygpath -w "$1"; else echo "$1"; fi; }
psq() { docker exec "$PG" psql -U postgres -d "$DB" -Atc "$1"; }
curlnet() { docker run --rm --network "$NET" curlimages/curl:8.10.1 -s "$@"; }
admin() { local img="$1"; shift; docker run --rm --network "$NET" -e QF_DATABASE_URL="$URL" "$img" /app/admin "$@"; }
fail() { echo "UPGRADE_DRILL_RESULT verdict=FAIL reason=$*"; echo "--- last API log lines ---"; docker logs --tail 25 "$API" 2>&1 || true; exit 1; }
wait_pg() { for _ in $(seq 1 60); do docker exec "$PG" pg_isready -U postgres -d "$DB" >/dev/null 2>&1 && return 0; sleep 1; done; fail "postgres not ready"; }
wait_ready() { for _ in $(seq 1 90); do [[ "$(curlnet -o /dev/null -w '%{http_code}' "http://$API:8080/readyz" || true)" == "200" ]] && return 0; sleep 1; done; return 1; }
succeeded() { psq "SELECT count(*) FROM jobs WHERE status='succeeded'" | tr -d '[:space:]'; }
schema_version() { psq "SELECT max(version_id) FROM goose_db_version" | tr -d '[:space:]'; }
start_api() { docker run -d --name "$API" --network "$NET" -e QF_DATABASE_URL="$URL" -e QF_HTTP_ADDR=0.0.0.0:8080 -e QF_METRICS_ADDR=0.0.0.0:9090 "$1" /app/api >/dev/null; }
start_worker() { docker run -d --name "$WRK" --network "$NET" -e QF_DATABASE_URL="$URL" -e QF_METRICS_ADDR=0.0.0.0:9090 "$1" /app/worker >/dev/null; }
progress_during() { # progress_during <label>: traffic must keep being processed for PHASE_SECONDS
  local a b; a=$(succeeded); sleep "$PHASE_SECONDS"; b=$(succeeded)
  echo "  [$1] jobs succeeded: $a -> $b during $PHASE_SECONDS s"
  (( b > a )) || fail "no job completed during phase '$1' (system stalled)"
}

# build_image <tag> [extra migration file ...]: builds from the committed tree plus the extra files
build_image() {
  local tag="$1"; shift
  local dir; dir=$(mktemp -d)
  git archive HEAD | tar -x -C "$dir"
  for f in "$@"; do cp "$f" "$dir/migrations/$(basename "$f")"; done
  docker build -q -f "$(hostpath "$dir/deploy/Dockerfile")" -t "$tag" "$(hostpath "$dir")" >/dev/null || { rm -rf "$dir"; fail "docker build of $tag failed"; }
  rm -rf "$dir"
}

echo "== upgrade / rollback drill $(date -u +%FT%TZ) on commit $(git rev-parse --short HEAD) =="
if ! git diff --quiet HEAD -- 2>/dev/null; then echo "WARNING: uncommitted changes exist; the drill builds from the COMMITTED state only."; fi

TMPM=$(mktemp -d)
cat > "$TMPM/00007_drill_add_nullable_column.sql" <<'SQL'
-- +goose Up
-- Additive and nullable with no default: metadata-only, and safe for the PREVIOUS code to run against.
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS drill_note text;
-- +goose Down
ALTER TABLE jobs DROP COLUMN IF EXISTS drill_note;
SQL
cat > "$TMPM/00008_drill_broken_migration.sql" <<'SQL'
-- +goose Up
ALTER TABLE jobs ADD COLUMN drill_ok integer;
SELECT * FROM drill_table_that_does_not_exist;
-- +goose Down
ALTER TABLE jobs DROP COLUMN IF EXISTS drill_ok;
SQL

echo "-- 0. building images from the committed tree (first build takes a few minutes)"
t=$(now); build_image "$OLD"; build_image "$NEW" "$TMPM/00007_drill_add_nullable_column.sql"
[[ "$BAD_MIGRATION" == "1" ]] && build_image "$BAD" "$TMPM/00007_drill_add_nullable_column.sql" "$TMPM/00008_drill_broken_migration.sql"
rm -rf "$TMPM"
echo "images built in $(secs "$t" "$(now)") s"

docker rm -f "$PG" "$API" "$WRK" "$LOAD" >/dev/null 2>&1 || true
docker network rm "$NET" >/dev/null 2>&1 || true
docker network create "$NET" >/dev/null

echo "-- 1. baseline: PREVIOUS release on a fresh database"
docker run -d --name "$PG" --network "$NET" -e POSTGRES_PASSWORD="$PGPW" -e POSTGRES_DB="$DB" postgres:16-alpine >/dev/null
wait_pg
admin "$OLD" migrate >/dev/null
V0=$(schema_version); echo "schema version after the old release's migrations: $V0"
TID=$(admin "$OLD" create-tenant -name upgrade-demo -rate 100000 -burst 100000 -pending 1000000 -concurrent 16 | grep -Eo '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}' | sed -n 1p)
KEY=$(admin "$OLD" create-api-key -tenant-id "$TID" -name upgrade-drill | grep -Eo 'qf_live_[A-Za-z0-9_-]+' | awk '{ if (length($0) > length(m)) m = $0 } END { print m }')
[[ -n "$TID" && -n "$KEY" ]] || fail "could not create tenant/key"
start_api "$OLD"; wait_ready || fail "old API did not become ready"
start_worker "$OLD"

echo "-- 2. start continuous client traffic (retries each batch with the SAME idempotency key until accepted)"
JOB='{"task_type":"test.sleep","payload":{"ms":200}}'
BODY='{"jobs":['; for i in $(seq 1 10); do BODY+="$JOB"; (( i < 10 )) && BODY+=','; done; BODY+=']}'
docker run -d --name "$LOAD" --network "$NET" -e KEY="$KEY" -e BODY="$BODY" -e API_HOST="$API" --entrypoint sh curlimages/curl:8.10.1 -c '
i=0
while [ ! -f /tmp/stop ]; do
  i=$((i+1)); tries=0
  while true; do
    code=$(curl -s -o /dev/null -m 5 -w "%{http_code}" -X POST "http://$API_HOST:8080/v1/jobs/batch" \
      -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" -H "Idempotency-Key: upg-$i" -d "$BODY")
    case "$code" in 202|200) break ;; esac
    tries=$((tries+1)); sleep 0.2
  done
  echo "SENT $i tries=$tries"
  sleep 0.3
done
echo "DONE $i"
' >/dev/null
progress_during "baseline, previous release"

echo "-- 3. EXPAND: run the NEW release's migration while the OLD code is still serving traffic"
t=$(now); admin "$NEW" migrate >/dev/null || fail "new migration failed"; t2=$(now)
V1=$(schema_version); echo "UPGRADE_DRILL_RESULT migrate_new_seconds=$(secs "$t" "$t2") schema_version_before=$V0 schema_version_after=$V1"
[[ "$V1" == "$((V0 + 1))" ]] || fail "schema version did not advance by one ($V0 -> $V1)"
[[ "$(psq "SELECT count(*) FROM information_schema.columns WHERE table_name='jobs' AND column_name='drill_note'" | tr -d '[:space:]')" == "1" ]] || fail "new column missing"
wait_ready || fail "old API stopped answering after the migration"
progress_during "OLD code running on the NEW schema"

echo "-- 4. DEPLOY: replace the worker, then the API, with the NEW release"
docker stop -t 30 "$WRK" >/dev/null; docker rm "$WRK" >/dev/null; start_worker "$NEW"
t=$(now); docker rm -f "$API" >/dev/null; start_api "$NEW"; wait_ready || fail "NEW API did not become ready"
echo "UPGRADE_DRILL_RESULT api_downtime_upgrade_seconds=$(secs "$t" "$(now)")"
progress_during "NEW release"

echo "-- 5. ROLLBACK: put the OLD code back while the new column still exists"
docker stop -t 30 "$WRK" >/dev/null; docker rm "$WRK" >/dev/null; start_worker "$OLD"
t=$(now); docker rm -f "$API" >/dev/null; start_api "$OLD"
wait_ready || fail "ROLLBACK_BLOCKED: the old release cannot start against the newer schema (see log lines below)"
echo "UPGRADE_DRILL_RESULT api_downtime_rollback_seconds=$(secs "$t" "$(now)") schema_version_during_rollback=$(schema_version)"
progress_during "OLD code after rollback"

if [[ "$BAD_MIGRATION" == "1" ]]; then
  echo "-- 6. FAILED MIGRATION: a release whose migration breaks halfway"
  set +e; admin "$BAD" migrate >/tmp/upg-bad-migrate.txt 2>&1; rc=$?; set -e
  echo "migrate exit code: $rc (must be non-zero); last lines:"; tail -4 /tmp/upg-bad-migrate.txt | sed 's/^/    /'
  [[ "$rc" != "0" ]] || fail "the broken migration was reported as success"
  VBAD=$(schema_version)
  COL=$(psq "SELECT count(*) FROM information_schema.columns WHERE table_name='jobs' AND column_name='drill_ok'" | tr -d '[:space:]')
  echo "UPGRADE_DRILL_RESULT failed_migration_exit_code=$rc schema_version_after=$VBAD partial_column_left_behind=$COL"
  [[ "$VBAD" == "$V1" && "$COL" == "0" ]] || fail "failed migration left the database changed (version $VBAD, partial column count $COL)"
  progress_during "running system after the failed migration"
fi

echo "-- 7. stop traffic, let everything finish, verify nothing was lost, duplicated or stranded"
docker exec "$LOAD" touch /tmp/stop
for _ in $(seq 1 60); do [[ "$(docker inspect -f '{{.State.Running}}' "$LOAD")" == "false" ]] || sleep 1; done
LOG=$(docker logs "$LOAD" 2>&1)
SENT=$(echo "$LOG" | awk '/^DONE/ { print $2 }')
[[ -n "$SENT" ]] || fail "the traffic generator did not finish cleanly"
RETRIED=$(echo "$LOG" | awk '/^SENT/ { split($3, a, "="); if (a[2] > 0) n++ } END { print n+0 }')
EXPECT=$(( SENT * 10 ))
echo "UPGRADE_DRILL_RESULT batches_sent=$SENT jobs_expected=$EXPECT batches_that_needed_retries=$RETRIED"
for _ in $(seq 1 180); do [[ "$(succeeded)" == "$EXPECT" ]] && break; sleep 1; done
total=$(psq "SELECT count(*) FROM jobs" | tr -d '[:space:]')
ok=$(succeeded)
dead=$(psq "SELECT count(*) FROM jobs WHERE status='dead'" | tr -d '[:space:]')
busy=$(psq "SELECT count(*) FROM tenant_execution_slots WHERE current_job_id IS NOT NULL" | tr -d '[:space:]')
unfinished=$(psq "SELECT count(*) FROM job_attempts WHERE finished_at IS NULL" | tr -d '[:space:]')
outstanding=$(psq "SELECT outstanding_jobs FROM tenant_usage WHERE tenant_id='$TID'" | tr -d '[:space:]')
not_one=$(psq "SELECT count(*) FROM jobs j WHERE j.status='succeeded' AND (SELECT count(*) FROM job_attempts a WHERE a.job_id=j.id AND a.outcome='succeeded') <> 1" | tr -d '[:space:]')
echo "UPGRADE_DRILL_RESULT jobs_in_db=$total succeeded=$ok dead=$dead busy_slots=$busy unfinished_attempts=$unfinished outstanding=$outstanding not_exactly_one_success=$not_one"
[[ "$total" == "$EXPECT" ]] || fail "job count mismatch: submitted $EXPECT, database has $total (lost or duplicated jobs)"
[[ "$ok" == "$EXPECT" && "$dead" == "0" && "$busy" == "0" && "$unfinished" == "0" && "$outstanding" == "0" && "$not_one" == "0" ]] || fail "integrity check failed"
echo "UPGRADE_DRILL_RESULT verdict=PASS"
