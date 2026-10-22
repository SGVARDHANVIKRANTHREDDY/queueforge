# Architecture Decision Records

| ADR | Decision |
|---|---|
| [001](ADR-001.md) | PostgreSQL first; no Redis in v1 |
| [002](ADR-002.md) | At-least-once execution semantics |
| [003](ADR-003.md) | Direct Postgres worker claims via `FOR UPDATE SKIP LOCKED` |
| [004](ADR-004.md) | Lease duration and recovery timing |
| [005](ADR-005.md) | Strict priority ordering in v1 |
| [006](ADR-006.md) | Schedule misfire behaviour and occurrence uniqueness |
| [007](ADR-007.md) | Tenant quota implementation and execution slots |
| [008](ADR-008.md) | Retention and replay semantics |
| [009](ADR-009.md) | API key storage, issuance and permissions |
| [010](ADR-010.md) | Benchmark protocol and honest reporting |
| [011](ADR-011.md) | LISTEN/NOTIFY as a wake-up hint; polling stays the source of truth |
| [012](ADR-012.md) | Recover each expired lease in its own transaction |
| [013](ADR-013.md) | Skip contended tenants when claiming instead of idling |
| [014](ADR-014.md) | The database is the only clock (and stable vs volatile time functions) |
| [015](ADR-015.md) | Single-statement batch claim with per-(queue, type, priority) probes |
| [016](ADR-016.md) | Auth hardening, audit log, redacted worker visibility |
