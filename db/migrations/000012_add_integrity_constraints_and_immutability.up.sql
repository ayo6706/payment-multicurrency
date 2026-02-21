DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'accounts_locked_nonnegative_ck'
  ) THEN
    ALTER TABLE accounts
      ADD CONSTRAINT accounts_locked_nonnegative_ck CHECK (locked_micros >= 0);
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'transactions_amount_positive_ck'
  ) THEN
    ALTER TABLE transactions
      ADD CONSTRAINT transactions_amount_positive_ck CHECK (amount > 0);
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'transactions_currency_ck'
  ) THEN
    ALTER TABLE transactions
      ADD CONSTRAINT transactions_currency_ck CHECK (currency IN ('USD', 'EUR', 'GBP'));
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'transactions_type_ck'
  ) THEN
    ALTER TABLE transactions
      ADD CONSTRAINT transactions_type_ck CHECK (type IN ('transfer', 'exchange', 'payout', 'deposit'));
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'transactions_status_ck'
  ) THEN
    ALTER TABLE transactions
      ADD CONSTRAINT transactions_status_ck CHECK (status IN ('PENDING', 'PROCESSING', 'COMPLETED', 'FAILED', 'REVERSED'));
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'entries_amount_positive_ck'
  ) THEN
    ALTER TABLE entries
      ADD CONSTRAINT entries_amount_positive_ck CHECK (amount > 0);
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'entries_direction_ck'
  ) THEN
    ALTER TABLE entries
      ADD CONSTRAINT entries_direction_ck CHECK (direction IN ('debit', 'credit'));
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'payouts_amount_positive_ck'
  ) THEN
    ALTER TABLE payouts
      ADD CONSTRAINT payouts_amount_positive_ck CHECK (amount_micros > 0);
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'payouts_currency_ck'
  ) THEN
    ALTER TABLE payouts
      ADD CONSTRAINT payouts_currency_ck CHECK (currency IN ('USD', 'EUR', 'GBP'));
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'payouts_status_ck'
  ) THEN
    ALTER TABLE payouts
      ADD CONSTRAINT payouts_status_ck CHECK (status IN ('PENDING', 'PROCESSING', 'COMPLETED', 'FAILED', 'MANUAL_REVIEW'));
  END IF;
END $$;

CREATE OR REPLACE FUNCTION immutable_record_guard() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION '% is append-only and cannot be modified', TG_TABLE_NAME;
END;
$$;

DROP TRIGGER IF EXISTS trg_audit_log_immutable ON audit_log;
CREATE TRIGGER trg_audit_log_immutable
BEFORE UPDATE OR DELETE ON audit_log
FOR EACH ROW
EXECUTE FUNCTION immutable_record_guard();

DROP TRIGGER IF EXISTS trg_entries_immutable ON entries;
CREATE TRIGGER trg_entries_immutable
BEFORE UPDATE OR DELETE ON entries
FOR EACH ROW
EXECUTE FUNCTION immutable_record_guard();
