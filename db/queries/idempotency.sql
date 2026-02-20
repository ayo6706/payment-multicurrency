-- name: GetIdempotencyKey :one
SELECT idempotency_key, request_hash, response_status, response_body, content_type, in_progress
FROM idempotency_keys
WHERE idempotency_key = $1;

-- name: ReserveIdempotencyKey :one
INSERT INTO idempotency_keys (idempotency_key, request_hash, method, path)
VALUES ($1, $2, $3, $4)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING idempotency_key;

-- name: FinalizeIdempotencyKey :one
UPDATE idempotency_keys
SET response_status = $1,
    response_body = $2,
    content_type = $3,
    in_progress = FALSE,
    updated_at = NOW()
WHERE idempotency_key = $4 AND request_hash = $5
RETURNING idempotency_key, request_hash, response_status, response_body, content_type, in_progress;
