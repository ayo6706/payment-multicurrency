DROP TRIGGER IF EXISTS trg_entries_immutable ON entries;
DROP TRIGGER IF EXISTS trg_audit_log_immutable ON audit_log;
DROP FUNCTION IF EXISTS immutable_record_guard();

ALTER TABLE payouts DROP CONSTRAINT IF EXISTS payouts_status_ck;
ALTER TABLE payouts DROP CONSTRAINT IF EXISTS payouts_currency_ck;
ALTER TABLE payouts DROP CONSTRAINT IF EXISTS payouts_amount_positive_ck;

ALTER TABLE entries DROP CONSTRAINT IF EXISTS entries_direction_ck;
ALTER TABLE entries DROP CONSTRAINT IF EXISTS entries_amount_positive_ck;

ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_status_ck;
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_type_ck;
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_currency_ck;
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_amount_positive_ck;

ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_locked_within_balance_ck;
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_locked_nonnegative_ck;
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_balance_nonnegative_ck;
