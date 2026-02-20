-- name: CreateUser :one
INSERT INTO users (id, username, email, role, created_at) 
VALUES ($1, $2, $3, $4, NOW()) 
RETURNING created_at;

-- name: CreateAccount :one
INSERT INTO accounts (id, user_id, currency, balance, created_at) 
VALUES ($1, $2, $3, $4, NOW()) 
RETURNING created_at;

-- name: GetAccount :one
SELECT id, user_id, currency, balance, created_at 
FROM accounts 
WHERE id = $1;

-- name: GetEntries :many
SELECT id, transaction_id, account_id, amount, direction, created_at
FROM entries
WHERE account_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: GetUser :one
SELECT id, username, email, role, created_at 
FROM users 
WHERE id = $1;

-- name: LockAccountFunds :exec
UPDATE accounts
SET locked_micros = locked_micros + $1
WHERE id = $2;

-- name: ReleaseAccountFunds :exec
UPDATE accounts
SET locked_micros = locked_micros - $1
WHERE id = $2;

-- name: ReleaseAccountFundsSafe :execrows
UPDATE accounts
SET locked_micros = locked_micros - $1
WHERE id = $2 AND locked_micros >= $1;

-- name: DeductLockedFunds :exec
UPDATE accounts
SET locked_micros = locked_micros - $1, balance = balance - $1
WHERE id = $2;

-- name: CreditAccount :exec
UPDATE accounts
SET balance = balance + $1
WHERE id = $2;

-- name: GetAccountBalanceAndLocked :one
SELECT balance, locked_micros, currency FROM accounts WHERE id = $1 FOR UPDATE;
