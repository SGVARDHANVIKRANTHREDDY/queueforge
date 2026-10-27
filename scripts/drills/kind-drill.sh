#!/usr/bin/env bash
# Kubernetes drill on a disposable 3-node kind cluster (1 control plane + 2 workers), using the REAL manifest
# deploy/k8s/queueforge.yaml with only three local substitutions (image, database URL, nothing else).
#
# Proves: the pods start and become Ready under the manifest's own security context and probes; a job flows
# end to end through the Service; killing an API pod and hard-killing a worker pod does not lose work; and
# draining a node (with the PodDisruptionBudgets in force) succeeds while traffic keeps flowing.
# When something fails it prints the diagnostics a reviewer needs (pod state, last container exit reason,
# previous logs, events), because "CrashLoopBackOff" alone explains nothing.
#
# Needs: docker, kind, kubectl, and the image queueforge:local (built if missing). Git Bash / WSL / Linux:
#     bash scripts/drills/kind-drill.sh
# Env: IMG (default queueforge:local), KEEP=1 keeps the cluster, PHASE_SECONDS (default 12).
# Takes about 5 to 10 minutes. Postgres runs in the cluster WITHOUT persistence: this is a throwaway drill.
set -euo pipefail
export MSYS_NO_PATHCONV=1 MSYS2_ARG_CONV_EXCL='*'
cd "$(git rev-parse --show-toplevel)"

CL=qf-drill; NS=queueforge; IMG="${IMG:-queueforge:local}"; PHASE_SECONDS="${PHASE_SECONDS:-12}"
mkdir -p evidence
OUT="evidence/kind-drill-$(date -u +%Y%m%dT%H%M%SZ).txt"
exec > >(tee -a "$OUT") 2>&1
for t in docker kind kubectl; do command -v "$t" >/dev/null 2>&1 || { echo "missing tool: $t" >&2; exit 2; }; done

now() { date +%s.%N; }
secs() { awk -v a="$1" -v b="$2" 'BEGIN { printf "%.1f", b - a }'; }
sql() { kubectl -n "$NS" exec deploy/queueforge-postgres -- psql -U postgres -d queueforge -Atc "$1"; }
succeeded() { sql "SELECT count(*) FROM jobs WHERE status='succeeded'" | tr -d '[:space:]'; }
ready() { kubectl -n "$NS" get deploy "$1" -o jsonpath='{.status.readyReplicas}' 2>/dev/null || true; }
cleanup() { if [[ "${KEEP:-0}" != "1" ]]; then kind delete cluster --name "$CL" >/dev/null 2>&1 || true; fi; }
trap cleanup EXIT

diagnose() {
  echo "================ DIAGNOSTICS ================"
  kubectl -n "$NS" get pods -o wide 2>&1 || true
  for p in $(kubectl -n "$NS" get pods -o name 2>/dev/null | grep -E 'queueforge-(api|worker)' || true); do
    echo "---- $p: container state and events"
    kubectl -n "$NS" describe "$p" 2>&1 | sed -n '/Last State/,/Ready:/p;/^Events:/,$p' | tail -45
    echo "---- $p: previous container log (tail)"
    kubectl -n "$NS" logs "$p" --previous --tail=30 2>&1 || kubectl -n "$NS" logs "$p" --tail=30 2>&1 || true
  done
}
fail() { echo "KIND_DRILL_RESULT verdict=FAIL reason=$*"; diagnose; exit 1; }
wait_ready_replicas() { # <deployment> <want> <timeout-seconds>
  local end=$(( $(date +%s) + $3 )); while true; do [[ "$(ready "$1")" == "$2" ]] && return 0; (( $(date +%s) > end )) && return 1; sleep 2; done
}
progress_during() {
  local a b; a=$(succeeded); sleep "$PHASE_SECONDS"; b=$(succeeded)
  echo "  [$1] jobs succeeded: $a -> $b during $PHASE_SECONDS s"
  (( b > a )) || fail "no job completed during '$1' (system stalled)"
}

echo "== kind drill $(date -u +%FT%TZ) on commit $(git rev-parse --short HEAD), image $IMG =="
docker image inspect "$IMG" >/dev/null 2>&1 || docker build -f deploy/Dockerfile -t "$IMG" .
kind delete cluster --name "$CL" >/dev/null 2>&1 || true

echo "-- 1. create a 3-node cluster and load the image"
t=$(now)
kind create cluster --name "$CL" --wait 180s --config=- <<'YAML'
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
  - role: worker
  - role: worker
YAML
kind load docker-image "$IMG" --name "$CL"
kubectl label node "$CL-worker" role=db --overwrite >/dev/null
echo "KIND_DRILL_RESULT cluster_ready_seconds=$(secs "$t" "$(now)") nodes=$(kubectl get nodes --no-headers | wc -l | tr -d ' ')"

echo "-- 2. PostgreSQL for the drill (pinned to node $CL-worker so draining the other node never touches it)"
kubectl apply -f - <<'YAML'
apiVersion: v1
kind: Namespace
metadata: { name: queueforge }
---
apiVersion: apps/v1
kind: Deployment
metadata: { name: queueforge-postgres, namespace: queueforge }
spec:
  replicas: 1
  selector: { matchLabels: { app: queueforge-postgres } }
  template:
    metadata: { labels: { app: queueforge-postgres } }
    spec:
      nodeSelector: { role: db }
      containers:
        - name: postgres
          image: postgres:16-alpine
          env:
            - { name: POSTGRES_PASSWORD, value: postgres }
            - { name: POSTGRES_DB, value: queueforge }
          ports: [{ containerPort: 5432 }]
          readinessProbe: { exec: { command: ["pg_isready", "-U", "postgres", "-d", "queueforge"] }, periodSeconds: 3 }
---
apiVersion: v1
kind: Service
metadata: { name: queueforge-postgres, namespace: queueforge }
spec:
  selector: { app: queueforge-postgres }
  ports: [{ port: 5432, targetPort: 5432 }]
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: { name: allow-postgres-in-namespace, namespace: queueforge }
spec:
  podSelector: { matchLabels: { app: queueforge-postgres } }
  policyTypes: ["Ingress"]
  ingress:
    - from: [{ podSelector: {} }]
      ports: [{ protocol: TCP, port: 5432 }]
YAML
kubectl -n "$NS" rollout status deploy/queueforge-postgres --timeout=240s || fail "postgres did not become ready"

echo "-- 3. apply the REAL manifest (only the image and the database URL are substituted)"
sed -e 's#^\( *image:\).*#\1 '"$IMG"'#' \
    -e 's#^\( *QF_DATABASE_URL:\).*#\1 "postgres://postgres:postgres@queueforge-postgres:5432/queueforge?sslmode=disable"#' \
    deploy/k8s/queueforge.yaml | kubectl apply -f -
t=$(now)
kubectl -n "$NS" rollout status deploy/queueforge-api --timeout=300s || fail "API deployment did not become ready"
kubectl -n "$NS" rollout status deploy/queueforge-worker --timeout=300s || fail "worker deployment did not become ready"
echo "KIND_DRILL_RESULT rollout_to_ready_seconds=$(secs "$t" "$(now)") api_ready=$(ready queueforge-api) worker_ready=$(ready queueforge-worker)"

echo "-- 4. end to end: tenant, key, traffic through the Service from inside the cluster"
admin() { kubectl -n "$NS" exec deploy/queueforge-api -- /app/admin "$@"; }
TID=$(admin create-tenant -name k8s-demo -rate 100000 -burst 100000 -pending 1000000 -concurrent 16 | grep -Eo '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}' | sed -n 1p)
KEY=$(admin create-api-key -tenant-id "$TID" -name k8s-drill | grep -Eo 'qf_live_[A-Za-z0-9_-]+' | awk '{ if (length($0) > length(m)) m = $0 } END { print m }')
[[ -n "$TID" && -n "$KEY" ]] || fail "could not create tenant/key"
JOB='{"task_type":"test.sleep","payload":{"ms":200}}'
BODY='{"jobs":['; for i in $(seq 1 10); do BODY+="$JOB"; (( i < 10 )) && BODY+=','; done; BODY+=']}'
kubectl -n "$NS" run qf-load --restart=Never --image=curlimages/curl:8.10.1 --env="KEY=$KEY" --env="BODY=$BODY" --env="API_HOST=queueforge-api" --overrides='{"spec":{"nodeSelector":{"role":"db"}}}' --command -- sh -c '
i=0
while [ ! -f /tmp/stop ]; do
  i=$((i+1)); tries=0
  while true; do
    code=$(curl -s -o /dev/null -m 5 -w "%{http_code}" -X POST "http://$API_HOST/v1/jobs/batch" \
      -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" -H "Idempotency-Key: k8s-$i" -d "$BODY")
    case "$code" in 202|200) break ;; esac
    tries=$((tries+1)); sleep 0.2
  done
  echo "SENT $i tries=$tries"
  sleep 0.1
done
echo "DONE $i"
' >/dev/null
kubectl -n "$NS" wait --for=condition=Ready pod/qf-load --timeout=120s >/dev/null || fail "load pod did not start"
progress_during "baseline through the Service"

echo "-- 5. kill one API pod"
victim=$(kubectl -n "$NS" get pods -l app=queueforge-api -o name | sed -n 1p)
t=$(now); kubectl -n "$NS" delete "$victim" --wait=false >/dev/null
wait_ready_replicas queueforge-api 2 180 || fail "API did not return to 2 ready replicas after a pod kill"
echo "KIND_DRILL_RESULT api_pod_kill_recovery_seconds=$(secs "$t" "$(now)")"
progress_during "after API pod kill"

echo "-- 6. HARD-kill the worker container (SIGKILL at the container runtime, no grace)"
victim=$(kubectl -n "$NS" get pods -l app=queueforge-worker -o jsonpath='{.items[0].metadata.name}')
node=$(kubectl -n "$NS" get pod "$victim" -o jsonpath='{.spec.nodeName}')
cid=$(docker exec "$node" crictl ps --label io.kubernetes.pod.name="$victim" --name worker -q | sed -n 1p)
[[ -n "$cid" ]] || fail "could not find the worker container on $node"
before=$(kubectl -n "$NS" get pod "$victim" -o jsonpath='{.status.containerStatuses[0].restartCount}')
t=$(now); docker exec "$node" crictl stop --timeout 0 "$cid" >/dev/null
for _ in $(seq 1 60); do
  current=$(kubectl -n "$NS" get pod "$victim" -o jsonpath='{.status.containerStatuses[0].restartCount}' 2>/dev/null || echo 0)
  [[ "$current" -gt "$before" ]] && break
  sleep 1
done
wait_ready_replicas queueforge-worker 2 180 || fail "workers did not return to 2 ready replicas after the container kill"
echo "KIND_DRILL_RESULT worker_container_kill_recovery_seconds=$(secs "$t" "$(now)")"
progress_during "after worker container kill"

echo "-- 7. drain a node while traffic flows (PodDisruptionBudgets must hold, drain must succeed)"
t=$(now)
kubectl drain "$CL-worker2" --ignore-daemonsets --delete-emptydir-data --timeout=300s || fail "node drain failed or was blocked"
echo "KIND_DRILL_RESULT node_drain_seconds=$(secs "$t" "$(now)")"
wait_ready_replicas queueforge-api 2 180 || fail "API not back to 2 ready replicas after the drain"
wait_ready_replicas queueforge-worker 2 180 || fail "workers not back to 2 ready replicas after the drain"
progress_during "after node drain"
kubectl uncordon "$CL-worker2" >/dev/null

echo "-- 8. stop traffic and verify nothing was lost, duplicated or stranded"
kubectl -n "$NS" exec qf-load -- touch /tmp/stop
for _ in $(seq 1 90); do [[ "$(kubectl -n "$NS" get pod qf-load -o jsonpath='{.status.phase}')" == "Succeeded" ]] && break; sleep 1; done
LOG=$(kubectl -n "$NS" logs qf-load)
SENT=$(echo "$LOG" | awk '/^DONE/ { print $2 }'); [[ -n "$SENT" ]] || fail "traffic generator did not finish"
RETRIED=$(echo "$LOG" | awk '/^SENT/ { split($3, a, "="); if (a[2] > 0) n++ } END { print n+0 }')
EXPECT=$(( SENT * 10 )); echo "KIND_DRILL_RESULT batches_sent=$SENT jobs_expected=$EXPECT batches_that_needed_retries=$RETRIED"
for _ in $(seq 1 240); do [[ "$(succeeded)" == "$EXPECT" ]] && break; sleep 1; done
total=$(sql "SELECT count(*) FROM jobs" | tr -d '[:space:]'); ok=$(succeeded)
dead=$(sql "SELECT count(*) FROM jobs WHERE status='dead'" | tr -d '[:space:]')
busy=$(sql "SELECT count(*) FROM tenant_execution_slots WHERE current_job_id IS NOT NULL" | tr -d '[:space:]')
unfinished=$(sql "SELECT count(*) FROM job_attempts WHERE finished_at IS NULL" | tr -d '[:space:]')
outstanding=$(sql "SELECT outstanding_jobs FROM tenant_usage WHERE tenant_id='$TID'" | tr -d '[:space:]')
not_one=$(sql "SELECT count(*) FROM jobs j WHERE j.status='succeeded' AND (SELECT count(*) FROM job_attempts a WHERE a.job_id=j.id AND a.outcome='succeeded') <> 1" | tr -d '[:space:]')
rerun=$(sql "SELECT count(*) FROM jobs WHERE attempts_started > 1" | tr -d '[:space:]')
echo "KIND_DRILL_RESULT jobs_in_db=$total succeeded=$ok dead=$dead busy_slots=$busy unfinished_attempts=$unfinished outstanding=$outstanding not_exactly_one_success=$not_one jobs_rerun_after_disruptions=$rerun"
[[ "$total" == "$EXPECT" ]] || fail "job count mismatch: submitted $EXPECT, database has $total"
[[ "$ok" == "$EXPECT" && "$dead" == "0" && "$busy" == "0" && "$unfinished" == "0" && "$outstanding" == "0" && "$not_one" == "0" ]] || fail "integrity check failed"
[[ "$rerun" -ge 1 ]] || fail "the worker container kill left no re-run jobs: the kill was not hard"
echo "KIND_DRILL_RESULT verdict=PASS"
