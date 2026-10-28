# Changelog

All notable changes. QueueForge is pre-1.0: interfaces may change between minor versions.

## v0.1.1
> Pre-1.0 release. Interfaces may change between minor versions. Validation covers the documented
> Go, PostgreSQL, Docker, Kubernetes, chaos, and deployment checks; no extended public production
> deployment is claimed.

### Added
- `outbox` package: public transactional enqueue (`EnqueueTx`) inside your own PostgreSQL transaction.
- `workerkit` package: public worker entry point (`Run`, `Permanent`); handlers no longer require forking.
- `POST /v1/jobs/batch`: atomic enqueue of up to 100 jobs; Go SDK `EnqueueBatch`; Python SDK `submit_batch`.
- Per-client request rate limiter (`QF_CLIENT_RATE_PER_SECOND`, `QF_CLIENT_RATE_BURST`).
- Opt-in batch completion (`QF_BATCH_COMPLETION=true`): one statement for many successful jobs.
- Optional least-privilege database roles (`QF_MIGRATE_DATABASE_URL`, `deploy/sql/least_privilege.sql`).
- Secret redaction of stored handler error messages.
- Kubernetes manifests (`deploy/k8s`), release workflow, `scripts/set-module-path.sh`.

### Fixed
- A panicking task handler no longer terminates the worker process (it is retried like any failure).
- Client address spoofing: `RealIP` middleware only runs when `QF_TRUST_FORWARDED_FOR=true`.
- Python SDK `submit_job` now always sends an Idempotency-Key (retries could create duplicate jobs). **Tested: 8/8 Python SDK tests pass.**
- 500 responses no longer include internal error text.

### Removed
- `QF_DATABASE_TLS_MODE`: it was read but never used. TLS is controlled by `sslmode` in `QF_DATABASE_URL`.

## Unreleased

Future changes will be recorded here.
