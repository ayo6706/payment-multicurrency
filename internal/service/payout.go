package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ayo6706/payment-multicurrency/internal/domain"
	"github.com/ayo6706/payment-multicurrency/internal/gateway"
	"github.com/ayo6706/payment-multicurrency/internal/models"
	"github.com/ayo6706/payment-multicurrency/internal/observability"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"
)

// PayoutService handles business logic for external payouts.
type PayoutService struct {
	store   QueryStore
	gateway gateway.Gateway
	audit   *AuditService
}

var (
	ErrPayoutNotFound              = errors.New("payout not found")
	ErrPayoutNotInManualReview     = errors.New("payout is not in manual review")
	ErrInvalidManualReviewDecision = errors.New("invalid manual review decision")
)

const stalePayoutRecoveryWindow = 2 * time.Minute

func NewPayoutService(store QueryStore, gw gateway.Gateway) *PayoutService {
	return &PayoutService{
		store:   store,
		gateway: gw,
		audit:   NewAuditService(store),
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

type ResolveManualReviewDecision string

const (
	DecisionConfirmSent  ResolveManualReviewDecision = "confirm_sent"
	DecisionRefundFailed ResolveManualReviewDecision = "refund_failed"
)

type ResolveManualReviewRequest struct {
	PayoutID   uuid.UUID
	Decision   ResolveManualReviewDecision
	Reason     string
	ActorID    *uuid.UUID
	GatewayRef *string
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

	queries := s.store.Queries()

	// Check idempotency
	existingTxRow, err := queries.CheckTransactionIdempotency(ctx, req.ReferenceID)
	if err == nil {
		// Return existing payout
		existingPayout, payoutErr := queries.GetPayoutByTransactionID(ctx, existingTxRow.ID)
		if payoutErr == nil {
			return &PayoutResponse{
				PayoutID: repository.FromPgUUID(existingPayout.ID),
				Status:   existingPayout.Status,
				Message:  "Payout already exists",
			}, nil
		}
		if !errors.Is(payoutErr, pgx.ErrNoRows) {
			return nil, fmt.Errorf("failed to get payout by transaction: %w", payoutErr)
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("failed to check idempotency: %w", err)
	}

	transactionID := uuid.New()
	payoutID := uuid.New()
	err = s.store.RunInTx(ctx, func(qtx *repository.Queries) error {
		// Lock the account and check balance
		accountRow, err := qtx.GetAccountBalanceAndLocked(ctx, repository.ToPgUUID(req.AccountID))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("account not found: %w", models.ErrInsufficientFunds)
			}
			return fmt.Errorf("failed to lock account: %w", err)
		}

		availableBalance := accountRow.Balance - accountRow.LockedMicros
		if availableBalance < req.AmountMicros {
			return models.ErrInsufficientFunds
		}

		// Verify currency matches
		if accountRow.Currency != req.Currency {
			return fmt.Errorf("currency mismatch: account is %s, requested %s", accountRow.Currency, req.Currency)
		}

		// Lock the funds
		rows, err := qtx.LockAccountFunds(ctx, repository.LockAccountFundsParams{
			LockedMicros: req.AmountMicros,
			ID:           repository.ToPgUUID(req.AccountID),
		})
		if err != nil {
			return fmt.Errorf("failed to lock funds: %w", err)
		}
		if err := requireExactlyOne(rows, "lock account funds"); err != nil {
			return err
		}

		// Create transaction record
		metadata, err := json.Marshal(map[string]any{
			"destination": req.Destination,
		})
		if err != nil {
			return fmt.Errorf("failed to encode metadata: %w", err)
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
			return fmt.Errorf("failed to create transaction: %w", err)
		}

		if err := s.audit.Write(ctx, qtx, "transaction", transactionID, nil, "created", "", domain.TxStatusPending, metadata); err != nil {
			return err
		}

		// Create payout record
		_, err = qtx.InsertPayout(ctx, repository.InsertPayoutParams{
			ID:            repository.ToPgUUID(payoutID),
			TransactionID: repository.ToPgUUID(transactionID),
			AccountID:     repository.ToPgUUID(req.AccountID),
			AmountMicros:  req.AmountMicros,
			Currency:      req.Currency,
			Status:        domain.PayoutStatusPending,
		})
		if err != nil {
			return fmt.Errorf("failed to create payout: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
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
func (s *PayoutService) ProcessPayouts(ctx context.Context, batchSize int32) error {
	if err := s.recoverStaleProcessingPayouts(ctx, batchSize); err != nil {
		return err
	}

	claimed, err := s.claimPendingPayouts(ctx, batchSize)
	if err != nil {
		return err
	}
	if len(claimed) == 0 {
		return nil
	}

	queries := s.store.Queries()
	for i, payout := range claimed {
		if err := ctx.Err(); err != nil {
			if requeueErr := s.requeueClaimedPayouts(context.Background(), claimed[i:]); requeueErr != nil {
				zap.L().Error("failed to requeue claimed payouts on context cancellation", zap.Error(requeueErr))
			}
			return err
		}

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
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				if requeueErr := s.requeueClaimedPayouts(context.Background(), []repository.Payout{payout}); requeueErr != nil {
					zap.L().Error("failed to requeue payout after gateway cancellation", zap.Error(requeueErr), zap.String("payout_id", payoutID.String()))
				}
				return err
			}
			s.handlePayoutFailure(ctx, payoutID, accountID, payout.AmountMicros, err.Error())
			continue
		}

		if err := s.handlePayoutSuccess(ctx, payoutID, accountID, payout.AmountMicros, payout.Currency, payout.TransactionID, gatewayRef); err != nil {
			zap.L().Error(
				"payout succeeded at gateway but local finalization failed; moved to manual review",
				zap.Error(err),
				zap.String("payout_id", payoutID.String()),
				zap.String("gateway_ref", gatewayRef),
			)
		}
	}

	return nil
}

func (s *PayoutService) recoverStaleProcessingPayouts(ctx context.Context, batchSize int32) error {
	cutoff := time.Now().Add(-stalePayoutRecoveryWindow)
	var stale []repository.Payout
	err := s.store.RunInTx(ctx, func(qtx *repository.Queries) error {
		var err error
		stale, err = qtx.GetStaleProcessingPayouts(ctx, repository.GetStaleProcessingPayoutsParams{
			UpdatedAt: pgtype.Timestamptz{Time: cutoff, Valid: true},
			Limit:     batchSize,
		})
		if err != nil {
			return fmt.Errorf("load stale processing payouts: %w", err)
		}
		for _, payout := range stale {
			rows, err := qtx.UpdatePayoutStatus(ctx, repository.UpdatePayoutStatusParams{
				Status:     domain.PayoutStatusPending,
				GatewayRef: nil,
				ID:         payout.ID,
			})
			if err != nil {
				return fmt.Errorf("requeue stale payout %s: %w", repository.FromPgUUID(payout.ID), err)
			}
			if err := requireExactlyOne(rows, "requeue stale payout"); err != nil {
				return err
			}
			if err := transitionTransactionState(ctx, qtx, s.audit, repository.FromPgUUID(payout.TransactionID), domain.TxStatusPending, nil, "requeue_stale", nil); err != nil {
				return fmt.Errorf("transition stale payout transaction %s: %w", repository.FromPgUUID(payout.TransactionID), err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	if len(stale) > 0 {
		zap.L().Warn("recovered stale processing payouts", zap.Int("count", len(stale)))
	}
	return nil
}

func (s *PayoutService) requeueClaimedPayouts(ctx context.Context, payouts []repository.Payout) error {
	if len(payouts) == 0 {
		return nil
	}
	return s.store.RunInTx(ctx, func(qtx *repository.Queries) error {
		for _, payout := range payouts {
			rows, err := qtx.UpdatePayoutStatus(ctx, repository.UpdatePayoutStatusParams{
				Status:     domain.PayoutStatusPending,
				GatewayRef: nil,
				ID:         payout.ID,
			})
			if err != nil {
				return fmt.Errorf("requeue payout %s: %w", repository.FromPgUUID(payout.ID), err)
			}
			if err := requireExactlyOne(rows, "requeue claimed payout"); err != nil {
				return err
			}
			if err := transitionTransactionState(ctx, qtx, s.audit, repository.FromPgUUID(payout.TransactionID), domain.TxStatusPending, nil, "requeue_claimed", nil); err != nil {
				return fmt.Errorf("transition claimed transaction %s: %w", repository.FromPgUUID(payout.TransactionID), err)
			}
		}
		return nil
	})
}

func (s *PayoutService) claimPendingPayouts(ctx context.Context, batchSize int32) ([]repository.Payout, error) {
	var payouts []repository.Payout
	err := s.store.RunInTx(ctx, func(queries *repository.Queries) error {
		var err error
		payouts, err = queries.GetPendingPayouts(ctx, batchSize)
		if err != nil {
			return fmt.Errorf("failed to fetch pending payouts: %w", err)
		}

		for i, payout := range payouts {
			payouts[i].Status = domain.PayoutStatusProcessing
			rows, err := queries.UpdatePayoutStatus(ctx, repository.UpdatePayoutStatusParams{
				Status:     domain.PayoutStatusProcessing,
				GatewayRef: nil,
				ID:         payout.ID,
			})
			if err != nil {
				return fmt.Errorf("failed to mark payout processing: %w", err)
			}
			if err := requireExactlyOne(rows, "mark payout processing"); err != nil {
				return err
			}

			if err := transitionTransactionState(ctx, queries, s.audit, repository.FromPgUUID(payout.TransactionID), domain.TxStatusProcessing, nil, "processing_started", nil); err != nil {
				return fmt.Errorf("failed to transition transaction status: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return payouts, nil
}

// handlePayoutSuccess finalizes accounting for a successful gateway payout.
// If local finalization fails after gateway success, the payout is moved to MANUAL_REVIEW
// and funds remain locked to avoid accidental double spend/retry.
func (s *PayoutService) handlePayoutSuccess(ctx context.Context, payoutID, accountID uuid.UUID, amount int64, currency string, transactionID pgtype.UUID, gatewayRef string) error {
	err := s.store.RunInTx(ctx, func(qtx *repository.Queries) error {
		rows, err := qtx.DeductLockedFunds(ctx, repository.DeductLockedFundsParams{
			LockedMicros: amount,
			ID:           repository.ToPgUUID(accountID),
		})
		if err != nil {
			return fmt.Errorf("failed to deduct locked funds: %w", err)
		}
		if err := requireExactlyOne(rows, "deduct locked payout funds"); err != nil {
			return err
		}

		systemAccountID, err := getSystemAccountID(currency)
		if err != nil {
			return fmt.Errorf("failed to get system account: %w", err)
		}

		_, err = qtx.CreateEntry(ctx, repository.CreateEntryParams{
			ID:            repository.ToPgUUID(uuid.New()),
			TransactionID: transactionID,
			AccountID:     repository.ToPgUUID(accountID),
			Amount:        amount,
			Direction:     domain.DirectionDebit,
		})
		if err != nil {
			return fmt.Errorf("failed to create user debit entry: %w", err)
		}

		_, err = qtx.CreateEntry(ctx, repository.CreateEntryParams{
			ID:            repository.ToPgUUID(uuid.New()),
			TransactionID: transactionID,
			AccountID:     repository.ToPgUUID(systemAccountID),
			Amount:        amount,
			Direction:     domain.DirectionCredit,
		})
		if err != nil {
			return fmt.Errorf("failed to create system credit entry: %w", err)
		}

		rows, err = qtx.UpdateAccountBalance(ctx, repository.UpdateAccountBalanceParams{
			Balance: amount,
			ID:      repository.ToPgUUID(systemAccountID),
		})
		if err != nil {
			return fmt.Errorf("failed to credit system account: %w", err)
		}
		if err := requireExactlyOne(rows, "credit system account"); err != nil {
			return err
		}

		if err := transitionTransactionState(ctx, qtx, s.audit, repository.FromPgUUID(transactionID), domain.TxStatusCompleted, nil, "payout_completed", nil); err != nil {
			return fmt.Errorf("failed to transition transaction status: %w", err)
		}

		ref := gatewayRef
		rows, err = qtx.UpdatePayoutStatus(ctx, repository.UpdatePayoutStatusParams{
			Status:     domain.PayoutStatusCompleted,
			GatewayRef: &ref,
			ID:         repository.ToPgUUID(payoutID),
		})
		if err != nil {
			return fmt.Errorf("failed to update payout status: %w", err)
		}
		if err := requireExactlyOne(rows, "mark payout completed"); err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		s.markPayoutManualReview(ctx, payoutID, gatewayRef, err.Error())
		return err
	}
	return nil
}

// handlePayoutFailure handles a failed payout from the gateway.
func (s *PayoutService) handlePayoutFailure(ctx context.Context, payoutID, accountID uuid.UUID, amount int64, reason string) {
	err := s.store.RunInTx(ctx, func(qtx *repository.Queries) error {
		// 1. Release locked funds
		rows, err := qtx.ReleaseAccountFunds(ctx, repository.ReleaseAccountFundsParams{
			LockedMicros: amount,
			ID:           repository.ToPgUUID(accountID),
		})
		if err != nil {
			return fmt.Errorf("release locked funds: %w", err)
		}
		if err := requireExactlyOne(rows, "release locked payout funds"); err != nil {
			return err
		}

		// 2. Get transaction ID for this payout
		payoutRow, err := qtx.GetPayout(ctx, repository.ToPgUUID(payoutID))
		if err != nil {
			return fmt.Errorf("load payout for failure handling: %w", err)
		}

		metadata, metaErr := marshalReasonMetadata(reason)
		if metaErr != nil {
			return fmt.Errorf("marshal payout failure metadata: %w", metaErr)
		}
		if err := transitionTransactionState(ctx, qtx, s.audit, repository.FromPgUUID(payoutRow.TransactionID), domain.TxStatusFailed, nil, "payout_failed", metadata); err != nil {
			return fmt.Errorf("transition transaction failed status: %w", err)
		}

		// 4. Update payout status to failed
		rows, err = qtx.UpdatePayoutStatus(ctx, repository.UpdatePayoutStatusParams{
			Status:     domain.PayoutStatusFailed,
			GatewayRef: nil,
			ID:         repository.ToPgUUID(payoutID),
		})
		if err != nil {
			return fmt.Errorf("update payout failed status: %w", err)
		}
		if err := requireExactlyOne(rows, "mark payout failed"); err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		zap.L().Error("handle payout failure failed", zap.Error(err), zap.String("payout_id", payoutID.String()))
		s.updatePayoutFailed(ctx, payoutID, err.Error()+": "+reason)
		return
	}

	zap.L().Warn("payout marked failed", zap.String("payout_id", payoutID.String()), zap.String("reason", reason))
}

// updatePayoutFailed is a helper to update payout status to failed when critical errors occur.
func (s *PayoutService) updatePayoutFailed(ctx context.Context, payoutID uuid.UUID, reason string) {
	queries := s.store.Queries()
	payoutRow, err := queries.GetPayout(ctx, repository.ToPgUUID(payoutID))
	if err == nil {
		if released, releaseErr := queries.ReleaseAccountFundsSafe(ctx, repository.ReleaseAccountFundsSafeParams{
			LockedMicros: payoutRow.AmountMicros,
			ID:           payoutRow.AccountID,
		}); releaseErr != nil {
			zap.L().Error("fallback locked funds release failed", zap.Error(releaseErr), zap.String("payout_id", payoutID.String()))
		} else if released > 0 {
			zap.L().Warn("fallback released locked funds", zap.String("payout_id", payoutID.String()), zap.Int64("amount_micros", payoutRow.AmountMicros))
		}

		if rows, err := queries.UpdateTransactionStatus(ctx, repository.UpdateTransactionStatusParams{
			Status: domain.TxStatusFailed,
			ID:     payoutRow.TransactionID,
		}); err != nil {
			zap.L().Error("fallback transaction fail update failed", zap.Error(err), zap.String("payout_id", payoutID.String()))
		} else if err := requireExactlyOne(rows, "fallback mark transaction failed"); err != nil {
			zap.L().Error("fallback transaction fail update affected unexpected rows", zap.Error(err), zap.String("payout_id", payoutID.String()))
		} else {
			if err := s.store.RunInTx(ctx, func(qtx *repository.Queries) error {
				metadata, metaErr := marshalReasonMetadata(reason)
				if metaErr != nil {
					return fmt.Errorf("marshal payout failure fallback metadata: %w", metaErr)
				}
				return s.audit.Write(ctx, qtx, "transaction", repository.FromPgUUID(payoutRow.TransactionID), nil, "payout_failed_fallback", "", domain.TxStatusFailed, metadata)
			}); err != nil {
				zap.L().Error("fallback audit write failed", zap.Error(err), zap.String("payout_id", payoutID.String()))
			}
		}
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		zap.L().Error("fallback payout lookup failed", zap.Error(err), zap.String("payout_id", payoutID.String()))
	}

	if rows, err := queries.UpdatePayoutStatus(ctx, repository.UpdatePayoutStatusParams{
		Status:     domain.PayoutStatusFailed,
		GatewayRef: nil,
		ID:         repository.ToPgUUID(payoutID),
	}); err != nil {
		zap.L().Error("fallback payout fail update failed", zap.Error(err), zap.String("payout_id", payoutID.String()))
	} else if err := requireExactlyOne(rows, "fallback mark payout failed"); err != nil {
		zap.L().Error("fallback payout fail update affected unexpected rows", zap.Error(err), zap.String("payout_id", payoutID.String()))
	}

	zap.L().Warn("payout failure fallback executed", zap.String("payout_id", payoutID.String()), zap.String("reason", reason))
}

func (s *PayoutService) markPayoutManualReview(ctx context.Context, payoutID uuid.UUID, gatewayRef, reason string) {
	queries := s.store.Queries()
	ref := gatewayRef
	if rows, err := queries.UpdatePayoutStatus(ctx, repository.UpdatePayoutStatusParams{
		Status:     domain.PayoutStatusManualReview,
		GatewayRef: &ref,
		ID:         repository.ToPgUUID(payoutID),
	}); err != nil {
		zap.L().Error("failed to mark payout manual review", zap.Error(err), zap.String("payout_id", payoutID.String()))
		return
	} else if err := requireExactlyOne(rows, "mark payout manual review"); err != nil {
		zap.L().Error("mark payout manual review affected unexpected rows", zap.Error(err), zap.String("payout_id", payoutID.String()))
		return
	}
	observability.IncrementManualReviewTransition("queued")

	row, err := queries.GetPayout(ctx, repository.ToPgUUID(payoutID))
	if err != nil {
		zap.L().Warn("manual review audit skipped: payout read failed", zap.Error(err), zap.String("payout_id", payoutID.String()))
		return
	}
	if err := s.store.RunInTx(ctx, func(qtx *repository.Queries) error {
		metadata, metaErr := marshalReasonMetadata(reason)
		if metaErr != nil {
			return fmt.Errorf("marshal manual review metadata: %w", metaErr)
		}
		return s.audit.Write(
			ctx,
			qtx,
			"transaction",
			repository.FromPgUUID(row.TransactionID),
			nil,
			"payout_manual_review",
			domain.TxStatusProcessing,
			domain.TxStatusProcessing,
			metadata,
		)
	}); err != nil {
		zap.L().Warn("manual review audit write failed", zap.Error(err), zap.String("payout_id", payoutID.String()))
	}
}

// GetPayout retrieves a payout by ID.
func (s *PayoutService) GetPayout(ctx context.Context, payoutID uuid.UUID) (*models.Payout, error) {
	queries := s.store.Queries()
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

func (s *PayoutService) ManualReviewQueueSize(ctx context.Context) (int64, error) {
	count, err := s.store.Queries().CountPayoutsByStatus(ctx, domain.PayoutStatusManualReview)
	if err != nil {
		return 0, fmt.Errorf("count manual review payouts: %w", err)
	}
	return count, nil
}

// ListManualReviewPayouts returns payouts currently waiting for manual operator action.
func (s *PayoutService) ListManualReviewPayouts(ctx context.Context, limit, offset int32) ([]models.Payout, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.store.Queries().GetPayoutsByStatus(ctx, repository.GetPayoutsByStatusParams{
		Status: domain.PayoutStatusManualReview,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list manual review payouts: %w", err)
	}
	out := make([]models.Payout, 0, len(rows))
	for _, row := range rows {
		out = append(out, models.Payout{
			ID:            repository.FromPgUUID(row.ID),
			TransactionID: repository.FromPgUUID(row.TransactionID),
			AccountID:     repository.FromPgUUID(row.AccountID),
			AmountMicros:  row.AmountMicros,
			Currency:      row.Currency,
			Status:        row.Status,
			GatewayRef:    row.GatewayRef,
			CreatedAt:     row.CreatedAt.Time,
			UpdatedAt:     row.UpdatedAt.Time,
		})
	}
	return out, nil
}

// ResolveManualReviewPayout finalizes a payout stuck in MANUAL_REVIEW.
func (s *PayoutService) ResolveManualReviewPayout(ctx context.Context, req ResolveManualReviewRequest) (*models.Payout, error) {
	decision := ResolveManualReviewDecision(strings.ToLower(strings.TrimSpace(string(req.Decision))))
	switch decision {
	case DecisionConfirmSent, DecisionRefundFailed:
	default:
		return nil, ErrInvalidManualReviewDecision
	}

	err := s.store.RunInTx(ctx, func(qtx *repository.Queries) error {
		payoutRow, err := qtx.GetPayoutForUpdate(ctx, repository.ToPgUUID(req.PayoutID))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrPayoutNotFound
			}
			return fmt.Errorf("get payout for update: %w", err)
		}
		if payoutRow.Status != domain.PayoutStatusManualReview {
			return ErrPayoutNotInManualReview
		}

		transactionID := repository.FromPgUUID(payoutRow.TransactionID)
		metadata, metaErr := marshalReasonMetadata(req.Reason)
		if metaErr != nil {
			return fmt.Errorf("marshal resolution metadata: %w", metaErr)
		}

		switch decision {
		case DecisionConfirmSent:
			if err := s.applyManualReviewConfirmation(ctx, qtx, payoutRow, transactionID, req.ActorID, metadata, req.GatewayRef); err != nil {
				return err
			}
		case DecisionRefundFailed:
			if err := s.applyManualReviewRefund(ctx, qtx, payoutRow, transactionID, req.ActorID, metadata); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	observability.IncrementManualReviewTransition(string(decision))
	return s.GetPayout(ctx, req.PayoutID)
}

func (s *PayoutService) applyManualReviewConfirmation(
	ctx context.Context,
	qtx *repository.Queries,
	payoutRow repository.Payout,
	transactionID uuid.UUID,
	actorID *uuid.UUID,
	metadata []byte,
	overrideGatewayRef *string,
) error {
	rows, err := qtx.DeductLockedFunds(ctx, repository.DeductLockedFundsParams{
		LockedMicros: payoutRow.AmountMicros,
		ID:           payoutRow.AccountID,
	})
	if err != nil {
		return fmt.Errorf("manual-review confirm: deduct locked funds: %w", err)
	}
	if err := requireExactlyOne(rows, "manual-review deduct locked funds"); err != nil {
		return err
	}

	systemAccountID, err := getSystemAccountID(payoutRow.Currency)
	if err != nil {
		return err
	}
	_, err = qtx.CreateEntry(ctx, repository.CreateEntryParams{
		ID:            repository.ToPgUUID(uuid.New()),
		TransactionID: payoutRow.TransactionID,
		AccountID:     payoutRow.AccountID,
		Amount:        payoutRow.AmountMicros,
		Direction:     domain.DirectionDebit,
	})
	if err != nil {
		return fmt.Errorf("manual-review confirm: create user debit entry: %w", err)
	}
	_, err = qtx.CreateEntry(ctx, repository.CreateEntryParams{
		ID:            repository.ToPgUUID(uuid.New()),
		TransactionID: payoutRow.TransactionID,
		AccountID:     repository.ToPgUUID(systemAccountID),
		Amount:        payoutRow.AmountMicros,
		Direction:     domain.DirectionCredit,
	})
	if err != nil {
		return fmt.Errorf("manual-review confirm: create system credit entry: %w", err)
	}
	rows, err = qtx.UpdateAccountBalance(ctx, repository.UpdateAccountBalanceParams{
		Balance: payoutRow.AmountMicros,
		ID:      repository.ToPgUUID(systemAccountID),
	})
	if err != nil {
		return fmt.Errorf("manual-review confirm: credit system account: %w", err)
	}
	if err := requireExactlyOne(rows, "manual-review credit system account"); err != nil {
		return err
	}

	if err := transitionTransactionState(ctx, qtx, s.audit, transactionID, domain.TxStatusCompleted, actorID, "manual_review_confirmed", metadata); err != nil {
		return fmt.Errorf("manual-review confirm: transition transaction: %w", err)
	}

	ref := payoutRow.GatewayRef
	if overrideGatewayRef != nil && strings.TrimSpace(*overrideGatewayRef) != "" {
		val := strings.TrimSpace(*overrideGatewayRef)
		ref = &val
	}
	rows, err = qtx.UpdatePayoutStatus(ctx, repository.UpdatePayoutStatusParams{
		Status:     domain.PayoutStatusCompleted,
		GatewayRef: ref,
		ID:         payoutRow.ID,
	})
	if err != nil {
		return fmt.Errorf("manual-review confirm: update payout status: %w", err)
	}
	if err := requireExactlyOne(rows, "manual-review set payout completed"); err != nil {
		return err
	}
	return nil
}

func (s *PayoutService) applyManualReviewRefund(
	ctx context.Context,
	qtx *repository.Queries,
	payoutRow repository.Payout,
	transactionID uuid.UUID,
	actorID *uuid.UUID,
	metadata []byte,
) error {
	rows, err := qtx.ReleaseAccountFundsSafe(ctx, repository.ReleaseAccountFundsSafeParams{
		LockedMicros: payoutRow.AmountMicros,
		ID:           payoutRow.AccountID,
	})
	if err != nil {
		return fmt.Errorf("manual-review refund: release locked funds: %w", err)
	}
	if rows > 1 {
		return fmt.Errorf("manual-review refund: released unexpected rows: %d", rows)
	}

	if err := transitionTransactionState(ctx, qtx, s.audit, transactionID, domain.TxStatusFailed, actorID, "manual_review_refunded", metadata); err != nil {
		return fmt.Errorf("manual-review refund: transition transaction: %w", err)
	}
	rows, err = qtx.UpdatePayoutStatus(ctx, repository.UpdatePayoutStatusParams{
		Status:     domain.PayoutStatusFailed,
		GatewayRef: payoutRow.GatewayRef,
		ID:         payoutRow.ID,
	})
	if err != nil {
		return fmt.Errorf("manual-review refund: update payout status: %w", err)
	}
	if err := requireExactlyOne(rows, "manual-review set payout failed"); err != nil {
		return err
	}
	return nil
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

func marshalReasonMetadata(reason string) ([]byte, error) {
	return json.Marshal(map[string]string{
		"reason": reason,
	})
}
