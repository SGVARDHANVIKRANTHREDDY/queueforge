# Kubernetes deployment (unverified)

`queueforge.yaml` has API and worker Deployments with probes, resource limits, non-root and
read-only-rootfs security contexts, PodDisruptionBudgets, an HPA and a default-deny NetworkPolicy.

It has **not been applied to a cluster**. Before trusting it:
1. `kubectl apply --dry-run=server -f deploy/k8s/queueforge.yaml`
2. Add an Ingress (or Gateway) and a NetworkPolicy that allows ingress-controller -> `queueforge-api:8080`
   and Prometheus -> `:9090`; the default-deny policy above blocks everything else.
3. Scale workers by backlog, not CPU: alert on `queueforge_oldest_runnable_job_age_seconds` (see
   `deploy/alerts.yml`) and add replicas (or use KEDA with a Postgres scaler).
4. Workers need `terminationGracePeriodSeconds` greater than `QF_SHUTDOWN_TIMEOUT`.
5. The API runs migrations at startup. With several replicas starting at once goose serialises them
   through its version table, but for large schema changes run `/app/admin migrate` as a one-off Job
   first.
