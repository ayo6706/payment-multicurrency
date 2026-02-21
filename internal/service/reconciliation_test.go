package service

import (
	"context"
	"testing"

	"github.com/ayo6706/payment-multicurrency/internal/domain"
	"github.com/ayo6706/payment-multicurrency/internal/models"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestReconciliationRun(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	repoSvc := repository.NewRepository(db)
	queries := repository.New(db)
	reconcileSvc := NewReconciliationService(repository.NewStore(db))

	userA := &models.User{ID: uuid.New(), Username: "rec-a", Email: "rec-a@example.com"}
	userB := &models.User{ID: uuid.New(), Username: "rec-b", Email: "rec-b@example.com"}
	require.NoError(t, repoSvc.CreateUser(ctx, userA))
	require.NoError(t, repoSvc.CreateUser(ctx, userB))

	accountA := &models.Account{ID: uuid.New(), UserID: userA.ID, Currency: "USD", Balance: 1_000_000}
	accountB := &models.Account{ID: uuid.New(), UserID: userB.ID, Currency: "USD", Balance: 0}
	require.NoError(t, repoSvc.CreateAccount(ctx, accountA))
	require.NoError(t, repoSvc.CreateAccount(ctx, accountB))

	transactionID := uuid.New()
	_, err := queries.CreateTransaction(ctx, repository.CreateTransactionParams{
		ID:          repository.ToPgUUID(transactionID),
		Amount:      100_000,
		Currency:    "USD",
		Type:        domain.TxTypeTransfer,
		Status:      domain.TxStatusCompleted,
		ReferenceID: "rec-balance",
	})
	require.NoError(t, err)

	_, err = queries.CreateEntry(ctx, repository.CreateEntryParams{
		ID:            repository.ToPgUUID(uuid.New()),
		TransactionID: repository.ToPgUUID(transactionID),
		AccountID:     repository.ToPgUUID(accountA.ID),
		Amount:        100_000,
		Direction:     domain.DirectionDebit,
	})
	require.NoError(t, err)

	_, err = queries.CreateEntry(ctx, repository.CreateEntryParams{
		ID:            repository.ToPgUUID(uuid.New()),
		TransactionID: repository.ToPgUUID(transactionID),
		AccountID:     repository.ToPgUUID(accountB.ID),
		Amount:        100_000,
		Direction:     domain.DirectionCredit,
	})
	require.NoError(t, err)

	require.NoError(t, reconcileSvc.Run(ctx))
	net, err := queries.GetLedgerNet(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(0), net)

	_, err = db.Exec(ctx, "INSERT INTO entries (id, transaction_id, account_id, amount, direction, created_at) VALUES ($1,$2,$3,$4,$5,NOW())",
		repository.ToPgUUID(uuid.New()), repository.ToPgUUID(transactionID), repository.ToPgUUID(accountA.ID), int64(5_000), domain.DirectionCredit)
	require.NoError(t, err)

	require.NoError(t, reconcileSvc.Run(ctx))
	net, err = queries.GetLedgerNet(ctx)
	require.NoError(t, err)
	require.NotEqual(t, int64(0), net)
}
