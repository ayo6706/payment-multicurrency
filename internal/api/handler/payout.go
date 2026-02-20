package handler

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/ayo6706/payment-multicurrency/internal/models"
	"github.com/ayo6706/payment-multicurrency/internal/service"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// PayoutHandler handles HTTP requests for payouts.
type PayoutHandler struct {
	payoutSvc *service.PayoutService
}

// NewPayoutHandler creates a new PayoutHandler instance.
func NewPayoutHandler(payoutSvc *service.PayoutService) *PayoutHandler {
	return &PayoutHandler{
		payoutSvc: payoutSvc,
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
	// Get idempotency key from header
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		RespondError(w, http.StatusBadRequest, "Idempotency-Key header is required")
		return
	}

	// Parse request body
	var req CreatePayoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate request
	if req.AmountMicros <= 0 {
		RespondError(w, http.StatusBadRequest, "Amount must be greater than zero")
		return
	}
	if req.AccountID == "" {
		RespondError(w, http.StatusBadRequest, "account_id is required")
		return
	}
	if req.Currency == "" {
		RespondError(w, http.StatusBadRequest, "currency is required")
		return
	}
	if err := req.Destination.Validate(); err != nil {
		RespondError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Parse account ID
	accountID, err := uuid.Parse(req.AccountID)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid account_id")
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
			RespondError(w, http.StatusBadRequest, err.Error())
			return
		}
		log.Printf("Error creating payout: %v", err)
		RespondError(w, http.StatusInternalServerError, "Failed to create payout")
		return
	}

	RespondJSON(w, http.StatusAccepted, resp)
}

// GetPayout handles GET /v1/payouts/{id}
// It returns the current status of a payout.
func (h *PayoutHandler) GetPayout(w http.ResponseWriter, r *http.Request) {
	// Extract payout ID from URL
	payoutIDStr := chi.URLParam(r, "id")
	payoutID, err := uuid.Parse(payoutIDStr)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid payout ID")
		return
	}

	// Get payout from service
	payout, err := h.payoutSvc.GetPayout(r.Context(), payoutID)
	if err != nil {
		if errors.Is(err, service.ErrPayoutNotFound) {
			RespondError(w, http.StatusNotFound, "Payout not found")
			return
		}
		log.Printf("Error getting payout: %v", err)
		RespondError(w, http.StatusInternalServerError, "Failed to get payout")
		return
	}

	RespondJSON(w, http.StatusOK, payout)
}
