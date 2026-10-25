#!/usr/bin/env bash
# Mutation drill, batch 2: breaks seven MORE guarantees (failure/recovery slot release, stale failure
# fencing, tenant isolation, workers-endpoint authorization, brute-force limiter, last_used_at write
# amplification, audit logging) and checks that the tests you already have fail for each one.
# Same safety rules as batch 1: every pattern must match exactly once, the unmodified tests must PASS
# first (baseline), every file is restored after each mutation and on Ctrl+C. Each caught mutation also
# saves the FULL failing test output next to the summary, so the evidence shows exactly what failed.
#
#   docker run --rm --network qf-race-net -v ${PWD}:/repo -w /repo `
#     -e QF_DATABASE_URL='postgres://postgres:postgres@qf-race-pg:5432/queueforge?sslmode=disable' `
#     golang:1.27.3-bookworm bash scripts/mutation/run-batch2.sh
#
# MUTATION_DRY_RUN=1 only checks that each pattern matches once and shows the exact line it changes.
set -uo pipefail

cd "$(git rev-parse --show-toplevel)" || exit 2
git config --global --add safe.directory "$PWD" >/dev/null 2>&1 || true
FILES=(internal/postgres/job_repo.go internal/postgres/tenant_repo.go internal/httpapi/api.go internal/auth/auth.go)
DRY="${MUTATION_DRY_RUN:-0}"
if [[ "$DRY" != "1" ]]; then : "${QF_DATABASE_URL:?set QF_DATABASE_URL to a disposable PostgreSQL}"; fi

for f in "${FILES[@]}"; do
  git diff --quiet -- "$f" || { echo "Refusing to run: $f has uncommitted changes. Commit or stash them first." >&2; exit 2; }
done
restore() { git checkout -- "${FILES[@]}" 2>/dev/null; }
trap restore EXIT INT TERM

mkdir -p evidence
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
OUT="evidence/mutation2-$STAMP.txt"
log() { echo "$*" | tee -a "$OUT"; }

# mutate <file> <perl-regex> <replacement>   (/s: dot matches newline; \1..\5 in the replacement = groups)
# Succeeds only if the regex matches exactly once.
mutate() {
  local file="$1" re="$2" repl="$3" n
  n=$(RE="$re" perl -0777 -ne 'my $re=$ENV{RE}; my $c=0; $c++ while /$re/gs; print $c;' "$file")
  if [[ "$n" != "1" ]]; then echo "pattern matched $n times in $file (need exactly 1)" >&2; return 3; fi
  RE="$re" REPL="$repl" perl -0777 -i -pe 'my $re=$ENV{RE}; my $r=$ENV{REPL}; s/$re/my @g=($1,$2,$3,$4,$5); my $x=$r; $x=~s{\\([1-5])}{$g[$1-1]}g; $x/se' "$file"
  if git diff --quiet -- "$file"; then echo "mutation changed nothing in $file" >&2; return 4; fi
}

declare -A BASELINE_OK
baseline() { # the tests must PASS on unmodified code, or a "caught" result means nothing
  local key="$1|$2"
  [[ -n "${BASELINE_OK[$key]:-}" ]] && return 0
  if go test -count=1 -run "$2" "$1" >/tmp/mut2-baseline.txt 2>&1; then BASELINE_OK[$key]=1; return 0; fi
  log "  BASELINE FAILS on unmodified code, so this mutation cannot be judged:"; tail -15 /tmp/mut2-baseline.txt | sed 's/^/    /' | tee -a "$OUT"
  return 1
}

CAUGHT=0; SURVIVED=0; INVALID=0; RESULTS=()

# run_mutation <id> <description> <file> <regex> <replacement> <pkg> <test-regex> [attempts]
run_mutation() {
  local id="$1" desc="$2" file="$3" re="$4" repl="$5" pkg="$6" tests="$7" attempts="${8:-1}"
  log ""; log "=== $id: $desc"
  log "  expected to be caught by: $pkg -run '$tests' (up to $attempts run(s))"
  restore
  if [[ "$DRY" != "1" ]] && ! baseline "$pkg" "$tests"; then INVALID=$((INVALID+1)); RESULTS+=("$id INVALID-baseline"); return; fi
  if ! mutate "$file" "$re" "$repl" 2>>"$OUT"; then log "  INVALID: could not apply the mutation (see message above)"; INVALID=$((INVALID+1)); RESULTS+=("$id INVALID"); restore; return; fi
  if [[ "$DRY" == "1" ]]; then
    log "  dry run: pattern matched once and changed $file"
    git diff -U0 -- "$file" | grep -E '^[-+]' | grep -vE '^(---|\+\+\+)' | cut -c1-170 | tee -a "$OUT"
    restore; RESULTS+=("$id DRYRUN-OK"); return
  fi
  local caught=0 i
  for i in $(seq 1 "$attempts"); do
    if go test -count=1 -run "$tests" "$pkg" >/tmp/mut2-run.txt 2>&1; then
      log "  attempt $i: tests PASSED with the bug present"
    elif grep -q '\[build failed\]\|^# ' /tmp/mut2-run.txt && ! grep -q -- '--- FAIL' /tmp/mut2-run.txt; then
      log "  attempt $i: INVALID, the mutated code did not compile"; tail -8 /tmp/mut2-run.txt | sed 's/^/    /' | tee -a "$OUT"; break
    else
      cp /tmp/mut2-run.txt "evidence/mutation2-$STAMP-$id-failure.txt"
      log "  attempt $i: tests FAILED, so the bug was caught. First failure lines (full output: evidence/mutation2-$STAMP-$id-failure.txt):"
      grep -m4 -E -- '--- FAIL|_test.go:[0-9]+' /tmp/mut2-run.txt | cut -c1-220 | sed 's/^/    /' | tee -a "$OUT"
      caught=1; break
    fi
  done
  restore
  if (( caught )); then log "  RESULT: CAUGHT"; CAUGHT=$((CAUGHT+1)); RESULTS+=("$id CAUGHT")
  else log "  RESULT: SURVIVED  <-- the tests do not protect this guarantee"; SURVIVED=$((SURVIVED+1)); RESULTS+=("$id SURVIVED"); fi
}

log "Mutation drill (batch 2) started $(date -u +%FT%TZ) on commit $(git rev-parse --short HEAD)"

run_mutation M7 "failed/recovered attempt never releases its execution slot (shared by retry, dead-letter and lease recovery)" internal/postgres/job_repo.go \
  "(WHERE tenant_id = \\\$1 AND current_job_id = \\\$2 AND lease_token = \\\$3)(\\s+., f\\.tenantID, f\\.jobID, f\\.leaseToken\\); err != nil)" \
  "\\1 AND false\\2" \
  ./tests/regression/ 'TestLifecycle_StaleWorkerAfterLeaseExpiryCannotOverwriteRecoveredJob|TestLifecycle_RecoveryRacingWithLateCompletion'

run_mutation M8 "failure completion accepts a STALE lease token (fencing removed)" internal/postgres/job_repo.go \
  "(SELECT max_attempts, attempts_started, replay_generation\\s+FROM jobs\\s+WHERE id = \\\$1 AND status = 'running' AND )lease_token = \\\$2" \
  "\\1(\$2::uuid IS NOT NULL OR lease_token = \$2)" \
  ./tests/regression/ 'TestLifecycle_StaleWorkerAfterLeaseExpiryCannotOverwriteRecoveredJob'

run_mutation M9 "GetJob ignores the tenant (cross-tenant read)" internal/postgres/job_repo.go \
  "(replay_generation, trace_id\\s+FROM jobs\\s+WHERE id = \\\$1 AND )tenant_id = \\\$2" \
  "\\1(\$2::uuid IS NOT NULL OR tenant_id = \$2)" \
  ./tests/security/ 'TestTenantIsolationFuzz'

run_mutation M10 "/v1/workers returns infrastructure details to tenants without workers:read" internal/httpapi/api.go \
  "(if principal == nil) \\|\\| (!principal\\.HasPermission\\(\"workers:read\"\\) \\{)" \
  "\\1 && \\2" \
  ./tests/security/ 'TestWorkersEndpointDoesNotLeakInfrastructure'

run_mutation M11 "authentication-failure limiter never blocks (brute force allowed)" internal/auth/auth.go \
  "(if blocked, retry := limiter\\.Blocked\\(client\\); blocked) \\{" \
  "\\1 && false {" \
  ./tests/security/ 'TestBruteForceIsThrottledBeforeTouchingTheDatabase'

run_mutation M12 "last_used_at is rewritten on EVERY request (write amplification on one hot row)" internal/postgres/tenant_repo.go \
  "(\\(k\\.last_used_at IS NULL OR k\\.last_used_at < clock_timestamp\\(\\) - interval '1 minute'\\) AS touch_due)(.*?UPDATE api_keys SET last_used_at = clock_timestamp\\(\\)\\s+WHERE id = \\\$1 AND )\\(last_used_at IS NULL OR last_used_at < clock_timestamp\\(\\) - interval '1 minute'\\)" \
  "true AS touch_due\\2true" \
  ./tests/security/ 'TestLastUsedAtIsNotRewrittenOnEveryRequest'

run_mutation M13 "audit middleware removed from the API router (control-plane actions no longer logged)" internal/httpapi/api.go \
  "v1\\.Use\\(a\\.auditMiddleware\\)" \
  "_ = a.auditMiddleware" \
  ./tests/security/ 'TestAuditLogRecordsControlPlaneActionsOnly'

log ""; log "=== SUMMARY ==="
for r in "${RESULTS[@]}"; do log "  $r"; done
log "caught=$CAUGHT survived=$SURVIVED invalid=$INVALID"
log "files restored: $(git diff --quiet -- "${FILES[@]}" && echo yes || echo NO, run git checkout)"
log "evidence written to $OUT"
if [[ "$DRY" == "1" ]]; then exit 0; fi
(( SURVIVED == 0 && INVALID == 0 )) && exit 0 || exit 1
