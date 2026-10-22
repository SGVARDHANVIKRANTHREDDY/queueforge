# QueueForge

A multi-tenant background-job queue for Go and PostgreSQL. Jobs live in your PostgreSQL database; you
enqueue over an HTTP API (or from Go code in your own transaction) and run your task handlers in Go
workers.

> **Status: pre-1.0, experimental.** The core design (leases with fencing, `FOR UPDATE SKIP LOCKED`
> claiming, crash recovery) has an extensive test suite in the repository, but the maintainers'
> latest changes have **not been built or tested yet**: see [CHANGELOG.md](CHANGELOG.md) and
> [docs/PRODUCTION_READINESS_AUDIT.md](docs/PRODUCTION_READINESS_AUDIT.md) if present. Do not run
> business-critical workloads on it. Delivery is **at-least-once**: handlers must be idempotent.

## Should you use it?

| You want | Use |
|---|---|
| A Go library with transactional insert, batch insert, typed workers and a large community | [River](https://github.com/riverqueue/river) |
| Python tasks, a huge ecosystem and years of production use | [Celery](https://docs.celeryq.dev) |
| A Redis-backed Go queue with very high throughput | Asynq |
| **Per-tenant quotas, rate limits and concurrency limits, an HTTP API with idempotency keys, and an audit log, with Go handlers on PostgreSQL** | QueueForge (this project) |

What it does **not** do: run Python or other-language tasks (handlers are Go only), exactly-once
execution (nobody can promise that for side effects), multi-region or automatic database failover.
Measured numbers in [docs/benchmark-reports/report.md](docs/benchmark-reports/report.md) come from one shared
vCPU and are not a scale claim.

## Quick start (about 10 minutes)

Prerequisites: Docker, Go (version in `go.mod`).

```bash
# 1. PostgreSQL + API + the bundled sample worker (development passwords: never use in production)
docker compose -f deploy/docker-compose.yml up -d --build

# 2. Create a tenant and an API key (the secret is shown once)
docker compose -f deploy/docker-compose.yml exec api /app/admin create-tenant -name demo -rate 50 -burst 100 -pending 1000 -concurrent 4
docker compose -f deploy/docker-compose.yml exec api /app/admin create-api-key -tenant-id <TENANT_UUID> -name demo-key

# 3. Enqueue a job (the sample worker has an "echo" task)
curl -s -X POST localhost:8080/v1/jobs -H "Authorization: Bearer qf_live_..." \
  -H "Idempotency-Key: demo-1" -H "Content-Type: application/json" \
  -d '{"task_type":"echo","payload":{"hello":"world"}}'

# 4. Watch it finish
curl -s localhost:8080/v1/jobs/<JOB_ID> -H "Authorization: Bearer qf_live_..."
```
Dashboard: <http://localhost:8080/ui/> (paste your key).

## Write your own tasks (Go)

Handlers run in a Go process you own. Import `workerkit` from your module:

```go
package main

import (
	"context"
	"encoding/json"
	"log"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/workerkit"
)

func main() {
	err := workerkit.Run(map[string]workerkit.Handler{
		"email.send": func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
			// Do the work. Respect ctx.Done(). Be idempotent: this can run more than once.
			// Return workerkit.Permanent(err) for failures that must not be retried.
			return json.RawMessage(`{"sent":true}`), nil
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
```
Configuration is via `QF_*` environment variables (see [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md)). A handler
that panics is recovered and retried like any other failure.

> The Go module path is currently `queueforge`, which `go get` cannot fetch. Run
> `./scripts/set-module-path.sh github.com/YOUR_USER/queueforge` once after you host the repository.

## Enqueue from your application

**HTTP / curl**: see `api/openapi.yaml`. Always send an `Idempotency-Key`; a retry with the same key and
body returns the original job (HTTP 200, `Idempotent-Replay: true`), a different body is a 409.

**Go**
```go
c := queueforge.New("https://queue.example.com", os.Getenv("QUEUEFORGE_KEY"))
job, err := c.Enqueue(ctx, queueforge.EnqueueRequest{TaskType: "email.send", Payload: payload})
jobs, err := c.EnqueueBatch(ctx, []queueforge.EnqueueRequest{...}, "") // atomic, up to 100
```

**Python** (enqueue and inspect only; tasks still run in Go workers)
```python
from queueforge import QueueForgeClient
client = QueueForgeClient(base_url="https://queue.example.com", api_key="qf_live_...")
job = client.submit_job("email.send", {"to": "a@b.c"})          # idempotency key is automatic
client.submit_batch([{"task_type": "email.send", "payload": {}}])  # atomic, up to 100
result = client.poll_until_complete(job["id"])
```

**In your own database transaction (Go, same PostgreSQL)**: the job commits or rolls back with your data.
```go
enq := outbox.New(pool)
tx, _ := pool.Begin(ctx)
defer tx.Rollback(ctx)
// ... your INSERT/UPDATE ...
_, err := enq.EnqueueTx(ctx, tx, outbox.Params{TenantID: tenantID, TaskType: "email.send", Payload: p})
if err != nil { return err }
return tx.Commit(ctx)
```

## Guarantees and limits (be precise)

- **At-least-once** execution. A job can run again after a crash or lost lease. Database writes from a
  worker that lost its lease are rejected (fencing), but **external side effects are not undone**.
- Submission is deduplicated by idempotency key plus a request hash.
- Every accepted job is stored by a PostgreSQL commit; durability is exactly your PostgreSQL's durability.
- Per tenant: enqueue rate limit, pending-job quota, concurrent-execution slots; queries are tenant-scoped.
- Single PostgreSQL primary: no built-in failover. Back up and run a restore drill (`deploy/restore-drill.sh`).
- Handlers must tolerate cancellation (`ctx.Done()`) and must not put secrets in error messages.

## Operating it

Metrics, alerts and runbooks: `deploy/alerts.yml`, `docs/runbooks/`. Production on one VPS:
[docs/DEPLOYMENT.md](docs/DEPLOYMENT.md). Kubernetes (unverified): `deploy/k8s/`. Threat model:
[docs/THREAT_MODEL.md](docs/THREAT_MODEL.md). Design decisions: `docs/ADRs/` (ADR-001 to ADR-016).

## Run the tests

```bash
export QF_DATABASE_URL='postgres://postgres:postgres@localhost:55432/queueforge?sslmode=disable'
go vet ./... && go test -race ./...                              # needs a reachable PostgreSQL 16
(cd sdk/python && python -m unittest discover -s tests -v)       # no database needed
```

## Layout

`cmd/{api,worker,admin}` binaries; `workerkit/` public worker entry point; `outbox/` public transactional
enqueue; `sdk/{go,python}` clients; `internal/` implementation; `migrations/` SQL; `api/openapi.yaml`;
`deploy/` Compose, Kubernetes, alerts; `tests/` integration, property, chaos, security, load; `bench/` comparisons.

## License

Add a LICENSE file before publishing; none was present when this README was written.
