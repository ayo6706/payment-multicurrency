package service

import (
	"context"
	"os"
	"testing"

	"github.com/ayo6706/payment-multicurrency/internal/models"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestDB is a helper to connect to the DB and clean it up.
// NOTE: This assumes a running Postgres instance via docker-compose on localhost:5432.
func setupTestDB(t *testing.T) *pgxpool.Pool {
	connString := os.Getenv("DATABASE_URL")
	if connString == "" {
		connString = "postgres://user:password@localhost:5432/payment_system?sslmode=disable"
	}
	db, err := pgxpool.New(context.Background(), connString)
	if err != nil {
		t.Fatalf("Failed to connect to DB: %v", err)
	}

	// Clean up tables
	_, err = db.Exec(context.Background(), "TRUNCATE TABLE entries, transactions, accounts, users CASCADE")
	if err != nil {
		t.Fatalf("Failed to truncate tables: %v", err)
	}

	// Re-seed System Liquidity Accounts
	_, err = db.Exec(context.Background(), `
		INSERT INTO users (id, username, email, role, created_at)
		VALUES ('11111111-1111-1111-1111-111111111111', 'system_liquidity', 'system@grey.finance', 'system', NOW())
		ON CONFLICT DO NOTHING;

		INSERT INTO accounts (id, user_id, currency, balance, created_at)
		VALUES
		('22222222-2222-2222-2222-222222222222', '11111111-1111-1111-1111-111111111111', 'USD', 0, NOW()),
		('33333333-3333-3333-3333-333333333333', '11111111-1111-1111-1111-111111111111', 'EUR', 0, NOW()),
		('44444444-4444-4444-4444-444444444444', '11111111-1111-1111-1111-111111111111', 'GBP', 0, NOW())
		ON CONFLICT DO NOTHING;
	`)
	if err != nil {
		t.Fatalf("Failed to re-seed system accounts: %v", err)
	}

	return db
}

func TestTransfer(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := repository.NewRepository(db)
	svc := NewTransferService(repo, db, NewMockExchangeRateService())

	ctx := context.Background()

	// 1. Setup Ayo and David
	ayo := &models.User{
		ID:       uuid.New(),
		Username: "ayo",
		Email:    "ayo@example.com",
	}
	err := repo.CreateUser(ctx, ayo)
	require.NoError(t, err)

	david := &models.User{
		ID:       uuid.New(),
		Username: "david",
		Email:    "david@example.com",
	}
	err = repo.CreateUser(ctx, david)
	require.NoError(t, err)

	// 2. Setup Accounts (Ayo has $100, David has $0)
	ayoAcc := &models.Account{
		ID:       uuid.New(),
		UserID:   ayo.ID,
		Currency: "USD",
		Balance:  100,
	}
	err = repo.CreateAccount(ctx, ayoAcc)
	require.NoError(t, err)

	davidAcc := &models.Account{
		ID:       uuid.New(),
		UserID:   david.ID,
		Currency: "USD",
		Balance:  0,
	}
	err = repo.CreateAccount(ctx, davidAcc)
	require.NoError(t, err)

	// 3. Perform Transfer: Ayo sends $50 to David
	amount := int64(50)
	_, err = svc.Transfer(ctx, ayoAcc.ID, davidAcc.ID, amount, "ref-123")
	require.NoError(t, err)

	// 4. Verify Balances
	var ayoBalance int64
	err = db.QueryRow(ctx, "SELECT balance FROM accounts WHERE id = $1", ayoAcc.ID).Scan(&ayoBalance)
	require.NoError(t, err)
	assert.Equal(t, int64(50), ayoBalance) // Should be 50 after transfer

	var davidBalance int64
	err = db.QueryRow(ctx, "SELECT balance FROM accounts WHERE id = $1", davidAcc.ID).Scan(&davidBalance)
	require.NoError(t, err)
	assert.Equal(t, int64(50), davidBalance) // Should be 50 after transfer
}
func TestTransferDeadlock(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := repository.NewRepository(db)
	svc := NewTransferService(repo, db, NewMockExchangeRateService())

	ctx := context.Background()

	// 1. Setup Ayo and David
	ayo := &models.User{ID: uuid.New(), Username: "ayo", Email: "ayo@example.com"}
	err := repo.CreateUser(ctx, ayo)
	require.NoError(t, err)

	david := &models.User{ID: uuid.New(), Username: "david", Email: "david@example.com"}
	err = repo.CreateUser(ctx, david)
	require.NoError(t, err)

	// 2. Setup Accounts with $100 each
	ayoAcc := &models.Account{ID: uuid.New(), UserID: ayo.ID, Currency: "USD", Balance: 100}
	err = repo.CreateAccount(ctx, ayoAcc)
	require.NoError(t, err)

	davidAcc := &models.Account{ID: uuid.New(), UserID: david.ID, Currency: "USD", Balance: 100}
	err = repo.CreateAccount(ctx, davidAcc)
	require.NoError(t, err)

	// 3. Perform concurrent transfers: Ayo -> David and David -> Ayo
	n := 10
	amount := int64(10)
	errs := make(chan error, n*2)

	for i := 0; i < n; i++ {
		go func(idx int) {
			_, err := svc.Transfer(ctx, ayoAcc.ID, davidAcc.ID, amount, uuid.New().String())
			errs <- err
		}(i)
		go func(idx int) {
			_, err := svc.Transfer(ctx, davidAcc.ID, ayoAcc.ID, amount, uuid.New().String())
			errs <- err
		}(i)
	}

	// 4. Wait for all to complete
	for i := 0; i < n*2; i++ {
		err := <-errs
		assert.NoError(t, err)
	}

	// 5. Verify Balances (should still be $100 each if n transfers each way)
	var ayoBalance int64
	err = db.QueryRow(ctx, "SELECT balance FROM accounts WHERE id = $1", ayoAcc.ID).Scan(&ayoBalance)
	require.NoError(t, err)
	assert.Equal(t, int64(100), ayoBalance)

	var davidBalance int64
	err = db.QueryRow(ctx, "SELECT balance FROM accounts WHERE id = $1", davidAcc.ID).Scan(&davidBalance)
	require.NoError(t, err)
	assert.Equal(t, int64(100), davidBalance)
}

func TestTransferExchange(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := repository.NewRepository(db)
	svc := NewTransferService(repo, db, NewMockExchangeRateService())

	ctx := context.Background()

	// 1. Setup Ayo (USD) and David (EUR)
	ayo := &models.User{
		ID:       uuid.New(),
		Username: "ayo",
		Email:    "ayo@example.com",
	}
	err := repo.CreateUser(ctx, ayo)
	require.NoError(t, err)

	david := &models.User{
		ID:       uuid.New(),
		Username: "david",
		Email:    "david@example.com",
	}
	err = repo.CreateUser(ctx, david)
	require.NoError(t, err)

	// 2. Setup Accounts
	// Ayo has 100 USD (100_000_000 micros)
	ayoAcc := &models.Account{
		ID:       uuid.New(),
		UserID:   ayo.ID,
		Currency: "USD",
		Balance:  100_000_000,
	}
	err = repo.CreateAccount(ctx, ayoAcc)
	require.NoError(t, err)

	// David has 0 EUR
	davidAcc := &models.Account{
		ID:       uuid.New(),
		UserID:   david.ID,
		Currency: "EUR",
		Balance:  0,
	}
	err = repo.CreateAccount(ctx, davidAcc)
	require.NoError(t, err)

	// 3. Perform FX Transfer: Ayo sends 100 USD -> David (EUR)
	// Rate is 0.92, so David should get 92 EUR
	cmd := TransferExchangeCmd{
		FromAccountID: ayoAcc.ID,
		ToAccountID:   davidAcc.ID,
		Amount:        100_000_000,
		FromCurrency:  "USD",
		ToCurrency:    "EUR",
		ReferenceID:   "ref-fx-123",
	}

	tx, err := svc.TransferExchange(ctx, cmd)
	require.NoError(t, err)
	assert.Equal(t, "exchange", tx.Type)
	assert.Equal(t, int64(100_000_000), tx.Amount)

	// 4. Verify Balances
	// Ayo: 0 USD
	var ayoBalance int64
	err = db.QueryRow(ctx, "SELECT balance FROM accounts WHERE id = $1", ayoAcc.ID).Scan(&ayoBalance)
	require.NoError(t, err)
	assert.Equal(t, int64(0), ayoBalance)

	// David: 92 EUR (92_000_000 micros)
	var davidBalance int64
	err = db.QueryRow(ctx, "SELECT balance FROM accounts WHERE id = $1", davidAcc.ID).Scan(&davidBalance)
	require.NoError(t, err)
	assert.Equal(t, int64(92_000_000), davidBalance)

	// 5. Verify System Liquidity Balances (Using fixed IDs from migration 000003)
	// System USD: +100 USD
	var sysUSDBalance int64
	err = db.QueryRow(ctx, "SELECT balance FROM accounts WHERE id = '22222222-2222-2222-2222-222222222222'").Scan(&sysUSDBalance)
	require.NoError(t, err)
	// Note: Initial balance was 0.
	assert.Equal(t, int64(100_000_000), sysUSDBalance)

	// System EUR: -92 EUR
	var sysEURBalance int64
	err = db.QueryRow(ctx, "SELECT balance FROM accounts WHERE id = '33333333-3333-3333-3333-333333333333'").Scan(&sysEURBalance)
	require.NoError(t, err)
	assert.Equal(t, int64(-92_000_000), sysEURBalance)
}
