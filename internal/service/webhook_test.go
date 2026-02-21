package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ayo6706/payment-multicurrency/internal/domain"
	"github.com/ayo6706/payment-multicurrency/internal/models"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestHandleDepositWebhookUpdatesBalances(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repoSvc := repository.NewRepository(db)
	store := repository.NewStore(db)
	svc := NewWebhookService(store, "secret", false)

	ctx := context.Background()

	user := &models.User{ID: uuid.New(), Username: "deposit-user", Email: "deposit@example.com"}
	require.NoError(t, repoSvc.CreateUser(ctx, user))

	account := &models.Account{ID: uuid.New(), UserID: user.ID, Currency: "USD", Balance: 0}
	require.NoError(t, repoSvc.CreateAccount(ctx, account))

	payload := DepositWebhookPayload{
		AccountID:    account.ID.String(),
		AmountMicros: 750_000,
		Currency:     "USD",
		Reference:    "dep-1",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	signature := signPayload("secret", body)

	resp, err := svc.HandleDepositWebhook(ctx, body, signature)
	require.NoError(t, err)
	require.Equal(t, domain.TxStatusCompleted, resp.Status)

	queries := repository.New(db)
	txRow, err := queries.GetTransaction(ctx, repository.ToPgUUID(resp.TransactionID))
	require.NoError(t, err)
	require.Equal(t, domain.TxStatusCompleted, txRow.Status)
	auditRows, err := queries.GetAuditLogsByEntity(ctx, repository.GetAuditLogsByEntityParams{
		EntityType: "transaction",
		EntityID:   repository.ToPgUUID(resp.TransactionID),
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(auditRows), 3)
	require.Equal(t, "created", auditRows[0].Action)
	require.Equal(t, "processing_started", auditRows[1].Action)
	require.Equal(t, "completed", auditRows[2].Action)

	accRow, err := queries.GetAccountBalanceAndLocked(ctx, repository.ToPgUUID(account.ID))
	require.NoError(t, err)
	require.Equal(t, int64(750_000), accRow.Balance)

	systemAccountID, err := getSystemAccountID("USD")
	require.NoError(t, err)
	systemRow, err := queries.GetAccountBalanceAndLocked(ctx, repository.ToPgUUID(systemAccountID))
	require.NoError(t, err)
	require.Equal(t, int64(-750_000), systemRow.Balance)
}

func TestHandleDepositWebhookRejectsBadSignature(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repoSvc := repository.NewRepository(db)
	store := repository.NewStore(db)
	svc := NewWebhookService(store, "secret", false)

	ctx := context.Background()

	user := &models.User{ID: uuid.New(), Username: "sig-user", Email: "sig@example.com"}
	require.NoError(t, repoSvc.CreateUser(ctx, user))

	account := &models.Account{ID: uuid.New(), UserID: user.ID, Currency: "USD", Balance: 0}
	require.NoError(t, repoSvc.CreateAccount(ctx, account))

	payload := DepositWebhookPayload{
		AccountID:    account.ID.String(),
		AmountMicros: 100_000,
		Currency:     "USD",
		Reference:    "dep-2",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	_, err = svc.HandleDepositWebhook(ctx, body, "sha256=bad")
	require.Error(t, err)
}

func TestHandleDepositWebhookRejectsWhenHMACKeyMissing(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repoSvc := repository.NewRepository(db)
	store := repository.NewStore(db)
	svc := NewWebhookService(store, "", false)

	ctx := context.Background()

	user := &models.User{ID: uuid.New(), Username: "nosig-user", Email: "nosig@example.com"}
	require.NoError(t, repoSvc.CreateUser(ctx, user))

	account := &models.Account{ID: uuid.New(), UserID: user.ID, Currency: "USD", Balance: 0}
	require.NoError(t, repoSvc.CreateAccount(ctx, account))

	payload := DepositWebhookPayload{
		AccountID:    account.ID.String(),
		AmountMicros: 100_000,
		Currency:     "USD",
		Reference:    "dep-no-key",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	_, err = svc.HandleDepositWebhook(ctx, body, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid signature")
}

func TestHandleDepositWebhookRetriesFailedReference(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repoSvc := repository.NewRepository(db)
	store := repository.NewStore(db)
	svc := NewWebhookService(store, "secret", false)

	ctx := context.Background()

	user := &models.User{ID: uuid.New(), Username: "retry-user", Email: "retry@example.com"}
	require.NoError(t, repoSvc.CreateUser(ctx, user))

	account := &models.Account{ID: uuid.New(), UserID: user.ID, Currency: "USD", Balance: 0}
	require.NoError(t, repoSvc.CreateAccount(ctx, account))

	queries := repository.New(db)
	failedTxID := uuid.New()
	failedMeta, err := json.Marshal(map[string]string{
		"webhook_reference": "dep-failed-retry",
		"account_id":        account.ID.String(),
	})
	require.NoError(t, err)
	_, err = queries.CreateTransaction(ctx, repository.CreateTransactionParams{
		ID:          repository.ToPgUUID(failedTxID),
		Amount:      100_000,
		Currency:    "USD",
		Type:        domain.TxTypeDeposit,
		Status:      domain.TxStatusFailed,
		ReferenceID: "dep-failed-retry",
		Metadata:    failedMeta,
	})
	require.NoError(t, err)

	payload := DepositWebhookPayload{
		AccountID:    account.ID.String(),
		AmountMicros: 100_000,
		Currency:     "USD",
		Reference:    "dep-failed-retry",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	signature := signPayload("secret", body)
	resp, err := svc.HandleDepositWebhook(ctx, body, signature)
	require.NoError(t, err)
	require.Equal(t, failedTxID, resp.TransactionID)
	require.Equal(t, domain.TxStatusCompleted, resp.Status)

	accRow, err := queries.GetAccountBalanceAndLocked(ctx, repository.ToPgUUID(account.ID))
	require.NoError(t, err)
	require.Equal(t, int64(100_000), accRow.Balance)
}

func TestHandleDepositWebhookRejectsFailedRetryWithDifferentAccount(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repoSvc := repository.NewRepository(db)
	store := repository.NewStore(db)
	svc := NewWebhookService(store, "secret", false)

	ctx := context.Background()

	userA := &models.User{ID: uuid.New(), Username: "retry-user-a", Email: "retry-a@example.com"}
	require.NoError(t, repoSvc.CreateUser(ctx, userA))
	accountA := &models.Account{ID: uuid.New(), UserID: userA.ID, Currency: "USD", Balance: 0}
	require.NoError(t, repoSvc.CreateAccount(ctx, accountA))

	userB := &models.User{ID: uuid.New(), Username: "retry-user-b", Email: "retry-b@example.com"}
	require.NoError(t, repoSvc.CreateUser(ctx, userB))
	accountB := &models.Account{ID: uuid.New(), UserID: userB.ID, Currency: "USD", Balance: 0}
	require.NoError(t, repoSvc.CreateAccount(ctx, accountB))

	queries := repository.New(db)
	failedTxID := uuid.New()
	failedMeta, err := json.Marshal(map[string]string{
		"webhook_reference": "dep-failed-mismatch",
		"account_id":        accountA.ID.String(),
	})
	require.NoError(t, err)
	_, err = queries.CreateTransaction(ctx, repository.CreateTransactionParams{
		ID:          repository.ToPgUUID(failedTxID),
		Amount:      120_000,
		Currency:    "USD",
		Type:        domain.TxTypeDeposit,
		Status:      domain.TxStatusFailed,
		ReferenceID: "dep-failed-mismatch",
		Metadata:    failedMeta,
	})
	require.NoError(t, err)

	payload := DepositWebhookPayload{
		AccountID:    accountB.ID.String(),
		AmountMicros: 120_000,
		Currency:     "USD",
		Reference:    "dep-failed-mismatch",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	signature := signPayload("secret", body)
	_, err = svc.HandleDepositWebhook(ctx, body, signature)
	require.ErrorIs(t, err, ErrDepositPayloadMismatch)

	accARow, err := queries.GetAccountBalanceAndLocked(ctx, repository.ToPgUUID(accountA.ID))
	require.NoError(t, err)
	require.Equal(t, int64(0), accARow.Balance)

	accBRow, err := queries.GetAccountBalanceAndLocked(ctx, repository.ToPgUUID(accountB.ID))
	require.NoError(t, err)
	require.Equal(t, int64(0), accBRow.Balance)
}

func TestHandleDepositWebhookDuplicateInsertReturnsInProgress(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repoSvc := repository.NewRepository(db)
	store := repository.NewStore(db)
	svc := NewWebhookService(store, "secret", false)

	ctx := context.Background()

	user := &models.User{ID: uuid.New(), Username: "race-user", Email: "race@example.com"}
	require.NoError(t, repoSvc.CreateUser(ctx, user))
	account := &models.Account{ID: uuid.New(), UserID: user.ID, Currency: "USD", Balance: 0}
	require.NoError(t, repoSvc.CreateAccount(ctx, account))

	payload := DepositWebhookPayload{
		AccountID:    account.ID.String(),
		AmountMicros: 210_000,
		Currency:     "USD",
		Reference:    "dep-race-ref",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	signature := signPayload("secret", body)

	lockTx, err := db.Begin(ctx)
	require.NoError(t, err)
	defer lockTx.Rollback(ctx)
	_, err = lockTx.Exec(ctx, "SELECT id FROM accounts WHERE id = $1 FOR UPDATE", account.ID)
	require.NoError(t, err)

	resultCh := make(chan error, 1)
	go func() {
		_, callErr := svc.HandleDepositWebhook(ctx, body, signature)
		resultCh <- callErr
	}()

	time.Sleep(50 * time.Millisecond)

	queries := repository.New(db)
	dupMeta, err := json.Marshal(map[string]string{
		"webhook_reference": payload.Reference,
		"account_id":        account.ID.String(),
	})
	require.NoError(t, err)
	_, err = queries.CreateTransaction(ctx, repository.CreateTransactionParams{
		ID:          repository.ToPgUUID(uuid.New()),
		Amount:      payload.AmountMicros,
		Currency:    payload.Currency,
		Type:        domain.TxTypeDeposit,
		Status:      domain.TxStatusPending,
		ReferenceID: payload.Reference,
		Metadata:    dupMeta,
	})
	require.NoError(t, err)
	require.NoError(t, lockTx.Commit(ctx))

	select {
	case callErr := <-resultCh:
		require.True(t, errors.Is(callErr, ErrDepositInProgress), "expected ErrDepositInProgress, got: %v", callErr)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for webhook call")
	}
}

func signPayload(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
