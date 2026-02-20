package service

import (
	"context"
	"errors"
	"testing"

	"github.com/ayo6706/payment-multicurrency/internal/models"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type panicStore struct{}

func (panicStore) Queries() *repository.Queries {
	panic("unexpected Queries call")
}

func (panicStore) RunInTx(ctx context.Context, fn func(q *repository.Queries) error) error {
	return errors.New("unexpected RunInTx call")
}

func TestTransferValidation(t *testing.T) {
	svc := NewTransferService(panicStore{}, NewMockExchangeRateService())
	aid := uuid.New()
	bid := uuid.New()

	cases := []struct {
		name        string
		fromID      uuid.UUID
		toID        uuid.UUID
		amount      int64
		referenceID string
	}{
		{name: "non_positive_amount", fromID: aid, toID: bid, amount: 0, referenceID: "ref"},
		{name: "missing_reference", fromID: aid, toID: bid, amount: 1, referenceID: ""},
		{name: "same_account", fromID: aid, toID: aid, amount: 1, referenceID: "ref"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Transfer(context.Background(), tc.fromID, tc.toID, tc.amount, tc.referenceID)
			require.Error(t, err)
		})
	}
}

func TestTransferExchangeValidation(t *testing.T) {
	svc := NewTransferService(panicStore{}, NewMockExchangeRateService())
	aid := uuid.New()
	bid := uuid.New()

	cases := []struct {
		name string
		cmd  TransferExchangeCmd
	}{
		{name: "non_positive_amount", cmd: TransferExchangeCmd{FromAccountID: aid, ToAccountID: bid, Amount: 0, ReferenceID: "r", FromCurrency: "USD", ToCurrency: "EUR"}},
		{name: "missing_reference", cmd: TransferExchangeCmd{FromAccountID: aid, ToAccountID: bid, Amount: 1, ReferenceID: "", FromCurrency: "USD", ToCurrency: "EUR"}},
		{name: "same_account", cmd: TransferExchangeCmd{FromAccountID: aid, ToAccountID: aid, Amount: 1, ReferenceID: "r", FromCurrency: "USD", ToCurrency: "EUR"}},
		{name: "same_currency", cmd: TransferExchangeCmd{FromAccountID: aid, ToAccountID: bid, Amount: 1, ReferenceID: "r", FromCurrency: "USD", ToCurrency: "USD"}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.TransferExchange(context.Background(), tc.cmd)
			require.Error(t, err)
		})
	}
}

func TestTransfer(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := repository.NewRepository(db)
	store := repository.NewStore(db)
	svc := NewTransferService(store, NewMockExchangeRateService())

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
	ayoAccDb, err := repo.GetAccount(ctx, ayoAcc.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(50), ayoAccDb.Balance) // Should be 50 after transfer

	davidAccDb, err := repo.GetAccount(ctx, davidAcc.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(50), davidAccDb.Balance) // Should be 50 after transfer
}
func TestTransferDeadlock(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := repository.NewRepository(db)
	store := repository.NewStore(db)
	svc := NewTransferService(store, NewMockExchangeRateService())

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
	ayoAccDb, err := repo.GetAccount(ctx, ayoAcc.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(100), ayoAccDb.Balance)

	davidAccDb, err := repo.GetAccount(ctx, davidAcc.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(100), davidAccDb.Balance)
}

func TestTransferExchange(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := repository.NewRepository(db)
	store := repository.NewStore(db)
	svc := NewTransferService(store, NewMockExchangeRateService())

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
	ayoAccDb, err := repo.GetAccount(ctx, ayoAcc.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), ayoAccDb.Balance)

	// David: 92 EUR (92_000_000 micros)
	davidAccDb, err := repo.GetAccount(ctx, davidAcc.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(92_000_000), davidAccDb.Balance)

	// 5. Verify System Liquidity Balances (Using fixed IDs from migration 000003)
	sysUSDID, _ := uuid.Parse("22222222-2222-2222-2222-222222222222")
	sysUSDDb, err := repo.GetAccount(ctx, sysUSDID)
	require.NoError(t, err)
	// Note: Initial balance was 0.
	assert.Equal(t, int64(100_000_000), sysUSDDb.Balance)

	// System EUR: -92 EUR
	sysEURID, _ := uuid.Parse("33333333-3333-3333-3333-333333333333")
	sysEURDb, err := repo.GetAccount(ctx, sysEURID)
	require.NoError(t, err)
	assert.Equal(t, int64(-92_000_000), sysEURDb.Balance)
}
