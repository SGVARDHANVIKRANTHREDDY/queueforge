# QueueForge: Manual Verification and Launch Checklist

*Everything to do yourself on your laptop, in order. Commands assume macOS, Linux or Windows WSL2 with a bash shell. Tick each box. When something fails, copy the exact output and send it to me.*

Why this exists: v8 was written without a compiler or database. Nothing in the Go code has been built or tested. This checklist turns "claims" into evidence. Do the phases in order; later phases depend on earlier ones.

**Rough time:** Phases 0-5 about 2-3 hours. Phases 6-10 another half day. Phase 11-12 a weekend plus a few weeks of real use.

## Phase 0. Install the tools

- [ ] **Git**: `git --version`
- [ ] **Go**: open `go.mod` after unzipping (Phase 1) and look at the `go` line (it says 1.27.0). Install that version or newer from https://go.dev/dl/ and confirm with `go version`. If that exact version does not exist for you, tell me before changing `go.mod`.
- [ ] **Docker Desktop** (or Docker Engine): `docker --version` and `docker run --rm hello-world`
- [ ] **Python 3.10+**: `python3 --version`
- [ ] Optional but useful: `psql` (PostgreSQL client), `curl`, `jq`
- [ ] Windows users: install WSL2 (Ubuntu) and run everything inside it, with the repo stored in the Linux filesystem (not under `/mnt/c`), or tests will be very slow.

## Phase 1. Get the code and take a baseline

- [ ] Download `Backend_Project_v8.zip` from our chat and unzip it. The folder is named `Backend Project` (with a space). Rename it: `mv "Backend Project" queueforge && cd queueforge`
- [ ] Make it a git repo so you can see every change you make: `git init && git add -A && git commit -m "baseline: v8 as received"`
- [ ] Open the project in an editor (VS Code with the Go extension is fine).

## Phase 2. Make it legally usable

- [ ] **Add a LICENSE file.** Without one nobody may legally use it. Pick one yourself (MIT and Apache-2.0 are common); copy the text from https://choosealicense.com and save it as `LICENSE`. Put your real name and the year in it.
- [ ] Commit: `git add LICENSE && git commit -m "add license"`

## Phase 3. Start a disposable PostgreSQL for tests

Use a throwaway container. Never point tests at a database you care about.

- [ ] Start it:

```bash
docker run -d --name qf-pg -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=queueforge -p 5432:5432 postgres:16-alpine
```

- [ ] Wait until it is ready: `docker exec qf-pg pg_isready -U postgres` (repeat until it says "accepting connections")
- [ ] Set the connection string for your shell session (you must repeat this in every new terminal):

```bash
export QF_DATABASE_URL='postgres://postgres:postgres@localhost:5432/queueforge?sslmode=disable'
```

- [ ] If port 5432 is already used on your laptop, change both the `-p` mapping (for example `5433:5432`) and the port in the URL.

## Phase 4. Make the module importable, then build

The Go module is named just `queueforge`, which other projects cannot download. Rename it to where you will host the repository.

- [ ] Decide your GitHub path, for example `github.com/YOURNAME/queueforge`.
- [ ] Run: `./scripts/set-module-path.sh github.com/YOURNAME/queueforge`
- [ ] Download dependencies and tidy: `go mod tidy` (needs internet; commit `go.mod` and `go.sum` afterwards)
- [ ] **Format check:** `gofmt -l .` should print nothing. If it prints file names, fix them with `gofmt -w .` and review the diff with `git diff`.
- [ ] **Compile everything:** `go build ./...`
- [ ] **Static checks:** `go vet ./...`
- [ ] Commit: `git add -A && git commit -m "module path, tidy, gofmt"`

**If `go build` or `go vet` fails (likely, because roughly 700 lines were written without a compiler):**

- [ ] Copy the full error output into our chat. The most likely files are `internal/postgres/job_repo.go`, `internal/httpapi/batch.go`, `internal/worker/completer.go`, `workerkit/workerkit.go`, `outbox/outbox.go`, and tests under `tests/`.
- [ ] Do not "fix" by deleting features. Send me the errors and I will give exact corrections.
- [ ] Repeat `go build ./... && go vet ./...` until both print nothing.

## Phase 5. Run the tests (this is the most valuable phase)

Save all output to files so nothing is lost. Create a folder first: `mkdir -p evidence`.

- [ ] **Python SDK tests** (no database needed; these already passed for me): `cd sdk/python && python3 -m unittest discover -s tests -v 2>&1 | tee ../../evidence/python-tests.txt; cd ../..` Expect `Ran 8 tests ... OK`.
- [ ] **Quick Go tests:** `go test -short -count=1 ./... 2>&1 | tee evidence/go-short.txt`
- [ ] **Full tests with the race detector** (one package at a time so they do not fight over the database): `go test -race -count=1 -p 1 -timeout 30m ./... 2>&1 | tee evidence/go-race.txt`
- [ ] Note the result: total packages passed and failed. Write the numbers here: passed \_\_\_\_\_\_ failed \_\_\_\_\_\_
- [ ] **My new tests specifically** (these have never run):

```bash
go test -race -count=1 -v -run 'TestSafeCall' ./internal/worker/
go test -race -count=1 -v -run 'TestClientRateLimiter' ./internal/auth/
go test -race -count=1 -v -run 'TestRedactSecrets' ./internal/domain/
go test -race -count=1 -v -run 'TestEnqueueJobTx|TestCompleteJobsSuccessBatch' ./tests/regression/
go test -race -count=1 -v -run 'TestBatchEnqueue|TestBruteForceCannotBeBypassed' ./tests/security/
```

- [ ] **Property test** (checks the state machine against a model): `go test -race -count=1 -v ./tests/property/ 2>&1 | tee evidence/property.txt`. If a seed fails, it prints a replay command: copy it into our chat.
- [ ] For every failing test: save the name, the full output, and send it to me. Do not skip failures.
- [ ] Commit the evidence folder: `git add evidence && git commit -m "test evidence"`

**Pass criteria for this phase:** `go build`, `go vet` and `go test -race` all clean, zero data races reported.

## Phase 6. Run it like a new user would (README quickstart)

Stop the test database first if the ports clash: `docker stop qf-pg`. This proves the README works from scratch.

- [ ] Start everything: `docker compose -f deploy/docker-compose.yml up -d --build`
- [ ] Check all services: `docker compose -f deploy/docker-compose.yml ps` (all should be running or healthy; if a service name differs from the README, note it)
- [ ] Create a tenant: `docker compose -f deploy/docker-compose.yml exec api /app/admin create-tenant -name demo -rate 50 -burst 100 -pending 1000 -concurrent 4` and copy the tenant UUID it prints.
- [ ] Create a key: `docker compose -f deploy/docker-compose.yml exec api /app/admin create-api-key -tenant-id <TENANT_UUID> -name demo-key`. The `qf_live_...` secret shows **once**; save it in a password manager.
- [ ] Enqueue:

```bash
export KEY=qf_live_PASTE_HERE
curl -s -X POST localhost:8080/v1/jobs -H "Authorization: Bearer $KEY" -H "Idempotency-Key: demo-1" -H "Content-Type: application/json" -d '{"task_type":"echo","payload":{"hello":"world"}}'
```

- [ ] Repeat the exact same command: you should get the **same job id** (HTTP 200, idempotent replay), not a new job.
- [ ] Check the job reaches `succeeded`: `curl -s localhost:8080/v1/jobs/<JOB_ID> -H "Authorization: Bearer $KEY"`
- [ ] Open http://localhost:8080/ui/ and paste your key. Confirm the job shows up.
- [ ] **Batch endpoint:**

```bash
curl -s -i -X POST localhost:8080/v1/jobs/batch -H "Authorization: Bearer $KEY" -H "Idempotency-Key: batch-1" -H "Content-Type: application/json" -d '{"jobs":[{"task_type":"echo","payload":{"i":1}},{"task_type":"echo","payload":{"i":2}}]}'
```

Expect `202` and two jobs. Run it again: expect `200` with the same two ids.

- [ ] **Python SDK against the live API:** `cd sdk/python && python3 -c "from queueforge import QueueForgeClient; c=QueueForgeClient(base_url='http://localhost:8080', api_key='$KEY'); print(c.submit_job('echo', {'a':1}))"; cd ../..`
- [ ] Write down every step of the README that did not work as written, then fix the README.

## Phase 7. Prove the fixes I made

- [ ] **Panicking handler must not kill the worker.** Temporarily add this entry inside the handler map in `cmd/worker/main.go`:

```go
"panic.test": func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
    var m map[string]int
    m["boom"] = 1 // nil map write panics
    return nil, nil
},
```

Rebuild and restart the worker (`docker compose -f deploy/docker-compose.yml up -d --build worker`), enqueue a `panic.test` job, then run `docker compose -f deploy/docker-compose.yml logs -f worker`. **Pass:** you see "handler panicked" with a stack trace, the worker container keeps running (`ps` shows no restart), and the job shows retries then `dead`. Remove the temporary handler afterwards.

- [ ] **Spoofed IP must not bypass the limiter.** With the default config (`QF_TRUST_FORWARDED_FOR` unset), run:

```bash
for i in $(seq 1 60); do curl -s -o /dev/null -w "%{http_code} " localhost:8080/v1/jobs -H "Authorization: Bearer qf_live_wrongwrongwrongwrongwrongwrongwrong1234" -H "X-Forwarded-For: 10.0.0.$i"; done; echo
```

**Pass:** the codes switch from `401` to `429` after about 20 requests. If they stay `401` for all 60, the bypass is back; tell me.

- [ ] **Error text must not leak.** Force a 500 if you can (for example stop the database container briefly and call the API) and confirm the response body is the generic "Failed to enqueue job", not a database error string.
- [ ] **Secret redaction:** add a temporary handler that returns `fmt.Errorf("dial postgres://app:hunter2@db failed")`, enqueue it, then read the job via the API. **Pass:** the stored error shows `[REDACTED]`, not `hunter2`. Remove the handler.

## Phase 8. Measure performance honestly

Use the disposable test database again (`docker start qf-pg`, then re-export `QF_DATABASE_URL`). Close other heavy programs and plug the laptop in. Write down your CPU model, core count and RAM.

- [ ] Baseline, **3 runs**, per-job completion:

```bash
for i in 1 2 3; do QF_BATCH_COMPLETION=false QF_BENCH_JOBS=3000 QF_BENCH_WORKERS=1 go test -count=1 -v -run 'TestBenchmark_Drain' ./tests/load/ | grep BENCH_RESULT; done | tee evidence/bench-baseline.txt
```

- [ ] Same with batch completion, **3 runs**:

```bash
for i in 1 2 3; do QF_BATCH_COMPLETION=true QF_BENCH_JOBS=3000 QF_BENCH_WORKERS=1 go test -count=1 -v -run 'TestBenchmark_Drain' ./tests/load/ | grep BENCH_RESULT; done | tee evidence/bench-batch.txt
```

- [ ] Idle latency: `go test -count=1 -v -run 'TestBenchmark_Idle' ./tests/load/ | tee evidence/bench-idle.txt`
- [ ] Compute the **median** of each set of three. If batch completion is not clearly faster, leave `QF_BATCH_COMPLETION` off and say so in the docs. Never quote a number you did not measure.
- [ ] Put the machine description plus the numbers in `docs/benchmark-reports/report.md` under a new heading "Reproduced on \<date>".
- [ ] Optional: run the head-to-head against River and Asynq (`bench/headtohead/README.md`); it needs Redis (`docker run -d -p 6379:6379 redis:7`).

## Phase 9. Break things on purpose (only against the disposable database)

- [ ] **Worker kill -9:** `go test -count=1 -v -run RealKill9 ./tests/chaos/ | tee evidence/chaos-kill9.txt`. Pass: recovery within about lease time, exactly 2 attempts, no leaked slots.
- [ ] **PostgreSQL outage** (uses your disposable container, never a real database):

```bash
QF_CHAOS_PG_STOP='docker stop qf-pg' QF_CHAOS_PG_START='docker start qf-pg' go test -count=1 -v -p 1 -timeout 20m -run PostgresOutage ./tests/chaos/ | tee evidence/chaos-outage.txt
```

Pass: all jobs finish, zero dead, zero leaks.

- [ ] **Network faults:** `QF_CHAOS_STRICT=1 go test -count=1 -v -p 1 -run NetworkFaults ./tests/chaos/ | tee evidence/chaos-network.txt`
- [ ] **Soak:** `QF_SOAK_SECONDS=1800 QF_SOAK_RATE=150 go test -v -timeout 60m -run TestSoak ./tests/load/ | tee evidence/soak.txt` (30 minutes; leave the laptop awake and plugged in). Note whether latency rises over time.
- [ ] Save every output in `evidence/` and commit it.

## Phase 10. Security checks

- [ ] Install and run the vulnerability scanner: `go install golang.org/x/vuln/cmd/govulncheck@latest && govulncheck ./... | tee evidence/govulncheck.txt`
- [ ] Install gitleaks (see https://github.com/gitleaks/gitleaks) and run it over the **whole history** before publishing: `gitleaks detect --source . -v | tee evidence/gitleaks.txt`. Any real secret found means rotate it, then remove it from history before pushing.
- [ ] Scan the image: `docker build -f deploy/Dockerfile -t queueforge:local . && trivy image queueforge:local | tee evidence/trivy.txt` (install trivy from its website).
- [ ] Triage each finding: fix, or write down why it does not apply.
- [ ] **Least-privilege database roles** (untested script):

```bash
docker exec -i qf-pg psql -U postgres -d queueforge -v app_pw="'apppass123'" -v migrate_pw="'migratepass123'" < deploy/sql/least_privilege.sql
```

Then start the API with `QF_DATABASE_URL=postgres://qf_app:apppass123@localhost:5432/queueforge?sslmode=disable` and `QF_MIGRATE_DATABASE_URL=postgres://qf_migrate:migratepass123@localhost:5432/queueforge?sslmode=disable`, and run `./deploy/smoke.sh`. Pass: everything works, and `psql` as `qf_app` cannot run `DROP TABLE jobs;` (it must be denied).

## Phase 11. Make sure you actually understand it (for interviews)

- [ ] Draw the job state machine and the claim, complete and recover flows on paper from memory.
- [ ] Rebuild three bugs from `docs/ENGINEERING_LOG.md` without looking at the fix: pick F-1 (poison pill in recovery), F-13 (volatile `clock_timestamp()`), and F-25 (panic in handler). For each: write the failing test, watch it fail on the old code, fix it, watch it pass.
- [ ] Write one paragraph per bug in **your own words**: what you saw, how you found it, what you changed.
- [ ] Be able to answer: Why is exactly-once execution impossible? What does lease fencing protect and what does it not? Why `SKIP LOCKED`? What does QueueForge give that River does not, and the reverse?
- [ ] Move interview-coaching content out of the public repo: `ROADMAP_TO_9.md`, the "STAR stories" sections of `ENGINEERING_LOG.md`, and any student-specific example (`examples/placement-alert-bot`). Keep them in a private notes folder.
- [ ] Fix stale text: `QueueForge_TDD.md` still lists the removed `QF_DATABASE_TLS_MODE`. Update the docs to match what you measured in Phases 5-9.

## Phase 12. Dogfood it in a separate project

- [ ] Create a new empty Go module outside the repo and run `go get` of your published module (do this after Phase 13). Write a worker using `workerkit.Run` with one real task (for example sending yourself an email through a mail API or resizing an image).
- [ ] Enqueue from your app using the Go SDK. Then try `outbox.EnqueueTx` inside a real transaction: insert a row and a job, then roll back and confirm **neither** exists; repeat with a commit and confirm **both** exist.
- [ ] Do the same from Python with `submit_job` and `submit_batch`.
- [ ] Note every confusing moment; each is a documentation bug.

## Phase 13. Publish and release

- [ ] Create an empty GitHub repository with the same path you chose in Phase 4.
- [ ] Push: `git remote add origin git@github.com:YOURNAME/queueforge.git && git push -u origin main`
- [ ] Watch GitHub Actions: the `ci` workflow must be green. Fix anything red.
- [ ] Tag the first release only after CI is green: `git tag v0.1.0 && git push origin v0.1.0`. The `release` workflow runs the tests, builds the image and publishes to ghcr.io.
- [ ] In GitHub, make the package public if you want others to pull it, and confirm `docker pull ghcr.io/YOURNAME/queueforge:v0.1.0` works from another machine.
- [ ] From a fresh empty folder: `go mod init demo && go get github.com/YOURNAME/queueforge/workerkit@v0.1.0` must succeed.
- [ ] Add repository topics, a short description, and keep `CHANGELOG.md` honest about what is and is not verified.

## Phase 14. Real deployment (needed to move past \~55)

- [ ] Rent a small VPS (2 vCPU, 4 GB) and a cheap domain. Follow `docs/DEPLOYMENT.md` sections 1-5 exactly. Write down each deviation.
- [ ] Run `./deploy/smoke.sh` from your laptop against the public URL; expect `failed=0`.
- [ ] **Backups:** run `./deploy/backup.sh`, then `./deploy/restore-drill.sh`, and record whether the restore worked and how long it took. Copy dumps off the machine.
- [ ] Kill a worker on purpose: `docker kill <worker container>` and watch the job get recovered in the logs.
- [ ] Set up an external uptime monitor on `/readyz` and a cron canary that enqueues an `echo` job every 5 minutes.
- [ ] Optional: apply `deploy/k8s/queueforge.yaml` to a test cluster with `kubectl apply --dry-run=server` first.
- [ ] Invite 2-3 friends or a student club to use it as separate tenants for **4+ weeks**.
- [ ] Keep `docs/OPERATIONS_LOG.md`: date, what happened, how detected, impact, root cause, fix, prevention. Write at least one real incident.

## Phase 15. Send me the evidence

When you reach the end of Phase 5 (and again after 9, 10 and 14), paste into our chat:

- [ ] Output of `go version`, `go build ./...`, `go vet ./...` (or the errors)
- [ ] The summary lines of `go test -race` (passed and failed packages) and the full text of any failure
- [ ] Benchmark medians from Phase 8 with your hardware
- [ ] Pass or fail for each chaos test in Phase 9
- [ ] The scanner findings from Phase 10
- [ ] Anything in the README or this checklist that did not work

I will fix what failed and re-score honestly with the new evidence.

## Expected score as you finish each phase

| You have completed | Realistic adopter score |
| --- | --- |
| Nothing (today) | \~30 |
| Phases 1-5, all green | \~45 |
| + Phase 6 quickstart, Phase 7 fixes verified, Phase 13 release | \~50 |
| + Phases 8-10 evidence, Phase 11 understanding | \~55 |
| + Phase 14 deployment, 4+ weeks of real use, one real incident | 60-65 |
| Above that | Real external users, a Python worker protocol, independent review |

## If you get stuck

- **Tests cannot connect to the database:** check `docker ps`, the port, and that `QF_DATABASE_URL` is exported in the **same terminal**.
- **Tests hang or are flaky:** use `-p 1` and close other heavy programs; the project's own report notes noise on small machines.
- **A chaos test is flaky:** run it alone with `QF_CHAOS_STRICT=1`, then send me the output; do not loosen the assertion.
- **`go mod tidy` fails:** send the error and your `go version`.
- **GitHub Actions is red but local is green:** send the failing log; likely a Go version or formatting difference.
