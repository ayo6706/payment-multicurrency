package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ayo6706/payment-multicurrency/internal/domain"
	"github.com/ayo6706/payment-multicurrency/internal/gateway"
	"github.com/ayo6706/payment-multicurrency/internal/models"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// PayoutService handles business logic for external payouts.
type PayoutService struct {
	repo     *repository.Repository
	db       *pgxpool.Pool
	gateway  gateway.Gateway
	systemID uuid.UUID
}

// ErrPayoutNotFound indicates the requested payout does not exist.
var ErrPayoutNotFound = errors.New("payout not found")

// NewPayoutService creates a new PayoutService instance.
func NewPayoutService(repo *repository.Repository, db *pgxpool.Pool, gw gateway.Gateway) *PayoutService {
	return &PayoutService{
		repo:     repo,
		db:       db,
		gateway:  gw,
		systemID: uuid.MustParse(domain.SystemUserID),
	}
}

// PayoutDestinationInput represents the external destination payload expected from clients.
type PayoutDestinationInput struct {
	IBAN string `json:"iban"`
	Name string `json:"name"`
}

// Validate ensures the destination contains the required fields.
func (d PayoutDestinationInput) Validate() error {
	if d.IBAN == "" {
		return errors.New("destination.iban is required")
	}
	if d.Name == "" {
		return errors.New("destination.name is required")
	}
	return nil
}

// RequestPayoutRequest holds the parameters for creating a payout.
type RequestPayoutRequest struct {
	AccountID    uuid.UUID
	AmountMicros int64
	Currency     string
	Destination  PayoutDestinationInput
	ReferenceID  string
}

// PayoutResponse represents the response from a payout request.
type PayoutResponse struct {
	PayoutID  uuid.UUID `json:"payout_id"`
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	CreatedAt string    `json:"created_at,omitempty"`
}

// RequestPayout creates a new external payout request.
// It validates the balance, locks the funds, creates a transaction record,
// and creates a payout record. The payout will be processed asynchronously
// by the background worker.
//
// Returns 202 Accepted with the payout ID.
func (s *PayoutService) RequestPayout(ctx context.Context, req RequestPayoutRequest) (*PayoutResponse, error) {
	if req.AmountMicros <= 0 {
		return nil, fmt.Errorf("invalid amount: %d", req.AmountMicros)
	}
	if req.ReferenceID == "" {
		return nil, errors.New("reference_id is required")
	}
	if err := req.Destination.Validate(); err != nil {
		return nil, err
	}

	queries := repository.New(s.db)

	// Check idempotency
	existingTxRow, err := queries.CheckTransactionIdempotency(ctx, req.ReferenceID)
	if err == nil {
		// Return existing payout
		existingPayout, err := queries.GetPayoutByTransactionID(ctx, existingTxRow.ID)
		if err == nil {
			return &PayoutResponse{
				PayoutID: repository.FromPgUUID(existingPayout.ID),
				Status:   existingPayout.Status,
				Message:  "Payout already exists",
			}, nil
		}
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("failed to check idempotency: %w", err)
	}

	// Begin transaction
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := queries.WithTx(tx)

	// Lock the account and check balance
	accountRow, err := qtx.GetAccountBalanceAndLocked(ctx, repository.ToPgUUID(req.AccountID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("account not found: %w", models.ErrInsufficientFunds)
		}
		return nil, fmt.Errorf("failed to lock account: %w", err)
	}

	availableBalance := accountRow.Balance - accountRow.LockedMicros
	if availableBalance < req.AmountMicros {
		return nil, models.ErrInsufficientFunds
	}

	// Verify currency matches
	if accountRow.Currency != req.Currency {
		return nil, fmt.Errorf("currency mismatch: account is %s, requested %s", accountRow.Currency, req.Currency)
	}

	// Lock the funds
	err = qtx.LockAccountFunds(ctx, repository.LockAccountFundsParams{
		LockedMicros: req.AmountMicros,
		ID:           repository.ToPgUUID(req.AccountID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to lock funds: %w", err)
	}

	// Create transaction record
	transactionID := uuid.New()
	metadata, err := json.Marshal(map[string]any{
		"destination": req.Destination,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to encode metadata: %w", err)
	}

	_, err = qtx.CreateTransaction(ctx, repository.CreateTransactionParams{
		ID:          repository.ToPgUUID(transactionID),
		Amount:      req.AmountMicros,
		Currency:    req.Currency,
		Type:        domain.TxTypePayout,
		Status:      domain.TxStatusPending,
		ReferenceID: req.ReferenceID,
		Metadata:    metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create transaction: %w", err)
	}

	// Create payout record
	payoutID := uuid.New()
	_, err = qtx.InsertPayout(ctx, repository.InsertPayoutParams{
		ID:            repository.ToPgUUID(payoutID),
		TransactionID: repository.ToPgUUID(transactionID),
		AccountID:     repository.ToPgUUID(req.AccountID),
		AmountMicros:  req.AmountMicros,
		Currency:      req.Currency,
		Status:        domain.PayoutStatusPending,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create payout: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &PayoutResponse{
		PayoutID: payoutID,
		Status:   domain.PayoutStatusPending,
		Message:  "Payout queued for processing",
	}, nil
}

// ProcessPayouts processes a batch of pending payouts.
// It fetches pending payouts using SKIP LOCKED, calls the gateway,
// and updates the payout status and ledger accordingly.
//
// This method is safe for concurrent workers thanks to FOR UPDATE SKIP LOCKED.
func (s *PayoutService) ProcessPayouts(ctx context.Context, batchSize int32) error {
	claimed, err := s.claimPendingPayouts(ctx, batchSize)
	if err != nil {
		return err
	}
	if len(claimed) == 0 {
		return nil
	}

	queries := repository.New(s.db)
	for _, payout := range claimed {
		payoutID := repository.FromPgUUID(payout.ID)
		accountID := repository.FromPgUUID(payout.AccountID)

		txRow, err := queries.GetTransaction(ctx, payout.TransactionID)
		if err != nil {
			s.handlePayoutFailure(ctx, payoutID, accountID, payout.AmountMicros, "failed to fetch transaction metadata")
			continue
		}

		destination := extractDestination(txRow.Metadata)
		gatewayDestination := formatDestination(destination)
		gatewayRef, err := s.gateway.SendPayout(ctx, gatewayDestination, payout.AmountMicros, payout.Currency)
		if err != nil {
			s.handlePayoutFailure(ctx, payoutID, accountID, payout.AmountMicros, err.Error())
			continue
		}

		s.handlePayoutSuccess(ctx, payoutID, accountID, payout.AmountMicros, payout.Currency, payout.TransactionID, gatewayRef)
	}

	return nil
}

func (s *PayoutService) claimPendingPayouts(ctx context.Context, batchSize int32) ([]repository.Payout, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin claim tx: %w", err)
	}
	defer tx.Rollback(ctx)

	queries := repository.New(s.db).WithTx(tx)
	payouts, err := queries.GetPendingPayouts(ctx, batchSize)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pending payouts: %w", err)
	}

	for i, payout := range payouts {
		payouts[i].Status = domain.PayoutStatusProcessing
		err = queries.UpdatePayoutStatus(ctx, repository.UpdatePayoutStatusParams{
			Status:     domain.PayoutStatusProcessing,
			GatewayRef: nil,
			ID:         payout.ID,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to mark payout processing: %w", err)
		}

		err = queries.UpdateTransactionStatus(ctx, repository.UpdateTransactionStatusParams{
			Status: domain.TxStatusProcessing,
			ID:     payout.TransactionID,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to update transaction status: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit claim tx: %w", err)
	}
	return payouts, nil
}

// handlePayoutSuccess handles a successful payout from the gateway.
func (s *PayoutService) handlePayoutSuccess(ctx context.Context, payoutID, accountID uuid.UUID, amount int64, currency string, transactionID pgtype.UUID, gatewayRef string) {
	queries := repository.New(s.db)

	// Begin transaction
	tx, err := s.db.Begin(ctx)
	if err != nil {
		s.updatePayoutFailed(ctx, payoutID, "failed to begin success transaction")
		return
	}
	defer tx.Rollback(ctx)

	qtx := queries.WithTx(tx)

	// 1. Deduct locked funds from balance (debits both balance and locked_micros)
	err = qtx.DeductLockedFunds(ctx, repository.DeductLockedFundsParams{
		LockedMicros: amount,
		ID:           repository.ToPgUUID(accountID),
	})
	if err != nil {
		s.updatePayoutFailed(ctx, payoutID, "failed to deduct locked funds")
		return
	}

	// 2. Get system account for this currency
	systemAccountID, err := getSystemAccountID(currency)
	if err != nil {
		s.updatePayoutFailed(ctx, payoutID, "failed to get system account")
		return
	}

	// 3. Create double-entry ledger entries
	// Entry 1: Debit User (funds leaving the system)
	_, err = qtx.CreateEntry(ctx, repository.CreateEntryParams{
		ID:            repository.ToPgUUID(uuid.New()),
		TransactionID: transactionID,
		AccountID:     repository.ToPgUUID(accountID),
		Amount:        amount,
		Direction:     domain.DirectionDebit,
	})
	if err != nil {
		s.updatePayoutFailed(ctx, payoutID, "failed to create user debit entry")
		return
	}

	// Entry 2: Credit System (to balance the ledger - system account is credited)
	_, err = qtx.CreateEntry(ctx, repository.CreateEntryParams{
		ID:            repository.ToPgUUID(uuid.New()),
		TransactionID: transactionID,
		AccountID:     repository.ToPgUUID(systemAccountID),
		Amount:        amount,
		Direction:     domain.DirectionCredit,
	})
	if err != nil {
		s.updatePayoutFailed(ctx, payoutID, "failed to create system credit entry")
		return
	}

	// 4. Update system account balance to mirror the ledger credit
	err = qtx.UpdateAccountBalance(ctx, repository.UpdateAccountBalanceParams{
		Balance: amount,
		ID:      repository.ToPgUUID(systemAccountID),
	})
	if err != nil {
		s.updatePayoutFailed(ctx, payoutID, "failed to credit system account")
		return
	}

	// 5. Update transaction status to completed
	err = qtx.UpdateTransactionStatus(ctx, repository.UpdateTransactionStatusParams{
		Status: domain.TxStatusCompleted,
		ID:     transactionID,
	})
	if err != nil {
		s.updatePayoutFailed(ctx, payoutID, "failed to update transaction status")
		return
	}

	// 6. Update payout status to completed (retry after commit if needed)
	ref := gatewayRef
	statusUpdateErr := qtx.UpdatePayoutStatus(ctx, repository.UpdatePayoutStatusParams{
		Status:     domain.PayoutStatusCompleted,
		GatewayRef: &ref,
		ID:         repository.ToPgUUID(payoutID),
	})
	if statusUpdateErr != nil {
		zap.L().Warn("payout status update failed inside tx", zap.Error(statusUpdateErr), zap.String("payout_id", payoutID.String()))
	}

	if err := tx.Commit(ctx); err != nil {
		s.updatePayoutFailed(ctx, payoutID, "failed to commit success transaction")
		return
	}

	if statusUpdateErr != nil {
		if err := queries.UpdatePayoutStatus(ctx, repository.UpdatePayoutStatusParams{
			Status:     domain.PayoutStatusCompleted,
			GatewayRef: &ref,
			ID:         repository.ToPgUUID(payoutID),
		}); err != nil {
			zap.L().Error("payout status retry failed", zap.Error(err), zap.String("payout_id", payoutID.String()))
		}
	}
}

// handlePayoutFailure handles a failed payout from the gateway.
func (s *PayoutService) handlePayoutFailure(ctx context.Context, payoutID, accountID uuid.UUID, amount int64, reason string) {
	queries := repository.New(s.db)

	// Begin transaction
	tx, err := s.db.Begin(ctx)
	if err != nil {
		s.updatePayoutFailed(ctx, payoutID, "failed to begin failure transaction")
		return
	}
	defer tx.Rollback(ctx)

	qtx := queries.WithTx(tx)

	// 1. Release locked funds
	err = qtx.ReleaseAccountFunds(ctx, repository.ReleaseAccountFundsParams{
		LockedMicros: amount,
		ID:           repository.ToPgUUID(accountID),
	})
	if err != nil {
		s.updatePayoutFailed(ctx, payoutID, "failed to release locked funds")
		return
	}

	// 2. Get transaction ID for this payout
	payoutRow, err := qtx.GetPayout(ctx, repository.ToPgUUID(payoutID))
	if err != nil {
		return
	}

	// 3. Update transaction status to failed
	err = qtx.UpdateTransactionStatus(ctx, repository.UpdateTransactionStatusParams{
		Status: domain.TxStatusFailed,
		ID:     payoutRow.TransactionID,
	})
	if err != nil {
		return
	}

	// 4. Update payout status to failed
	err = qtx.UpdatePayoutStatus(ctx, repository.UpdatePayoutStatusParams{
		Status:     domain.PayoutStatusFailed,
		GatewayRef: nil,
		ID:         repository.ToPgUUID(payoutID),
	})
	if err != nil {
		return
	}

	if err := tx.Commit(ctx); err != nil {
		s.updatePayoutFailed(ctx, payoutID, "failed to commit failure transaction")
		return
	}
}

// updatePayoutFailed is a helper to update payout status to failed when critical errors occur.
func (s *PayoutService) updatePayoutFailed(ctx context.Context, payoutID uuid.UUID, reason string) {
	queries := repository.New(s.db)
	queries.UpdatePayoutStatus(ctx, repository.UpdatePayoutStatusParams{
		Status:     domain.PayoutStatusFailed,
		GatewayRef: nil,
		ID:         repository.ToPgUUID(payoutID),
	})
}

// GetPayout retrieves a payout by ID.
func (s *PayoutService) GetPayout(ctx context.Context, payoutID uuid.UUID) (*models.Payout, error) {
	queries := repository.New(s.db)
	row, err := queries.GetPayout(ctx, repository.ToPgUUID(payoutID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPayoutNotFound
		}
		return nil, fmt.Errorf("failed to get payout: %w", err)
	}

	return &models.Payout{
		ID:            repository.FromPgUUID(row.ID),
		TransactionID: repository.FromPgUUID(row.TransactionID),
		AccountID:     repository.FromPgUUID(row.AccountID),
		AmountMicros:  row.AmountMicros,
		Currency:      row.Currency,
		Status:        row.Status,
		GatewayRef:    row.GatewayRef,
		CreatedAt:     row.CreatedAt.Time,
		UpdatedAt:     row.UpdatedAt.Time,
	}, nil
}

type payoutMetadata struct {
	Destination PayoutDestinationInput `json:"destination"`
}

// extractDestination extracts the destination from transaction metadata.
func extractDestination(metadata []byte) PayoutDestinationInput {
	var meta payoutMetadata
	if len(metadata) == 0 {
		return meta.Destination
	}
	_ = json.Unmarshal(metadata, &meta)
	return meta.Destination
}

func formatDestination(dest PayoutDestinationInput) string {
	if dest.IBAN == "" && dest.Name == "" {
		return "EXTERNAL_ACCOUNT"
	}
	if dest.Name == "" {
		return dest.IBAN
	}
	if dest.IBAN == "" {
		return dest.Name
	}
	return fmt.Sprintf("%s (%s)", dest.Name, dest.IBAN)
}
