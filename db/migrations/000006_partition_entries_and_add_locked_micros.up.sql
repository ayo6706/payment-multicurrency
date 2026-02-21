-- Add locked_micros to accounts
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS locked_micros BIGINT NOT NULL DEFAULT 0;

-- Convert entries table to a partitioned table
ALTER TABLE entries RENAME TO entries_old;

CREATE TABLE entries (
    id UUID,
    transaction_id UUID NOT NULL REFERENCES transactions(id),
    account_id UUID NOT NULL REFERENCES accounts(id),
    amount BIGINT NOT NULL,
    direction VARCHAR(10) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- Create a default partition to catch any data that doesn't fit in specific ranges
CREATE TABLE entries_default PARTITION OF entries DEFAULT;

-- Create some initial partitions
CREATE TABLE entries_y2026m01 PARTITION OF entries FOR VALUES FROM ('2026-01-01') TO ('2026-02-01');
CREATE TABLE entries_y2026m02 PARTITION OF entries FOR VALUES FROM ('2026-02-01') TO ('2026-03-01');
CREATE TABLE entries_y2026m03 PARTITION OF entries FOR VALUES FROM ('2026-03-01') TO ('2026-04-01');

-- Migrate data
INSERT INTO entries SELECT * FROM entries_old;

-- Clean up old table
DROP TABLE entries_old;
