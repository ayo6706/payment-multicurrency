# Architecture Decisions and Prioritization

## System shape

Layered structure:
- `internal/api`: transport (routing, middleware, handlers)
- `internal/service`: business rules and transaction orchestration
- `internal/repository`: sqlc-generated database queries
- `internal/worker`: async payout + reconciliation loops

Primary goal was correctness first, then operational safety.

## Key decisions

### 1. Ledger correctness over feature breadth
- Used double-entry patterns for internal, FX, payout, and deposit flows.
- FX uses liquidity accounts to preserve auditability per currency.
- Monetary amounts are stored as `BIGINT` micros to avoid floating-point drift.

### 2. Concurrency safety
- Account locking uses `SELECT ... FOR UPDATE`.
- Payout worker uses `FOR UPDATE SKIP LOCKED` for horizontal-safe claim semantics.
- Lock ordering by account ID in transfers minimizes deadlock risk.

### 3. Idempotency as a two-layer guard
- Redis for low-latency replay.
- PostgreSQL for authoritative persistence and crash safety.

### 4. Uniform transaction lifecycle
- All transaction types follow:
  - `PENDING -> PROCESSING -> COMPLETED`
  - `PROCESSING -> FAILED`
- Every transition writes immutable `audit_log` records.

### 5. Operational reliability
- Background reconciliation checks ledger net balance and emits critical telemetry.
- Structured logs (`zap`) and Prometheus metrics expose runtime health.
- Readiness probes verify dependencies.

### 6. API consistency
- Errors are standardized to RFC 7807 across handlers and middleware.
- Responses include `instance` and `request_id` for support/debug correlation.

## Trade-offs made

- Chosen: strong transfer/payout correctness and observability.
- Deferred:
  - Full transaction reversal endpoint and policy layer.
  - Advanced distributed tracing and SRE dashboards.
  - Full-scale continuous chaos/recovery automation.

This implementation prioritizes those three before broader feature expansion.
