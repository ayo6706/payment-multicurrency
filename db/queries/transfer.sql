-- name: CheckTransactionIdempotency :one
SELECT id, amount, currency, type, status, reference_id, fx_rate, created_at 
FROM transactions 
WHERE reference_id = $1;

-- name: LockAccount :one
SELECT id FROM accounts WHERE id = $1 FOR UPDATE;

-- name: GetAccountBalanceAndCurrency :one
SELECT balance, currency FROM accounts WHERE id = $1;

-- name: GetAccountCurrency :one
SELECT currency FROM accounts WHERE id = $1;

-- name: CreateTransaction :one
INSERT INTO transactions (id, amount, currency, type, status, reference_id, fx_rate, metadata, created_at) 
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
RETURNING id;

-- name: CreateEntry :one
INSERT INTO entries (id, transaction_id, account_id, amount, direction, created_at) 
VALUES ($1, $2, $3, $4, $5, NOW())
RETURNING id;

-- name: UpdateAccountBalance :execrows
UPDATE accounts
SET balance = balance + $1
WHERE id = $2;

-- name: GetTransaction :one
SELECT id, amount, currency, type, status, reference_id, fx_rate, metadata, created_at
FROM transactions
WHERE id = $1;

-- name: GetTransactionStatusForUpdate :one
SELECT status
FROM transactions
WHERE id = $1
FOR UPDATE;

-- name: UpdateTransactionStatus :execrows
UPDATE transactions
SET status = $1
WHERE id = $2;

-- name: UpdateTransactionFx :execrows
UPDATE transactions
SET fx_rate = $1,
    metadata = $2
WHERE id = $3;
