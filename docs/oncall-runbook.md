# On-Call Runbook

## Scope

This runbook covers production operations for transfers, payouts, and webhook deposits.

## Key Metrics

- `http_request_duration_seconds{method,path,status}`
- `idempotency_events_total{outcome}`
- `payout_manual_review_queue_size`
- `payout_manual_review_transitions_total{action}`
- `worker_runs_total{worker,result}`
- `ledger_imbalance_total{currency}`

## Recommended Alerts

- `ledger_imbalance_total` increase > `0` over `5m`.
- `payout_manual_review_queue_size > 0` for `15m` during business hours.
- `worker_runs_total{worker="payout",result="failed"}` spike over baseline.
- `worker_runs_total{worker="reconciliation",result="failed"}` > `0` for `1h`.
- `idempotency_events_total{outcome="reserve_error"}` or `idempotency_events_total{outcome="finalize_error"}` sustained > `0`.

## Triage Sequence

1. Check `/health/ready` and dependency status (PostgreSQL, Redis).
2. Check payout worker and reconciliation worker logs.
3. Inspect `worker_runs_total` and `payout_manual_review_queue_size`.
4. Inspect affected transaction and payout rows in DB.

## Manual Review Queue Operations (Admin)

1. List queue:
   - `GET /v1/payouts/manual-review?limit=50&offset=0`
2. Resolve as sent:
   - `POST /v1/payouts/{id}/resolve` with body:
   - `{"decision":"confirm_sent","reason":"gateway confirmed settlement"}`
3. Resolve as refunded:
   - `POST /v1/payouts/{id}/resolve` with body:
   - `{"decision":"refund_failed","reason":"gateway confirmed failure"}`

## Webhook Incident Handling

1. Verify `X-Webhook-Signature` generation and shared key.
2. Check for `webhook/reference-mismatch` (payload drift on same reference).
3. Retry only with the same payload for the same reference.

## Reconciliation Incident Handling

1. If `ledger_imbalance_total` increments, freeze non-essential payout operations.
2. Run an immediate reconciliation query and identify impacted currency.
3. Reconstruct transaction timeline from `audit_log` and `entries`.
4. Escalate to incident commander and open a postmortem.
