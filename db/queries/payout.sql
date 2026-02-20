-- name: InsertPayout :one
INSERT INTO payouts (id, transaction_id, account_id, amount_micros, currency, status, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
RETURNING *;

-- name: GetPayout :one
SELECT * FROM payouts WHERE id = $1;

-- name: GetPendingPayouts :many
SELECT * FROM payouts 
WHERE status = 'PENDING' 
ORDER BY created_at ASC
FOR UPDATE SKIP LOCKED 
LIMIT $1;

-- name: GetStaleProcessingPayouts :many
SELECT * FROM payouts
WHERE status = 'PROCESSING' AND updated_at < $1
ORDER BY updated_at ASC
FOR UPDATE SKIP LOCKED
LIMIT $2;

-- name: UpdatePayoutStatus :exec
UPDATE payouts
SET status = $1, gateway_ref = $2, updated_at = NOW()
WHERE id = $3;

-- name: GetPayoutByTransactionID :one
SELECT * FROM payouts WHERE transaction_id = $1;
