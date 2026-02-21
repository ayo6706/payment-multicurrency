package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// setupTestDB connects to the local Postgres instance and seeds system accounts.
func setupTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	connString := os.Getenv("DATABASE_URL")
	if connString == "" {
		connString = "postgres://user:password@localhost:5432/payment_system?sslmode=disable"
	}
	db, err := pgxpool.New(context.Background(), connString)
	if err != nil {
		t.Fatalf("Failed to connect to DB: %v", err)
	}

	ensureLockedMicrosColumn(t, db)
	ensurePayoutsTable(t, db)
	ensureAuditLogTable(t, db)

	for _, table := range []string{"audit_log", "entries", "transactions", "payouts", "accounts", "users"} {
		stmt := fmt.Sprintf("TRUNCATE TABLE %s CASCADE", table)
		if _, err := db.Exec(context.Background(), stmt); err != nil {
			if strings.Contains(err.Error(), "does not exist") {
				continue
			}
			t.Fatalf("Failed to truncate %s: %v", table, err)
		}
	}

	seedSystemAccounts(t, db)
	return db
}

func seedSystemAccounts(t *testing.T, db *pgxpool.Pool) {
	t.Helper()

	columns := "id, user_id, currency, balance, created_at"
	values := "('22222222-2222-2222-2222-222222222222', '11111111-1111-1111-1111-111111111111', 'USD', 0, NOW())," +
		"('33333333-3333-3333-3333-333333333333', '11111111-1111-1111-1111-111111111111', 'EUR', 0, NOW())," +
		"('44444444-4444-4444-4444-444444444444', '11111111-1111-1111-1111-111111111111', 'GBP', 0, NOW())"
	if hasLockedMicrosColumn(db) {
		columns = "id, user_id, currency, balance, locked_micros, created_at"
		values = "('22222222-2222-2222-2222-222222222222', '11111111-1111-1111-1111-111111111111', 'USD', 0, 0, NOW())," +
			"('33333333-3333-3333-3333-333333333333', '11111111-1111-1111-1111-111111111111', 'EUR', 0, 0, NOW())," +
			"('44444444-4444-4444-4444-444444444444', '11111111-1111-1111-1111-111111111111', 'GBP', 0, 0, NOW())"
	}

	sql := fmt.Sprintf(`
		INSERT INTO users (id, username, email, role, created_at)
		VALUES ('11111111-1111-1111-1111-111111111111', 'system_liquidity', 'system@grey.finance', 'system', NOW())
		ON CONFLICT DO NOTHING;

		INSERT INTO accounts (%s)
		VALUES %s
		ON CONFLICT (id) DO NOTHING;
	`, columns, values)
	if _, err := db.Exec(context.Background(), sql); err != nil {
		t.Fatalf("Failed to re-seed system accounts: %v", err)
	}
}

func hasLockedMicrosColumn(db *pgxpool.Pool) bool {
	row := db.QueryRow(context.Background(), `
		SELECT 1
		FROM information_schema.columns
		WHERE table_name = 'accounts' AND column_name = 'locked_micros'
	`)
	var tmp int
	return row.Scan(&tmp) == nil
}

func ensurePayoutsTable(t *testing.T, db *pgxpool.Pool) {
	t.Helper()

	sql := `
		CREATE TABLE IF NOT EXISTS payouts (
			id UUID PRIMARY KEY,
			transaction_id UUID NOT NULL,
			account_id UUID NOT NULL,
			amount_micros BIGINT NOT NULL,
			currency TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'PENDING',
			gateway_ref TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
	`
	if _, err := db.Exec(context.Background(), sql); err != nil {
		t.Fatalf("failed to ensure payouts table: %v", err)
	}
}

func ensureAuditLogTable(t *testing.T, db *pgxpool.Pool) {
	t.Helper()

	sql := `
		CREATE TABLE IF NOT EXISTS audit_log (
			id BIGSERIAL PRIMARY KEY,
			entity_type TEXT NOT NULL,
			entity_id UUID NOT NULL,
			actor_id UUID,
			action TEXT NOT NULL,
			prev_state TEXT,
			next_state TEXT,
			metadata JSONB,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
	`
	if _, err := db.Exec(context.Background(), sql); err != nil {
		t.Fatalf("failed to ensure audit_log table: %v", err)
	}
}

func ensureLockedMicrosColumn(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	if hasLockedMicrosColumn(db) {
		return
	}
	if _, err := db.Exec(context.Background(), "ALTER TABLE accounts ADD COLUMN locked_micros BIGINT NOT NULL DEFAULT 0"); err != nil {
		t.Fatalf("failed to add locked_micros column: %v", err)
	}
}
