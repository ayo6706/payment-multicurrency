package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ayo6706/payment-multicurrency/internal/domain"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrInvalidSignature       = errors.New("invalid signature")
	ErrDepositInProgress      = errors.New("deposit is still processing")
	ErrDepositPayloadMismatch = errors.New("deposit payload does not match existing reference")
)

// WebhookService handles incoming webhook events from external systems.
type WebhookService struct {
	store   QueryStore
	hmacKey []byte
	skipSig bool
	audit   *AuditService
}

// NewWebhookService creates a new WebhookService instance.
func NewWebhookService(store QueryStore, hmacKey string, skipSignature bool) *WebhookService {
	return &WebhookService{
		store:   store,
		hmacKey: []byte(hmacKey),
		skipSig: skipSignature,
		audit:   NewAuditService(store),
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
	if !s.verifyHMAC(payload, signature) {
		return nil, ErrInvalidSignature
	}

	var deposit DepositWebhookPayload
	if err := json.Unmarshal(payload, &deposit); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	deposit.Currency = strings.ToUpper(strings.TrimSpace(deposit.Currency))
	deposit.Reference = strings.TrimSpace(deposit.Reference)
	deposit.AccountID = strings.TrimSpace(deposit.AccountID)

	if deposit.AmountMicros <= 0 {
		return nil, fmt.Errorf("invalid amount: %d", deposit.AmountMicros)
	}
	if deposit.Reference == "" {
		return nil, errors.New("reference is required")
	}
	if deposit.AccountID == "" {
		return nil, errors.New("account_id is required")
	}

	if !isValidCurrency(deposit.Currency) {
		return nil, fmt.Errorf("unsupported currency: %s", deposit.Currency)
	}

	accountID, err := uuid.Parse(deposit.AccountID)
	if err != nil {
		return nil, fmt.Errorf("invalid account_id: %w", err)
	}

	queries := s.store.Queries()
	existingTxRow, err := queries.CheckTransactionIdempotency(ctx, deposit.Reference)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("failed to check idempotency: %w", err)
	}

	retryExisting := false
	transactionID := uuid.New()
	if err == nil {
		if existingTxRow.Type != domain.TxTypeDeposit || existingTxRow.Amount != deposit.AmountMicros || existingTxRow.Currency != deposit.Currency {
			return nil, ErrDepositPayloadMismatch
		}
		switch strings.ToUpper(existingTxRow.Status) {
		case domain.TxStatusCompleted:
			return &DepositWebhookResponse{
				TransactionID: repository.FromPgUUID(existingTxRow.ID),
				Status:        existingTxRow.Status,
				Message:       "Deposit already processed",
			}, nil
		case domain.TxStatusPending, domain.TxStatusProcessing:
			return nil, ErrDepositInProgress
		case domain.TxStatusFailed:
			retryExisting = true
			transactionID = repository.FromPgUUID(existingTxRow.ID)
		default:
			return nil, fmt.Errorf("existing reference in unsupported state: %s", existingTxRow.Status)
		}
	}

	systemAccountID, err := getSystemAccountID(deposit.Currency)
	if err != nil {
		return nil, fmt.Errorf("failed to get system account: %w", err)
	}

	metadataJson, err := json.Marshal(map[string]string{
		"webhook_reference": deposit.Reference,
		"account_id":        deposit.AccountID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to encode metadata: %w", err)
	}

	err = s.store.RunInTx(ctx, func(qtx *repository.Queries) error {
		accountRow, err := qtx.GetAccountBalanceAndLocked(ctx, repository.ToPgUUID(accountID))
		if err != nil {
			return fmt.Errorf("failed to lock account: %w", err)
		}

		if accountRow.Currency != deposit.Currency {
			return fmt.Errorf("currency mismatch: account is %s, deposit is %s", accountRow.Currency, deposit.Currency)
		}

		if retryExisting {
			if err := transitionTransactionState(ctx, qtx, s.audit, transactionID, domain.TxStatusProcessing, nil, "retry_processing_started", metadataJson); err != nil {
				return fmt.Errorf("failed to transition retry transaction: %w", err)
			}
		} else {
			_, err = qtx.CreateTransaction(ctx, repository.CreateTransactionParams{
				ID:          repository.ToPgUUID(transactionID),
				Amount:      deposit.AmountMicros,
				Currency:    deposit.Currency,
				Type:        domain.TxTypeDeposit,
				Status:      domain.TxStatusPending,
				ReferenceID: deposit.Reference,
				Metadata:    metadataJson,
			})
			if err != nil {
				return fmt.Errorf("failed to create transaction: %w", err)
			}
			if err := s.audit.Write(ctx, qtx, "transaction", transactionID, nil, "created", "", domain.TxStatusPending, metadataJson); err != nil {
				return err
			}
			if err := transitionTransactionState(ctx, qtx, s.audit, transactionID, domain.TxStatusProcessing, nil, "processing_started", nil); err != nil {
				return fmt.Errorf("failed to transition transaction to processing: %w", err)
			}
		}

		_, err = qtx.CreateEntry(ctx, repository.CreateEntryParams{
			ID:            repository.ToPgUUID(uuid.New()),
			TransactionID: repository.ToPgUUID(transactionID),
			AccountID:     repository.ToPgUUID(systemAccountID),
			Amount:        deposit.AmountMicros,
			Direction:     domain.DirectionDebit,
		})
		if err != nil {
			return fmt.Errorf("failed to create system debit entry: %w", err)
		}

		_, err = qtx.CreateEntry(ctx, repository.CreateEntryParams{
			ID:            repository.ToPgUUID(uuid.New()),
			TransactionID: repository.ToPgUUID(transactionID),
			AccountID:     repository.ToPgUUID(accountID),
			Amount:        deposit.AmountMicros,
			Direction:     domain.DirectionCredit,
		})
		if err != nil {
			return fmt.Errorf("failed to create user credit entry: %w", err)
		}

		rows, err := qtx.UpdateAccountBalance(ctx, repository.UpdateAccountBalanceParams{
			Balance: deposit.AmountMicros,
			ID:      repository.ToPgUUID(accountID),
		})
		if err != nil {
			return fmt.Errorf("failed to update user balance: %w", err)
		}
		if err := requireExactlyOne(rows, "credit deposit account"); err != nil {
			return err
		}

		rows, err = qtx.UpdateAccountBalance(ctx, repository.UpdateAccountBalanceParams{
			Balance: -deposit.AmountMicros,
			ID:      repository.ToPgUUID(systemAccountID),
		})
		if err != nil {
			return fmt.Errorf("failed to update system balance: %w", err)
		}
		if err := requireExactlyOne(rows, "debit system liquidity account"); err != nil {
			return err
		}
		if err := transitionTransactionState(ctx, qtx, s.audit, transactionID, domain.TxStatusCompleted, nil, "completed", nil); err != nil {
			return fmt.Errorf("failed to complete webhook transaction: %w", err)
		}
		return nil
	})
	if err != nil {
		if !retryExisting {
			failErr := s.transitionState(ctx, transactionID, domain.TxStatusFailed, "failed", []byte(`{"reason":"deposit_failed"}`))
			if failErr != nil && !errors.Is(failErr, pgx.ErrNoRows) {
				return nil, fmt.Errorf("deposit failed: %w (status update failed: %v)", err, failErr)
			}
		}
		return nil, err
	}
	return &DepositWebhookResponse{
		TransactionID: transactionID,
		Status:        domain.TxStatusCompleted,
		Message:       "Deposit processed successfully",
	}, nil
}

// verifyHMAC verifies the HMAC signature of the payload.
func (s *WebhookService) verifyHMAC(payload []byte, signature string) bool {
	if s.skipSig {
		return true
	}
	if len(s.hmacKey) == 0 {
		return false
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

func (s *WebhookService) transitionState(ctx context.Context, transactionID uuid.UUID, nextState, action string, metadata []byte) error {
	return s.store.RunInTx(ctx, func(qtx *repository.Queries) error {
		return transitionTransactionState(ctx, qtx, s.audit, transactionID, nextState, nil, action, metadata)
	})
}
