-- Revert partitioned entries table to standing table
ALTER TABLE entries RENAME TO entries_partitioned;

CREATE TABLE entries (
    id UUID PRIMARY KEY,
    transaction_id UUID NOT NULL REFERENCES transactions(id),
    account_id UUID NOT NULL REFERENCES accounts(id),
    amount BIGINT NOT NULL,
    direction VARCHAR(10) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Migrate data back
INSERT INTO entries SELECT id, transaction_id, account_id, amount, direction, created_at FROM entries_partitioned;

-- Drop partitioned table and its partitions
DROP TABLE entries_partitioned;

-- Remove locked_micros
ALTER TABLE accounts DROP COLUMN IF EXISTS locked_micros;
