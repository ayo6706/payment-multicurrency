package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ayo6706/payment-multicurrency/internal/models"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/ayo6706/payment-multicurrency/internal/service"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// PayoutHandler handles HTTP requests for payouts.
type PayoutHandler struct {
	payoutSvc *service.PayoutService
	repo      *repository.Repository
}

// NewPayoutHandler creates a new PayoutHandler instance.
func NewPayoutHandler(payoutSvc *service.PayoutService, repo *repository.Repository) *PayoutHandler {
	return &PayoutHandler{
		payoutSvc: payoutSvc,
		repo:      repo,
	}
}

// CreatePayoutRequest represents the request body for creating a payout.
type CreatePayoutRequest struct {
	AccountID    string                         `json:"account_id"`
	AmountMicros int64                          `json:"amount_micros"`
	Currency     string                         `json:"currency"`
	Destination  service.PayoutDestinationInput `json:"destination"`
}

// CreatePayout handles POST /v1/payouts
// It creates a new payout request and returns 202 Accepted.
func (h *PayoutHandler) CreatePayout(w http.ResponseWriter, r *http.Request) {
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		RespondError(w, r, http.StatusBadRequest, "idempotency/missing-key", "Idempotency-Key header is required")
		return
	}

	// Parse request body
	var req CreatePayoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, r, http.StatusBadRequest, "request/invalid-body", "Invalid request body")
		return
	}

	// Validate request
	if req.AmountMicros <= 0 {
		RespondError(w, r, http.StatusBadRequest, "request/invalid-amount", "Amount must be greater than zero")
		return
	}
	if req.AccountID == "" {
		RespondError(w, r, http.StatusBadRequest, "request/missing-account-id", "account_id is required")
		return
	}
	if req.Currency == "" {
		RespondError(w, r, http.StatusBadRequest, "request/missing-currency", "currency is required")
		return
	}
	if err := req.Destination.Validate(); err != nil {
		RespondError(w, r, http.StatusBadRequest, "request/invalid-destination", err.Error())
		return
	}

	// Parse account ID
	accountID, err := uuid.Parse(req.AccountID)
	if err != nil {
		RespondError(w, r, http.StatusBadRequest, "request/invalid-account-id", "Invalid account_id")
		return
	}

	// Call service
	payoutReq := service.RequestPayoutRequest{
		AccountID:    accountID,
		AmountMicros: req.AmountMicros,
		Currency:     req.Currency,
		Destination:  req.Destination,
		ReferenceID:  idempotencyKey,
	}

	resp, err := h.payoutSvc.RequestPayout(r.Context(), payoutReq)
	if err != nil {
		if errors.Is(err, models.ErrInsufficientFunds) {
			RespondError(w, r, http.StatusBadRequest, "payout/insufficient-funds", err.Error())
			return
		}
		zap.L().Error("create payout failed", zap.Error(err))
		RespondError(w, r, http.StatusInternalServerError, "payout/create-failed", "Failed to create payout")
		return
	}

	RespondJSON(w, http.StatusAccepted, resp)
}

// GetPayout handles GET /v1/payouts/{id}
// It returns the current status of a payout.
func (h *PayoutHandler) GetPayout(w http.ResponseWriter, r *http.Request) {
	actorID, isAdmin, err := requestActor(r)
	if err != nil {
		RespondError(w, r, http.StatusUnauthorized, "auth/unauthorized", "Unauthorized")
		return
	}

	// Extract payout ID from URL
	payoutIDStr := chi.URLParam(r, "id")
	payoutID, err := uuid.Parse(payoutIDStr)
	if err != nil {
		RespondError(w, r, http.StatusBadRequest, "request/invalid-payout-id", "Invalid payout ID")
		return
	}

	// Get payout from service
	payout, err := h.payoutSvc.GetPayout(r.Context(), payoutID)
	if err != nil {
		if errors.Is(err, service.ErrPayoutNotFound) {
			RespondError(w, r, http.StatusNotFound, "payout/not-found", "Payout not found")
			return
		}
		zap.L().Error("get payout failed", zap.Error(err), zap.String("payout_id", payoutID.String()))
		RespondError(w, r, http.StatusInternalServerError, "payout/read-failed", "Failed to get payout")
		return
	}
	if !isAdmin {
		account, accErr := h.repo.GetAccount(r.Context(), payout.AccountID)
		if accErr != nil {
			RespondError(w, r, http.StatusInternalServerError, "payout/account-read-failed", "Failed to verify payout ownership")
			return
		}
		if account.UserID != actorID {
			RespondError(w, r, http.StatusForbidden, "auth/insufficient-permissions", "insufficient permissions")
			return
		}
	}

	RespondJSON(w, http.StatusOK, payout)
}

// ListManualReviewPayouts handles GET /v1/payouts/manual-review (admin only).
func (h *PayoutHandler) ListManualReviewPayouts(w http.ResponseWriter, r *http.Request) {
	limit := int32(50)
	offset := int32(0)
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed <= 0 {
			RespondError(w, r, http.StatusBadRequest, "request/invalid-limit", "limit must be a positive integer")
			return
		}
		limit = int32(parsed)
	}
	if v := strings.TrimSpace(r.URL.Query().Get("offset")); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed < 0 {
			RespondError(w, r, http.StatusBadRequest, "request/invalid-offset", "offset must be a non-negative integer")
			return
		}
		offset = int32(parsed)
	}

	payouts, err := h.payoutSvc.ListManualReviewPayouts(r.Context(), limit, offset)
	if err != nil {
		zap.L().Error("list manual review payouts failed", zap.Error(err))
		RespondError(w, r, http.StatusInternalServerError, "payout/manual-review-list-failed", "Failed to list manual review payouts")
		return
	}
	total, err := h.payoutSvc.ManualReviewQueueSize(r.Context())
	if err != nil {
		zap.L().Warn("failed to compute manual review queue size", zap.Error(err))
		total = int64(len(payouts))
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"items":       payouts,
		"limit":       limit,
		"offset":      offset,
		"count":       len(payouts),
		"total_count": total,
	})
}

type resolveManualReviewRequest struct {
	Decision   string  `json:"decision"`
	Reason     string  `json:"reason"`
	GatewayRef *string `json:"gateway_ref,omitempty"`
}

// ResolveManualReviewPayout handles POST /v1/payouts/{id}/resolve (admin only).
func (h *PayoutHandler) ResolveManualReviewPayout(w http.ResponseWriter, r *http.Request) {
	actorID, _, err := requestActor(r)
	if err != nil {
		RespondError(w, r, http.StatusUnauthorized, "auth/unauthorized", "Unauthorized")
		return
	}
	payoutID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, r, http.StatusBadRequest, "request/invalid-payout-id", "Invalid payout ID")
		return
	}

	var req resolveManualReviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, r, http.StatusBadRequest, "request/invalid-body", "Invalid request body")
		return
	}
	req.Decision = strings.TrimSpace(strings.ToLower(req.Decision))
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Decision == "" {
		RespondError(w, r, http.StatusBadRequest, "request/missing-decision", "decision is required")
		return
	}
	if req.Reason == "" {
		RespondError(w, r, http.StatusBadRequest, "request/missing-reason", "reason is required")
		return
	}

	result, err := h.payoutSvc.ResolveManualReviewPayout(r.Context(), service.ResolveManualReviewRequest{
		PayoutID:   payoutID,
		Decision:   service.ResolveManualReviewDecision(req.Decision),
		Reason:     req.Reason,
		ActorID:    &actorID,
		GatewayRef: req.GatewayRef,
	})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrPayoutNotFound):
			RespondError(w, r, http.StatusNotFound, "payout/not-found", "Payout not found")
			return
		case errors.Is(err, service.ErrPayoutNotInManualReview):
			RespondError(w, r, http.StatusConflict, "payout/not-in-manual-review", "Payout is not in manual review")
			return
		case errors.Is(err, service.ErrInvalidManualReviewDecision):
			RespondError(w, r, http.StatusBadRequest, "payout/invalid-decision", "decision must be confirm_sent or refund_failed")
			return
		default:
			zap.L().Error("resolve manual review payout failed", zap.Error(err), zap.String("payout_id", payoutID.String()))
			RespondError(w, r, http.StatusInternalServerError, "payout/manual-review-resolve-failed", "Failed to resolve manual review payout")
			return
		}
	}

	RespondJSON(w, http.StatusOK, result)
}
