-- name: GetLedgerNet :one
SELECT COALESCE(SUM(
  CASE
    WHEN direction = 'credit' THEN amount
    WHEN direction = 'debit' THEN -amount
    ELSE 0
  END
), 0)::bigint AS net_amount
FROM entries;

-- name: GetLedgerCurrencyImbalances :many
SELECT
  a.currency,
  COALESCE(SUM(
    CASE
      WHEN e.direction = 'credit' THEN e.amount
      WHEN e.direction = 'debit' THEN -e.amount
      ELSE 0
    END
  ), 0)::bigint AS net_amount
FROM entries e
INNER JOIN accounts a ON a.id = e.account_id
GROUP BY a.currency
HAVING COALESCE(SUM(
  CASE
    WHEN e.direction = 'credit' THEN e.amount
    WHEN e.direction = 'debit' THEN -e.amount
    ELSE 0
  END
), 0) <> 0;
