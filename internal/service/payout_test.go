package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ayo6706/payment-multicurrency/internal/domain"
	"github.com/ayo6706/payment-multicurrency/internal/models"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type stubGateway struct {
	ref string
	err error
}

func (s *stubGateway) SendPayout(ctx context.Context, destination string, amount int64, currency string) (string, error) {
	return s.ref, s.err
}

func TestPayoutDestinationValidate(t *testing.T) {
	cases := []struct {
		name string
		in   PayoutDestinationInput
		ok   bool
	}{
		{
			name: "valid",
			in: PayoutDestinationInput{
				IBAN: "GB29NWBK60161331926819",
				Name: "John",
			},
			ok: true,
		},
		{name: "missing_iban", in: PayoutDestinationInput{Name: "John"}, ok: false},
		{name: "missing_name", in: PayoutDestinationInput{IBAN: "GB29NWBK60161331926819"}, ok: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := tc.in.Validate()
			if tc.ok {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
		})
	}
}

func TestPayoutProcessSuccess(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repoSvc := repository.NewRepository(db)
	store := repository.NewStore(db)
	gateway := &stubGateway{ref: "MOCK-REF"}
	payoutSvc := NewPayoutService(store, gateway)

	ctx := context.Background()

	user := &models.User{ID: uuid.New(), Username: "payout-user", Email: "payout@example.com"}
	require.NoError(t, repoSvc.CreateUser(ctx, user))

	account := &models.Account{ID: uuid.New(), UserID: user.ID, Currency: "USD", Balance: 2_000_000}
	require.NoError(t, repoSvc.CreateAccount(ctx, account))

	resp, err := payoutSvc.RequestPayout(ctx, RequestPayoutRequest{
		AccountID:    account.ID,
		AmountMicros: 500_000,
		Currency:     "USD",
		Destination:  PayoutDestinationInput{IBAN: "GB29NWBK60161331926819", Name: "John"},
		ReferenceID:  "req-success",
	})
	require.NoError(t, err)

	queries := repository.New(db)
	accRow, err := queries.GetAccountBalanceAndLocked(ctx, repository.ToPgUUID(account.ID))
	require.NoError(t, err)
	require.Equal(t, int64(500_000), accRow.LockedMicros)

	require.NoError(t, payoutSvc.ProcessPayouts(ctx, 5))

	payoutRow, err := queries.GetPayout(ctx, repository.ToPgUUID(resp.PayoutID))
	require.NoError(t, err)
	require.Equal(t, domain.PayoutStatusCompleted, payoutRow.Status)
	require.NotNil(t, payoutRow.GatewayRef)

	txRow, err := queries.GetTransaction(ctx, payoutRow.TransactionID)
	require.NoError(t, err)
	require.Equal(t, domain.TxStatusCompleted, txRow.Status)

	accRow, err = queries.GetAccountBalanceAndLocked(ctx, repository.ToPgUUID(account.ID))
	require.NoError(t, err)
	require.Equal(t, int64(1_500_000), accRow.Balance)
	require.Equal(t, int64(0), accRow.LockedMicros)

	systemAccountID, err := getSystemAccountID("USD")
	require.NoError(t, err)
	systemRow, err := queries.GetAccountBalanceAndLocked(ctx, repository.ToPgUUID(systemAccountID))
	require.NoError(t, err)
	require.Equal(t, int64(500_000), systemRow.Balance)
}

func TestPayoutProcessFailureReleasesFunds(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repoSvc := repository.NewRepository(db)
	store := repository.NewStore(db)
	gateway := &stubGateway{err: errors.New("gateway down")}
	payoutSvc := NewPayoutService(store, gateway)

	ctx := context.Background()

	user := &models.User{ID: uuid.New(), Username: "fail-user", Email: "fail@example.com"}
	require.NoError(t, repoSvc.CreateUser(ctx, user))

	account := &models.Account{ID: uuid.New(), UserID: user.ID, Currency: "USD", Balance: 1_000_000}
	require.NoError(t, repoSvc.CreateAccount(ctx, account))

	resp, err := payoutSvc.RequestPayout(ctx, RequestPayoutRequest{
		AccountID:    account.ID,
		AmountMicros: 250_000,
		Currency:     "USD",
		Destination:  PayoutDestinationInput{IBAN: "GB29NWBK60161331926819", Name: "John"},
		ReferenceID:  "req-fail",
	})
	require.NoError(t, err)

	require.NoError(t, payoutSvc.ProcessPayouts(ctx, 5))

	queries := repository.New(db)
	payoutRow, err := queries.GetPayout(ctx, repository.ToPgUUID(resp.PayoutID))
	require.NoError(t, err)
	require.Equal(t, domain.PayoutStatusFailed, payoutRow.Status)

	txRow, err := queries.GetTransaction(ctx, payoutRow.TransactionID)
	require.NoError(t, err)
	require.Equal(t, domain.TxStatusFailed, txRow.Status)

	accRow, err := queries.GetAccountBalanceAndLocked(ctx, repository.ToPgUUID(account.ID))
	require.NoError(t, err)
	require.Equal(t, int64(1_000_000), accRow.Balance)
	require.Equal(t, int64(0), accRow.LockedMicros)
}

func TestPayoutProcessRecoversStaleProcessing(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repoSvc := repository.NewRepository(db)
	store := repository.NewStore(db)
	gateway := &stubGateway{ref: "MOCK-REF-RECOVER"}
	payoutSvc := NewPayoutService(store, gateway)

	ctx := context.Background()

	user := &models.User{ID: uuid.New(), Username: "recover-user", Email: "recover@example.com"}
	require.NoError(t, repoSvc.CreateUser(ctx, user))

	account := &models.Account{ID: uuid.New(), UserID: user.ID, Currency: "USD", Balance: 2_000_000}
	require.NoError(t, repoSvc.CreateAccount(ctx, account))

	resp, err := payoutSvc.RequestPayout(ctx, RequestPayoutRequest{
		AccountID:    account.ID,
		AmountMicros: 300_000,
		Currency:     "USD",
		Destination:  PayoutDestinationInput{IBAN: "GB29NWBK60161331926819", Name: "John"},
		ReferenceID:  "req-recover",
	})
	require.NoError(t, err)

	queries := repository.New(db)
	payoutRow, err := queries.GetPayout(ctx, repository.ToPgUUID(resp.PayoutID))
	require.NoError(t, err)

	// Simulate a worker that claimed the payout and then crashed.
	require.NoError(t, queries.UpdatePayoutStatus(ctx, repository.UpdatePayoutStatusParams{
		Status:     domain.PayoutStatusProcessing,
		GatewayRef: nil,
		ID:         payoutRow.ID,
	}))
	require.NoError(t, queries.UpdateTransactionStatus(ctx, repository.UpdateTransactionStatusParams{
		Status: domain.TxStatusProcessing,
		ID:     payoutRow.TransactionID,
	}))
	_, err = db.Exec(ctx, "UPDATE payouts SET updated_at = $1 WHERE id = $2", time.Now().Add(-3*time.Minute), payoutRow.ID)
	require.NoError(t, err)

	require.NoError(t, payoutSvc.ProcessPayouts(ctx, 5))

	payoutRow, err = queries.GetPayout(ctx, repository.ToPgUUID(resp.PayoutID))
	require.NoError(t, err)
	require.Equal(t, domain.PayoutStatusCompleted, payoutRow.Status)
	require.NotNil(t, payoutRow.GatewayRef)
}

func TestUpdatePayoutFailedReleasesLockedFunds(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repoSvc := repository.NewRepository(db)
	store := repository.NewStore(db)
	gateway := &stubGateway{ref: "MOCK-REF"}
	payoutSvc := NewPayoutService(store, gateway)

	ctx := context.Background()
	user := &models.User{ID: uuid.New(), Username: "fallback-user", Email: "fallback@example.com"}
	require.NoError(t, repoSvc.CreateUser(ctx, user))
	account := &models.Account{ID: uuid.New(), UserID: user.ID, Currency: "USD", Balance: 1_000_000}
	require.NoError(t, repoSvc.CreateAccount(ctx, account))

	resp, err := payoutSvc.RequestPayout(ctx, RequestPayoutRequest{
		AccountID:    account.ID,
		AmountMicros: 200_000,
		Currency:     "USD",
		Destination:  PayoutDestinationInput{IBAN: "GB29NWBK60161331926819", Name: "John"},
		ReferenceID:  "req-fallback-release",
	})
	require.NoError(t, err)

	payoutSvc.updatePayoutFailed(ctx, resp.PayoutID, "forced fallback")

	queries := repository.New(db)
	payoutRow, err := queries.GetPayout(ctx, repository.ToPgUUID(resp.PayoutID))
	require.NoError(t, err)
	require.Equal(t, domain.PayoutStatusFailed, payoutRow.Status)

	txRow, err := queries.GetTransaction(ctx, payoutRow.TransactionID)
	require.NoError(t, err)
	require.Equal(t, domain.TxStatusFailed, txRow.Status)

	accRow, err := queries.GetAccountBalanceAndLocked(ctx, repository.ToPgUUID(account.ID))
	require.NoError(t, err)
	require.Equal(t, int64(0), accRow.LockedMicros)
	require.Equal(t, int64(1_000_000), accRow.Balance)
}
