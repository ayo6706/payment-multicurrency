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

	return db
}

func TestTransfer(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := repository.NewRepository(db)
	svc := NewTransferService(repo, db)

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
	svc := NewTransferService(repo, db)

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
