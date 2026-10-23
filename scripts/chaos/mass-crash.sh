#!/usr/bin/env bash
# Mass-crash recovery drill: kill -9 EVERY worker at once while jobs are in flight, let them come
# back, and measure how long recovery takes and whether anything was lost or duplicated.
#
# WHY: lease recovery processes a bounded batch per tick. With one crashed worker that is invisible;
# when a whole fleet dies (host reboot, OOM, bad deploy) recovery time is what your users feel.
#
# RUN IT ONLY against a disposable / staging stack. It SIGKILLs the worker containers.
#
# Prerequisites
#   - Linux, WSL2 or macOS bash with: docker compose, curl, awk. (macOS: install coreutils for gdate.)
#   - A running stack from the compose file, workers scaled to N (the script scales them for you).
#   - A DEDICATED tenant with generous limits, e.g. (run inside the api container):
#       admin create-tenant -name masscrash -rate 100000 -burst 100000 -pending 100000 -concurrent 64
#       admin create-api-key -tenant-id <TENANT_UUID> -name masscrash
#     (-concurrent should be >= WORKERS x QF_WORKER_CONCURRENCY so that many jobs are in flight)
#
# Usage
#   CONFIRM=yes TENANT_ID=<uuid> API_KEY=qf_live_... BASE_URL=https://queue.example.com \
#   COMPOSE="docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env.prod" \
#   WORKERS=4 JOBS=3000 LEASE_SECONDS=30 ./scripts/chaos/mass-crash.sh | tee evidence/mass-crash.txt
#
# Environment
#   CONFIRM        must be "yes"
#   TENANT_ID      tenant that owns the test jobs (all SQL is scoped to it)
#   API_KEY        key for that tenant
#   BASE_URL       API base URL reachable from this machine (default http://localhost:8080)
#   COMPOSE        compose command prefix (default: docker compose -f deploy/docker-compose.prod.yml)
#   WORKERS        worker replicas (default 4)       JOBS  total jobs to enqueue (default 3000)
#   LEASE_SECONDS  your QF_DEFAULT_LEASE_DURATION in seconds, only used for the report (default 30)
#   PGUSER, PGDB   database user/name inside the postgres container (defaults postgres / queueforge)
#   JOB_MS         how long each job sleeps (default 600000 = 10 min, so all stay in flight)
set -euo pipefail

CONFIRM="${CONFIRM:-}"
TENANT_ID="${TENANT_ID:?set TENANT_ID}"
API_KEY="${API_KEY:?set API_KEY}"
BASE_URL="${BASE_URL:-http://localhost:8080}"
COMPOSE="${COMPOSE:-docker compose -f deploy/docker-compose.prod.yml}"
WORKERS="${WORKERS:-4}"
JOBS="${JOBS:-3000}"
LEASE_SECONDS="${LEASE_SECONDS:-30}"
PGUSER="${PGUSER:-postgres}"
PGDB="${PGDB:-queueforge}"
JOB_MS="${JOB_MS:-600000}"

if [[ "$CONFIRM" != "yes" ]]; then
  echo "Refusing to run: this kills every worker container. Re-run with CONFIRM=yes on a disposable stack." >&2
  exit 2
fi
if ! [[ "$TENANT_ID" =~ ^[0-9a-fA-F-]{36}$ ]]; then echo "TENANT_ID must be a UUID" >&2; exit 2; fi
if (( JOBS < 100 )) || (( JOBS % 100 != 0 )); then echo "JOBS must be a multiple of 100 (batch size), at least 100" >&2; exit 2; fi

now() { date +%s.%N; }
elapsed() { awk -v a="$1" -v b="$2" 'BEGIN { printf "%.1f", b - a }'; }
sql() { $COMPOSE exec -T postgres psql -U "$PGUSER" -d "$PGDB" -Atc "$1"; }
count() { sql "$1" | tr -d '[:space:]'; }
T="'$TENANT_ID'"

# wait_until "<description>" <timeout_seconds> "<sql returning an integer>" <op> <value>
wait_until() {
  local what="$1" timeout="$2" query="$3" op="$4" want="$5" start got
  start=$(now)
  while true; do
    got=$(count "$query" || echo -1)
    case "$op" in
      ge) (( got >= want )) && return 0 ;;
      eq) (( got == want )) && return 0 ;;
    esac
    if (( $(awk -v s="$start" -v n="$(now)" 'BEGIN { print (n - s > '"$timeout"') ? 1 : 0 }') )); then
      echo "TIMEOUT after ${timeout}s waiting for: $what (last value $got, wanted $op $want)" >&2
      exit 1
    fi
    sleep 0.5
  done
}

echo "== mass-crash drill: workers=$WORKERS jobs=$JOBS lease=${LEASE_SECONDS}s tenant=$TENANT_ID =="

# 0. Preconditions: the tenant must be empty so every count below is ours.
existing=$(count "SELECT count(*) FROM jobs WHERE tenant_id=$T")
if (( existing != 0 )); then echo "tenant already has $existing jobs; use a fresh dedicated tenant" >&2; exit 2; fi

# 1. Scale workers up and enqueue JOBS jobs in batches of 100 (atomic batch endpoint).
$COMPOSE up -d --scale worker="$WORKERS" worker >/dev/null
batch_body() {
  local i body='{"jobs":['
  for i in $(seq 1 100); do
    body+="{\"task_type\":\"test.sleep\",\"payload\":{\"ms\":$JOB_MS},\"max_attempts\":5,\"timeout_seconds\":900}"
    (( i < 100 )) && body+=','
  done
  echo "${body}]}"
}
BODY=$(batch_body)
t_enq0=$(now)
for b in $(seq 1 $(( JOBS / 100 ))); do
  code=$(curl -s -o /tmp/mass-crash-resp.txt -w '%{http_code}' -X POST "$BASE_URL/v1/jobs/batch" \
    -H "Authorization: Bearer $API_KEY" -H "Content-Type: application/json" \
    -H "Idempotency-Key: mass-crash-$TENANT_ID-$b" -d "$BODY")
  if [[ "$code" != "202" && "$code" != "200" ]]; then
    echo "batch $b failed with HTTP $code: $(head -c 300 /tmp/mass-crash-resp.txt)" >&2; exit 1
  fi
done
t_enq1=$(now)
echo "MASS_CRASH_RESULT enqueue_seconds=$(elapsed "$t_enq0" "$t_enq1")"
wait_until "all $JOBS jobs stored" 60 "SELECT count(*) FROM jobs WHERE tenant_id=$T" ge "$JOBS"

# 2. Wait until the fleet is saturated: running jobs stop growing.
prev=-1; stable=0; running=0
for _ in $(seq 1 120); do
  running=$(count "SELECT count(*) FROM jobs WHERE tenant_id=$T AND status='running'")
  if (( running == prev && running > 0 )); then stable=$((stable+1)); else stable=0; fi
  prev=$running
  (( stable >= 6 )) && break
  sleep 0.5
done
if (( running < 2 )); then echo "no jobs reached 'running' (are workers up and handling test.sleep?)" >&2; exit 1; fi
echo "MASS_CRASH_RESULT running_at_kill=$running"

# 3. KILL -9 every worker container simultaneously.
t_kill=$(now)
$COMPOSE kill -s KILL worker >/dev/null
echo "all workers SIGKILLed at $(date -u +%H:%M:%S)"

# 4. Recovery: the expired leases must each be recovered (attempt closed as lease_expired).
first=""
while true; do
  rec=$(count "SELECT count(*) FROM job_attempts WHERE tenant_id=$T AND outcome='lease_expired'")
  if [[ -z "$first" ]] && (( rec > 0 )); then first=$(elapsed "$t_kill" "$(now)"); fi
  (( rec >= running )) && break
  if (( $(awk -v s="$t_kill" -v n="$(now)" 'BEGIN { print (n - s > 900) ? 1 : 0 }') )); then
    echo "TIMEOUT: only $rec of $running expired leases recovered after 900s" >&2; exit 1
  fi
  sleep 0.5
done
t_all=$(elapsed "$t_kill" "$(now)")
echo "MASS_CRASH_RESULT first_recovery_seconds=${first:-NA} all_recovered_seconds=$t_all lease_seconds=$LEASE_SECONDS"

# 5. Make the remaining work finish quickly (the "fixed deploy"), restart workers, and drain.
sql "UPDATE jobs SET payload='{\"ms\":0}' WHERE tenant_id=$T AND status IN ('queued','running')" >/dev/null
$COMPOSE up -d --scale worker="$WORKERS" worker >/dev/null
# jobs already re-claimed with the long payload finish when their sleep ends; allow for JOB_MS
drain_timeout=$(( JOB_MS / 1000 + 600 ))
wait_until "all $JOBS jobs succeeded" "$drain_timeout" "SELECT count(*) FROM jobs WHERE tenant_id=$T AND status='succeeded'" eq "$JOBS"
t_done=$(elapsed "$t_kill" "$(now)")

# 6. Integrity checks: nothing lost, nothing duplicated, nothing leaked.
dead=$(count "SELECT count(*) FROM jobs WHERE tenant_id=$T AND status='dead'")
busy=$(count "SELECT count(*) FROM tenant_execution_slots WHERE tenant_id=$T AND current_job_id IS NOT NULL")
unfinished=$(count "SELECT count(*) FROM job_attempts WHERE tenant_id=$T AND finished_at IS NULL")
outstanding=$(count "SELECT outstanding_jobs FROM tenant_usage WHERE tenant_id=$T")
not_one=$(count "SELECT count(*) FROM jobs j WHERE j.tenant_id=$T AND j.status='succeeded' AND (SELECT count(*) FROM job_attempts a WHERE a.job_id=j.id AND a.outcome='succeeded') <> 1")
reran=$(count "SELECT count(*) FROM jobs WHERE tenant_id=$T AND attempts_started > 1")
echo "MASS_CRASH_RESULT all_done_seconds=$t_done jobs_rerun=$reran dead=$dead busy_slots=$busy unfinished_attempts=$unfinished outstanding=$outstanding not_exactly_one_success=$not_one"

fail=0
(( dead == 0 )) || { echo "FAIL: $dead jobs went dead" >&2; fail=1; }
(( busy == 0 && unfinished == 0 && outstanding == 0 )) || { echo "FAIL: leak (busy=$busy unfinished=$unfinished outstanding=$outstanding)" >&2; fail=1; }
(( not_one == 0 )) || { echo "FAIL: $not_one succeeded jobs without exactly one succeeded attempt" >&2; fail=1; }
(( reran >= running )) || { echo "FAIL: only $reran jobs were re-run but $running were in flight at the kill" >&2; fail=1; }
if (( fail == 0 )); then echo "MASS_CRASH_RESULT verdict=PASS"; else echo "MASS_CRASH_RESULT verdict=FAIL"; exit 1; fi
