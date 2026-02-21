# Payment Processing System

Production-oriented multi-currency payment backend in Go with PostgreSQL and Redis.

## 0. Requirement Mapping (Assessment Checklist)

- Internal multi-currency payments (`USD`, `EUR`, `GBP`): implemented
- External multi-currency payouts (`USD`, `EUR`, `GBP`): implemented
- Golang + PostgreSQL: implemented
- Docker Compose startup: implemented (`docker compose up --build` or `docker-compose up --build`)
- README + architecture/trade-offs + future improvements: documented
- Tests + testing approach: documented and automated

## 1. What was implemented

### Core functional scope
- Internal user-to-user transfers in `USD`, `EUR`, `GBP`
- Cross-currency FX transfers using a 4-entry liquidity-account pattern
- External payouts (`PENDING -> PROCESSING -> COMPLETED/FAILED/MANUAL_REVIEW`) via async worker
- Deposit webhook ingestion with HMAC validation

### Financial correctness controls
- Double-entry ledger writes for money movement
- Pessimistic locking with `SELECT ... FOR UPDATE`
- `FOR UPDATE SKIP LOCKED` payout claiming for safe multi-worker processing
- Idempotency (Redis fast path + PostgreSQL source of truth)
- Transaction state machine for all transaction types:
  - `PENDING -> PROCESSING -> COMPLETED`
  - `PROCESSING -> FAILED`
  - `COMPLETED/FAILED -> REVERSED` (model supports; reverse endpoint not implemented)
- Immutable `audit_log` entries for state transitions
- Reconciliation worker to detect ledger imbalance and emit critical telemetry

### Production hardening
- Structured JSON logging with request trace IDs (`zap`)
- Prometheus metrics (`/metrics`)
- Health probes (`/health/live`, `/health/ready`)
- Tiered rate limiting (`go-chi/httprate`)
- RBAC middleware (`user` vs `admin`)
- RFC 7807 error responses across handlers and middleware

## 2. Tech stack

- Go
- PostgreSQL 16
- Redis 7
- Chi router
- pgx + sqlc
- Docker Compose

## 3. Run the system

### Prerequisites
- Docker + Docker Compose
- (Optional for local run) Go installed

### Start everything
```bash
docker compose up --build
```

If your environment uses the legacy Compose CLI:
```bash
docker-compose up --build
```

This starts:
- `db` (PostgreSQL)
- `redis`
- `migrate` (applies all migrations)
- `api` (HTTP server on `http://localhost:8080`)

## 3.1 Evaluator Quickstart (2 minutes)

This short flow demonstrates:
- internal user payment
- external payout initiation
- payout status polling

```bash
# 1) Create two users
U1=$(curl -s -X POST http://localhost:8080/v1/users -H "Content-Type: application/json" -d '{"username":"eval_a","email":"eval_a@example.com"}' | jq -r .id)
U2=$(curl -s -X POST http://localhost:8080/v1/users -H "Content-Type: application/json" -d '{"username":"eval_b","email":"eval_b@example.com"}' | jq -r .id)

# 2) Login first user and capture token
TOKEN=$(curl -s -X POST http://localhost:8080/v1/auth/login -H "Content-Type: application/json" -d "{\"user_id\":\"$U1\"}" | jq -r .token)

# 3) Create USD accounts
A1=$(curl -s -X POST http://localhost:8080/v1/accounts -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d "{\"user_id\":\"$U1\",\"currency\":\"USD\",\"balance\":2000000}" | jq -r .id)
A2=$(curl -s -X POST http://localhost:8080/v1/accounts -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d "{\"user_id\":\"$U2\",\"currency\":\"USD\",\"balance\":0}" | jq -r .id)

# 4) Internal transfer with idempotency key
curl -s -X POST http://localhost:8080/v1/transfers/internal \
  -H "Authorization: Bearer $TOKEN" \
  -H "Idempotency-Key: $(uuidgen)" \
  -H "Content-Type: application/json" \
  -d "{\"from_account_id\":\"$A1\",\"to_account_id\":\"$A2\",\"amount\":500000}" | jq .

# 5) Promote user1 to admin (for payout endpoint), re-login, and request payout
docker exec payment_db psql -U user -d payment_system -c "UPDATE users SET role='admin' WHERE id='$U1';"
TOKEN_ADMIN=$(curl -s -X POST http://localhost:8080/v1/auth/login -H "Content-Type: application/json" -d "{\"user_id\":\"$U1\"}" | jq -r .token)
PAYOUT_ID=$(curl -s -X POST http://localhost:8080/v1/payouts \
  -H "Authorization: Bearer $TOKEN_ADMIN" \
  -H "Idempotency-Key: $(uuidgen)" \
  -H "Content-Type: application/json" \
  -d "{\"account_id\":\"$A1\",\"amount_micros\":100000,\"currency\":\"USD\",\"destination\":{\"iban\":\"GB29NWBK60161331926819\",\"name\":\"Evaluator\"}}" | jq -r .payout_id)

# 6) Poll payout status
curl -s -X GET http://localhost:8080/v1/payouts/$PAYOUT_ID -H "Authorization: Bearer $TOKEN_ADMIN" | jq .
```

Notes:
- Commands use `jq` for readability.
- Swagger UI is available at `http://localhost:8080/swagger/index.html`.

### Useful endpoints
- `GET /health/live`
- `GET /health/ready`
- `GET /metrics`
- `GET /openapi.yaml`
- `GET /swagger/index.html`
- `POST /v1/users`
- `POST /v1/auth/login`
- `POST /v1/accounts`
- `GET /v1/accounts/{id}/balance`
- `GET /v1/accounts/{id}/statement`
- `POST /v1/transfers/internal`
- `POST /v1/transfers/exchange`
- `POST /v1/payouts`
- `GET /v1/payouts/manual-review` (admin)
- `POST /v1/payouts/{id}/resolve` (admin)
- `GET /v1/payouts/{id}`
- `POST /v1/webhooks/deposit`

## 4. Configuration

Configured through environment variables (loaded with `viper`):

- `PORT`
- `DATABASE_URL`
- `REDIS_URL`
- `JWT_SECRET` (required, minimum 32 characters)
- `JWT_ISSUER` (required)
- `JWT_AUDIENCE` (required)
- `WEBHOOK_HMAC_KEY`
- `WEBHOOK_SKIP_SIG`
- `PAYOUT_POLL_INTERVAL`
- `PAYOUT_BATCH_SIZE`
- `RECONCILIATION_INTERVAL`
- `PUBLIC_RATE_LIMIT_RPS`
- `AUTH_RATE_LIMIT_RPS`
- `IDEMPOTENCY_TTL`

## 5. Error contract (RFC 7807)

All errors are returned as `application/problem+json`:

```json
{
  "type": "https://errors.paymentapp.com/<slug>",
  "title": "Bad Request",
  "status": 400,
  "detail": "validation message",
  "instance": "/v1/transfers/internal",
  "request_id": "<trace-id>"
}
```

## 6. Testing

Run all tests:
```bash
go test ./...
```

Current approach:
- Service-level tests for transfer, payout, webhook, reconciliation
- API integration tests via `httptest`
- Repository-level tests for DB interactions

## 7. Architecture and trade-offs

### Why this shape
- Layered design (`api -> service -> repository`) to keep transport, business rules, and SQL concerns isolated.
- Financial amounts are persisted as integer micros (`BIGINT`) end-to-end to avoid floating-point precision drift.
- FX math uses `shopspring/decimal` and is converted back to integer micros before persistence.
- Money movement writes double-entry ledger rows and account balance updates in the same DB transaction.
- Critical state transitions are audited (`audit_log`) for traceability and post-incident reconstruction.

### Consistency and failure handling decisions
- Payment mutations (internal transfer, exchange, payout state/finalization, deposit webhook) run inside ACID transactions.
- Account-level concurrency control is pessimistic (`SELECT ... FOR UPDATE`), with stable lock ordering to reduce deadlocks.
- Payout workers claim work with `FOR UPDATE SKIP LOCKED` so multiple workers can scale safely without double processing.
- Idempotency is two-layered: Redis for fast replay + PostgreSQL as authoritative source of truth.

### Deliberately deferred due to scope/time
- Broker-backed async pipeline (Kafka/RabbitMQ) for stronger decoupling between API acceptance and payout execution.
- Outbox/inbox pattern for exactly-once side effects to external gateways.
- More robust exchange-rate synchronization (scheduled refresh + staleness policy + provider failover/circuit breakers).
- Adaptive/distributed rate limiting strategy (global limits, per-tenant quotas, abuse controls beyond fixed RPS).
- Full reversal API (`REVERSED`) and broader authorization policy around operational interventions.

For deeper detail see `docs/architecture.md`.

Operational docs:
- `docs/oncall-runbook.md`
- `docs/failure-drills.md`
