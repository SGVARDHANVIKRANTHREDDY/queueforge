#!/usr/bin/env bash
# Post-deploy smoke test. Exits non-zero on the first failed check.
#   BASE_URL=https://queue.example.com API_KEY=qf_live_... ./deploy/smoke.sh
# Optional: WORKER_METRICS_URL=http://127.0.0.1:9091  (checks worker-side metrics exist)
set -u
BASE_URL="${BASE_URL:?set BASE_URL}"; API_KEY="${API_KEY:?set API_KEY}"
pass=0; fail=0
ok()   { echo "  ok   $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL $1"; fail=$((fail+1)); }
check(){ if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (got '$2', want '$3')"; fi; }
code() { curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$@"; }
auth=(-H "Authorization: Bearer ${API_KEY}")

echo "health"
check "GET /healthz"                   "$(code "$BASE_URL/healthz")" 200
check "GET /readyz (DB reachable)"     "$(code "$BASE_URL/readyz")" 200

echo "security"
check "no API key -> 401"              "$(code "$BASE_URL/v1/jobs")" 401
check "bad API key -> 401"             "$(code -H 'Authorization: Bearer qf_live_invalid' "$BASE_URL/v1/jobs")" 401
m=$(code "$BASE_URL/metrics")
if [ "$m" = 200 ] && [ "${EXPECT_PUBLIC_METRICS:-no}" != yes ]; then
  bad "/metrics is publicly readable (block it at the proxy)"
else ok "/metrics not public (HTTP $m)"; fi

echo "enqueue + idempotency"
key="smoke-$(date +%s)-$RANDOM"
body='{"task_type":"echo","queue":"default","payload":{"smoke":true}}'
r1=$(curl -s -D /tmp/smoke_h1 --max-time 10 "${auth[@]}" -H "Idempotency-Key: $key" -H 'Content-Type: application/json' -d "$body" "$BASE_URL/v1/jobs")
id=$(echo "$r1" | sed -n 's/.*"id" *: *"\([^"]*\)".*/\1/p' | head -1)
[ -n "$id" ] && ok "job accepted ($id)" || { bad "enqueue failed: $r1"; exit 1; }
r2code=$(code "${auth[@]}" -H "Idempotency-Key: $key" -H 'Content-Type: application/json' -d "$body" "$BASE_URL/v1/jobs")
check "same key + same body -> replay (200)" "$r2code" 200
r3code=$(code "${auth[@]}" -H "Idempotency-Key: $key" -H 'Content-Type: application/json' -d '{"task_type":"echo","queue":"default","payload":{"different":1}}' "$BASE_URL/v1/jobs")
check "same key + different body -> 409"     "$r3code" 409

echo "worker executes the job"
status=""
for _ in $(seq 1 40); do
  status=$(curl -s --max-time 10 "${auth[@]}" "$BASE_URL/v1/jobs/$id" | sed -n 's/.*"status" *: *"\([^"]*\)".*/\1/p' | head -1)
  [ "$status" = succeeded ] && break; sleep 0.5
done
check "job reaches 'succeeded' within 20s (is a worker running?)" "$status" succeeded

echo "api surface"
check "GET /v1/dead-letters"           "$(code "${auth[@]}" "$BASE_URL/v1/dead-letters")" 200
check "GET /v1/workers"                "$(code "${auth[@]}" "$BASE_URL/v1/workers")" 200

if [ -n "${API_METRICS_URL:-}" ]; then
  echo "api ops endpoint"
  am=$(curl -s --max-time 10 "$API_METRICS_URL/metrics")
  for m in queueforge_jobs_runnable queueforge_oldest_runnable_job_age_seconds queueforge_workers_alive queueforge_db_pool_connections; do
    echo "$am" | grep -q "^$m" && ok "api exposes $m" || bad "api metrics missing $m"
  done
fi

if [ -n "${WORKER_METRICS_URL:-}" ]; then
  echo "worker metrics"
  mt=$(curl -s --max-time 10 "$WORKER_METRICS_URL/metrics")
  echo "$mt" | grep -q 'queueforge_jobs_claimed_total' && ok "worker exposes queueforge_jobs_claimed_total" || bad "worker metrics missing claimed counter"
  check "worker /readyz" "$(code "$WORKER_METRICS_URL/readyz")" 200
fi

echo; echo "passed=$pass failed=$fail"; [ "$fail" -eq 0 ]
