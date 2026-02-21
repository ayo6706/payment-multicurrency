# Failure Drills and Performance Validation

## Drill Cadence

- Run monthly in staging.
- Run after major payout or idempotency refactors.

## Drill 1: Gateway Timeout Forces Manual Review

1. Configure/mock gateway to return timeout errors after network send.
2. Create admin payout requests (`POST /v1/payouts`).
3. Confirm payout moves to `MANUAL_REVIEW` and funds remain locked.
4. Confirm `payout_manual_review_queue_size` increases.
5. Resolve with `/v1/payouts/{id}/resolve` and validate final state.

Success criteria:
- No duplicate payout sends.
- No unlocked funds before operator decision.

## Drill 2: Idempotency Conflict and Replay

1. Send transfer request with an idempotency key.
2. Replay exact request with same key (expect replay response).
3. Replay with same key and modified payload (expect `409`).
4. Confirm `idempotency_events_total` includes `replay` and `hash_mismatch`.

Success criteria:
- Exactly one committed transfer for a key.

## Drill 3: Webhook Signature and Reference Safety

1. Submit deposit webhook with invalid signature (expect `401`).
2. Submit valid webhook payload (expect `200`).
3. Replay same reference with changed payload (expect `409`).

Success criteria:
- No signature bypass.
- No mixed-payload reuse for same reference.

## Drill 4: Database Interruption Recovery

1. Stop PostgreSQL briefly during load.
2. Confirm readiness turns unhealthy.
3. Restore PostgreSQL and verify service recovery.
4. Run reconciliation and check no net imbalance.

Success criteria:
- Service fails closed and recovers without data corruption.

## k6 Transfer Load Test

Script: `scripts/k6/internal_transfer_load.js`

Example:

```bash
k6 run \
  -e BASE_URL=http://localhost:8080 \
  -e TOKEN=<jwt> \
  -e FROM_ACCOUNT_ID=<uuid> \
  -e TO_ACCOUNT_ID=<uuid> \
  -e AMOUNT_MICROS=100 \
  scripts/k6/internal_transfer_load.js
```

Interpretation:
- `http_req_failed` should remain near zero.
- `http_req_duration p95` should stay below threshold budget.
- Investigate lock contention if p95 spikes under steady load.
