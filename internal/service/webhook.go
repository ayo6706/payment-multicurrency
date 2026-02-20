package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ayo6706/payment-multicurrency/internal/domain"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WebhookService handles incoming webhook events from external systems.
type WebhookService struct {
	repo     *repository.Repository
	db       *pgxpool.Pool
	hmacKey  []byte
	systemID uuid.UUID
	skipSig  bool
}

// NewWebhookService creates a new WebhookService instance.
func NewWebhookService(repo *repository.Repository, db *pgxpool.Pool, hmacKey string, skipSignature bool) *WebhookService {
	return &WebhookService{
		repo:     repo,
		db:       db,
		hmacKey:  []byte(hmacKey),
		systemID: uuid.MustParse(domain.SystemUserID),
		skipSig:  skipSignature,
	}
}

// DepositWebhookPayload represents the incoming deposit webhook payload.
type DepositWebhookPayload struct {
	AccountID    string `json:"account_id"`
	AmountMicros int64  `json:"amount_micros"`
	Currency     string `json:"currency"`
	Reference    string `json:"reference"` // Unique reference from external system
}

// DepositWebhookResponse represents the response to a deposit webhook.
type DepositWebhookResponse struct {
	TransactionID uuid.UUID `json:"transaction_id"`
	Status        string    `json:"status"`
	Message       string    `json:"message"`
}

// HandleDepositWebhook processes an incoming deposit webhook.
// It verifies the HMAC signature, creates a transaction, updates the user balance,
// and writes double-entry ledger entries (System DEBIT, User CREDIT).
func (s *WebhookService) HandleDepositWebhook(ctx context.Context, payload []byte, signature string) (*DepositWebhookResponse, error) {
	// 1. Verify HMAC signature
	if !s.verifyHMAC(payload, signature) {
		return nil, errors.New("invalid signature")
	}

	// 2. Parse payload
	var deposit DepositWebhookPayload
	if err := json.Unmarshal(payload, &deposit); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	// Validate payload
	if deposit.AmountMicros <= 0 {
		return nil, fmt.Errorf("invalid amount: %d", deposit.AmountMicros)
	}
	if deposit.Reference == "" {
		return nil, errors.New("reference is required")
	}
	if deposit.AccountID == "" {
		return nil, errors.New("account_id is required")
	}

	// Validate currency
	if !isValidCurrency(deposit.Currency) {
		return nil, fmt.Errorf("unsupported currency: %s", deposit.Currency)
	}

	accountID, err := uuid.Parse(deposit.AccountID)
	if err != nil {
		return nil, fmt.Errorf("invalid account_id: %w", err)
	}

	queries := repository.New(s.db)

	// Check idempotency using the reference
	existingTxRow, err := queries.CheckTransactionIdempotency(ctx, deposit.Reference)
	if err == nil {
		// Already processed
		return &DepositWebhookResponse{
			TransactionID: repository.FromPgUUID(existingTxRow.ID),
			Status:        existingTxRow.Status,
			Message:       "Deposit already processed",
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("failed to check idempotency: %w", err)
	}

	// Get system account for this currency
	systemAccountID, err := getSystemAccountID(deposit.Currency)
	if err != nil {
		return nil, fmt.Errorf("failed to get system account: %w", err)
	}

	// Begin transaction
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := queries.WithTx(tx)

	// Lock user account and verify currency
	accountRow, err := qtx.GetAccountBalanceAndLocked(ctx, repository.ToPgUUID(accountID))
	if err != nil {
		return nil, fmt.Errorf("failed to lock account: %w", err)
	}

	if accountRow.Currency != deposit.Currency {
		return nil, fmt.Errorf("currency mismatch: account is %s, deposit is %s", accountRow.Currency, deposit.Currency)
	}

	// Create transaction record
	transactionID := uuid.New()
	metadataJson, err := json.Marshal(map[string]string{
		"webhook_reference": deposit.Reference,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to encode metadata: %w", err)
	}

	_, err = qtx.CreateTransaction(ctx, repository.CreateTransactionParams{
		ID:          repository.ToPgUUID(transactionID),
		Amount:      deposit.AmountMicros,
		Currency:    deposit.Currency,
		Type:        domain.TxTypeDeposit,
		Status:      domain.TxStatusCompleted,
		ReferenceID: deposit.Reference,
		Metadata:    metadataJson,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create transaction: %w", err)
	}

	// Create double-entry ledger entries
	// Entry 1: Debit System (system account is debited for the deposit)
	_, err = qtx.CreateEntry(ctx, repository.CreateEntryParams{
		ID:            repository.ToPgUUID(uuid.New()),
		TransactionID: repository.ToPgUUID(transactionID),
		AccountID:     repository.ToPgUUID(systemAccountID),
		Amount:        deposit.AmountMicros,
		Direction:     domain.DirectionDebit,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create system debit entry: %w", err)
	}

	// Entry 2: Credit User (user account receives the deposit)
	_, err = qtx.CreateEntry(ctx, repository.CreateEntryParams{
		ID:            repository.ToPgUUID(uuid.New()),
		TransactionID: repository.ToPgUUID(transactionID),
		AccountID:     repository.ToPgUUID(accountID),
		Amount:        deposit.AmountMicros,
		Direction:     domain.DirectionCredit,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create user credit entry: %w", err)
	}

	// Update user balance
	err = qtx.UpdateAccountBalance(ctx, repository.UpdateAccountBalanceParams{
		Balance: deposit.AmountMicros,
		ID:      repository.ToPgUUID(accountID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update user balance: %w", err)
	}

	// Mirror the system ledger debit on the accounts table
	err = qtx.UpdateAccountBalance(ctx, repository.UpdateAccountBalanceParams{
		Balance: -deposit.AmountMicros,
		ID:      repository.ToPgUUID(systemAccountID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update system balance: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &DepositWebhookResponse{
		TransactionID: transactionID,
		Status:        domain.TxStatusCompleted,
		Message:       "Deposit processed successfully",
	}, nil
}

// verifyHMAC verifies the HMAC signature of the payload.
func (s *WebhookService) verifyHMAC(payload []byte, signature string) bool {
	if s.skipSig || len(s.hmacKey) == 0 {
		return true
	}

	// Calculate expected HMAC
	h := hmac.New(sha256.New, s.hmacKey)
	h.Write(payload)
	expectedSig := "sha256=" + hex.EncodeToString(h.Sum(nil))

	// Use hmac.Equal for constant-time comparison
	return hmac.Equal([]byte(signature), []byte(expectedSig))
}

// isValidCurrency checks if the currency is supported.
func isValidCurrency(currency string) bool {
	switch currency {
	case "USD", "EUR", "GBP":
		return true
	default:
		return false
	}
}
