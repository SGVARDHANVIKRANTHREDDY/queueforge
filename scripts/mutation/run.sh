#!/usr/bin/env bash
# Mutation drill: deliberately break six guarantees in the SQL, and check that the tests you already
# have FAIL for each one. A test suite that stays green when a guarantee is broken is not protecting it.
#
# Each mutation is a text edit that must match EXACTLY ONE place in the file (otherwise the script stops
# and tells you, rather than editing the wrong thing). After every mutation the file is restored with
# `git checkout`, and a trap restores it again if you press Ctrl+C.
#
# Needs: bash, git, perl, go, and a reachable disposable PostgreSQL. Run it in the same Linux container
# you use for the race tests, for example (PowerShell, from the repo root):
#
#   docker run --rm --network qf-race-net -v ${PWD}:/repo -w /repo `
#     -e QF_DATABASE_URL='postgres://postgres:postgres@qf-race-pg:5432/queueforge?sslmode=disable' `
#     golang:1.27.3-bookworm bash scripts/mutation/run.sh
#
# Output goes to the screen and to evidence/mutation-<timestamp>.txt. Exit code 0 only if EVERY mutation
# was caught. Set MUTATION_DRY_RUN=1 to only check that each pattern matches exactly once (no tests run).
set -uo pipefail

cd "$(git rev-parse --show-toplevel)" || exit 2
git config --global --add safe.directory "$PWD" >/dev/null 2>&1 || true
REPO_SQL=internal/postgres/job_repo.go
CLAIM_SQL=internal/postgres/claim.go
DRY="${MUTATION_DRY_RUN:-0}"
if [[ "$DRY" != "1" ]]; then : "${QF_DATABASE_URL:?set QF_DATABASE_URL to a disposable PostgreSQL}"; fi

for f in "$REPO_SQL" "$CLAIM_SQL"; do
  git diff --quiet -- "$f" || { echo "Refusing to run: $f has uncommitted changes. Commit or stash them first." >&2; exit 2; }
done
restore() { git checkout -- "$REPO_SQL" "$CLAIM_SQL" 2>/dev/null; }
trap restore EXIT INT TERM

mkdir -p evidence
OUT="evidence/mutation-$(date -u +%Y%m%dT%H%M%SZ).txt"
log() { echo "$*" | tee -a "$OUT"; }

# mutate <file> <perl-regex> <replacement>
#   The regex is applied with /s (dot matches newline). Use \1 in the replacement for group 1.
#   Succeeds only if the regex matches exactly once.
mutate() {
  local file="$1" re="$2" repl="$3" n
  n=$(RE="$re" perl -0777 -ne 'my $re=$ENV{RE}; my $c=()=/$re/gs; print $c;' "$file")
  if [[ "$n" != "1" ]]; then echo "pattern matched $n times in $file (need exactly 1)" >&2; return 3; fi
  RE="$re" REPL="$repl" perl -0777 -i -pe 'my $re=$ENV{RE}; my $r=$ENV{REPL}; s/$re/my $g1=$1; my $x=$r; $x=~s{\\1}{$g1}g; $x/se' "$file"
  if git diff --quiet -- "$file"; then echo "mutation changed nothing in $file" >&2; return 4; fi
}

declare -A BASELINE_OK
baseline() { # <pkg> <run-regex>: the tests must PASS on unmodified code, or a "caught" result means nothing
  local key="$1|$2"
  [[ -n "${BASELINE_OK[$key]:-}" ]] && return 0
  if go test -count=1 -run "$2" "$1" >/tmp/mut-baseline.txt 2>&1; then BASELINE_OK[$key]=1; return 0; fi
  log "  BASELINE FAILS on unmodified code, so this mutation cannot be judged:"; tail -15 /tmp/mut-baseline.txt | sed 's/^/    /' | tee -a "$OUT"
  return 1
}

CAUGHT=0; SURVIVED=0; INVALID=0; RESULTS=()

# run_mutation <id> <description> <file> <regex> <replacement> <pkg> <test-regex> [attempts]
run_mutation() {
  local id="$1" desc="$2" file="$3" re="$4" repl="$5" pkg="$6" tests="$7" attempts="${8:-1}"
  log ""; log "=== $id: $desc"
  log "  expected to be caught by: $pkg -run '$tests' (up to $attempts run(s))"
  restore
  # The baseline MUST run on the unmodified code, so it goes BEFORE the mutation is applied.
  # (An earlier version ran it after, which made every healthy test look broken.)
  if [[ "$DRY" != "1" ]] && ! baseline "$pkg" "$tests"; then INVALID=$((INVALID+1)); RESULTS+=("$id INVALID-baseline"); return; fi
  if ! mutate "$file" "$re" "$repl" 2>>"$OUT"; then log "  INVALID: could not apply the mutation (see message above)"; INVALID=$((INVALID+1)); RESULTS+=("$id INVALID"); restore; return; fi
  if [[ "$DRY" == "1" ]]; then log "  dry run: pattern matched once and changed the file"; git diff -U0 -- "$file" | grep -E '^[-+][^-+]' | cut -c1-170 | tee -a "$OUT"; restore; RESULTS+=("$id DRYRUN-OK"); return; fi
  local caught=0 i
  for i in $(seq 1 "$attempts"); do
    if go test -count=1 -run "$tests" "$pkg" >/tmp/mut-run.txt 2>&1; then
      log "  attempt $i: tests PASSED with the bug present"
    elif grep -q '\[build failed\]\|^# ' /tmp/mut-run.txt && ! grep -q -- '--- FAIL' /tmp/mut-run.txt; then
      log "  attempt $i: INVALID, the mutated code did not compile"; tail -8 /tmp/mut-run.txt | sed 's/^/    /' | tee -a "$OUT"; break
    else
      log "  attempt $i: tests FAILED, so the bug was caught. Full failing output:"
      sed 's/^/    /' /tmp/mut-run.txt | tee -a "$OUT"
      caught=1; break
    fi
  done
  restore
  if (( caught )); then log "  RESULT: CAUGHT"; CAUGHT=$((CAUGHT+1)); RESULTS+=("$id CAUGHT")
  else log "  RESULT: SURVIVED  <-- the tests do not protect this guarantee"; SURVIVED=$((SURVIVED+1)); RESULTS+=("$id SURVIVED"); fi
}

log "Mutation drill started $(date -u +%FT%TZ) on commit $(git rev-parse --short HEAD)"

run_mutation M1 "success completion accepts a STALE lease token (fencing removed)" "$REPO_SQL" \
  "(result = \\\$3, completed_at = clock_timestamp\\(\\),.*?WHERE id = \\\$1 AND status = 'running' AND )lease_token = \\\$2" \
  "\\1(\$2::uuid IS NOT NULL OR lease_token = \$2)" \
  ./tests/regression/ 'TestLifecycle_StaleWorkerAfterLeaseExpiryCannotOverwriteRecoveredJob'

run_mutation M2 "success completion forgets to release the execution slot" "$REPO_SQL" \
  "(\\), slot AS \\(\\s+UPDATE tenant_execution_slots\\s+SET current_job_id = NULL, lease_token = NULL, updated_at = clock_timestamp\\(\\)\\s+WHERE tenant_id = \\\$6 AND current_job_id = \\\$1 AND lease_token = \\\$2\\s+AND EXISTS \\(SELECT 1 FROM done\\))" \
  "\\1 AND false" \
  ./tests/regression/ 'TestLifecycle_RenewalBeatsRecovery'

run_mutation M3 "success completion forgets to decrement the tenant's outstanding-jobs counter" "$REPO_SQL" \
  "(outstanding_jobs = GREATEST\\(0, outstanding_jobs - 1\\), updated_at = clock_timestamp\\(\\)\\s+WHERE tenant_id = \\\$6 AND EXISTS \\(SELECT 1 FROM done\\))" \
  "\\1 AND false" \
  ./tests/regression/ 'TestLifecycle_RenewalBeatsRecovery'

run_mutation M4 "success completion leaves the attempt row unfinished" "$REPO_SQL" \
  "(AND a\\.attempt_number = done\\.attempts_started AND a\\.lease_token = \\\$2)" \
  "\\1 AND false" \
  ./tests/regression/ 'TestLifecycle_RenewalBeatsRecovery'

run_mutation M5 "cancelled completion accepts a STALE lease token (fencing removed)" "$REPO_SQL" \
  "(SET status = 'cancelled',.*?WHERE id = \\\$1 AND status = 'running' AND )lease_token = \\\$2" \
  "\\1(\$2::uuid IS NOT NULL OR lease_token = \$2)" \
  ./tests/regression/ 'TestLifecycle_StaleWorkerAfterLeaseExpiryCannotOverwriteRecoveredJob'

run_mutation M6 "claim loses its 'still queued?' re-check after taking the row lock (double claims)" "$CLAIM_SQL" \
  "(WHERE j\\.id IN \\(SELECT id FROM cand\\)) AND j\\.status = 'queued'" \
  "\\1" \
  ./tests/regression/ 'TestClaim_ConcurrentWorkersNeverDoubleClaim' 3

log ""; log "=== SUMMARY ==="
for r in "${RESULTS[@]}"; do log "  $r"; done
log "caught=$CAUGHT survived=$SURVIVED invalid=$INVALID"
log "files restored: $(git diff --quiet -- "$REPO_SQL" "$CLAIM_SQL" && echo yes || echo NO, run git checkout)"
log "evidence written to $OUT"
if [[ "$DRY" == "1" ]]; then exit 0; fi
(( SURVIVED == 0 && INVALID == 0 )) && exit 0 || exit 1
