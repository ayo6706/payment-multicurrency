-- name: CreateUser :one
INSERT INTO users (id, username, email, created_at) 
VALUES ($1, $2, $3, NOW()) 
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
SELECT id, username, email, created_at 
FROM users 
WHERE id = $1;