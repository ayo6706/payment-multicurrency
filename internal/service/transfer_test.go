package service

import (
	"context"
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
	connString := "postgres://user:password@localhost:5432/payment_system?sslmode=disable"
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

	// 1. Setup Alice and Bob
	alice := &models.User{
		ID:       uuid.New(),
		Username: "alice",
		Email:    "alice@example.com",
	}
	err := repo.CreateUser(ctx, alice)
	require.NoError(t, err)

	bob := &models.User{
		ID:       uuid.New(),
		Username: "bob",
		Email:    "bob@example.com",
	}
	err = repo.CreateUser(ctx, bob)
	require.NoError(t, err)

	// 2. Setup Accounts (Alice has $100, Bob has $0)
	aliceAcc := &models.Account{
		ID:       uuid.New(),
		UserID:   alice.ID,
		Currency: "USD",
		Balance:  100,
	}
	err = repo.CreateAccount(ctx, aliceAcc)
	require.NoError(t, err)

	bobAcc := &models.Account{
		ID:       uuid.New(),
		UserID:   bob.ID,
		Currency: "USD",
		Balance:  0,
	}
	err = repo.CreateAccount(ctx, bobAcc)
	require.NoError(t, err)

	// 3. Perform Transfer: Alice sends $50 to Bob
	amount := int64(50)
	err = svc.Transfer(ctx, aliceAcc.ID, bobAcc.ID, amount, "ref-123")
	require.NoError(t, err)

	// 4. Verify Balances
	var aliceBalance int64
	err = db.QueryRow(ctx, "SELECT balance FROM accounts WHERE id = $1", aliceAcc.ID).Scan(&aliceBalance)
	require.NoError(t, err)
	assert.Equal(t, int64(50), aliceBalance) // Should be 50 after transfer

	var bobBalance int64
	err = db.QueryRow(ctx, "SELECT balance FROM accounts WHERE id = $1", bobAcc.ID).Scan(&bobBalance)
	require.NoError(t, err)
	assert.Equal(t, int64(50), bobBalance) // Should be 50 after transfer
}
func TestTransferDeadlock(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := repository.NewRepository(db)
	svc := NewTransferService(repo, db)

	ctx := context.Background()

	// 1. Setup Alice and Bob
	alice := &models.User{ID: uuid.New(), Username: "alice", Email: "alice@example.com"}
	err := repo.CreateUser(ctx, alice)
	require.NoError(t, err)

	bob := &models.User{ID: uuid.New(), Username: "bob", Email: "bob@example.com"}
	err = repo.CreateUser(ctx, bob)
	require.NoError(t, err)

	// 2. Setup Accounts with $100 each
	aliceAcc := &models.Account{ID: uuid.New(), UserID: alice.ID, Currency: "USD", Balance: 100}
	err = repo.CreateAccount(ctx, aliceAcc)
	require.NoError(t, err)

	bobAcc := &models.Account{ID: uuid.New(), UserID: bob.ID, Currency: "USD", Balance: 100}
	err = repo.CreateAccount(ctx, bobAcc)
	require.NoError(t, err)

	// 3. Perform concurrent transfers: Alice -> Bob and Bob -> Alice
	n := 10
	amount := int64(10)
	errs := make(chan error, n*2)

	for i := 0; i < n; i++ {
		go func(idx int) {
			errs <- svc.Transfer(ctx, aliceAcc.ID, bobAcc.ID, amount, uuid.New().String())
		}(i)
		go func(idx int) {
			errs <- svc.Transfer(ctx, bobAcc.ID, aliceAcc.ID, amount, uuid.New().String())
		}(i)
	}

	// 4. Wait for all to complete
	for i := 0; i < n*2; i++ {
		err := <-errs
		assert.NoError(t, err)
	}

	// 5. Verify Balances (should still be $100 each if n transfers each way)
	var aliceBalance int64
	err = db.QueryRow(ctx, "SELECT balance FROM accounts WHERE id = $1", aliceAcc.ID).Scan(&aliceBalance)
	require.NoError(t, err)
	assert.Equal(t, int64(100), aliceBalance)

	var bobBalance int64
	err = db.QueryRow(ctx, "SELECT balance FROM accounts WHERE id = $1", bobAcc.ID).Scan(&bobBalance)
	require.NoError(t, err)
	assert.Equal(t, int64(100), bobBalance)
}
