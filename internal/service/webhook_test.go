package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

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
	svc := NewWebhookService(repoSvc, db, "secret", false)

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
	svc := NewWebhookService(repoSvc, db, "secret", false)

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

func signPayload(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
