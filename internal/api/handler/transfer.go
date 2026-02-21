package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/ayo6706/payment-multicurrency/internal/models"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/ayo6706/payment-multicurrency/internal/service"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type TransferHandler struct {
	svc  *service.TransferService
	repo *repository.Repository
}

func NewTransferHandler(svc *service.TransferService, repo *repository.Repository) *TransferHandler {
	return &TransferHandler{svc: svc, repo: repo}
}

func (h *TransferHandler) MakeInternalTransfer(w http.ResponseWriter, r *http.Request) {
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		RespondError(w, r, http.StatusBadRequest, "idempotency/missing-key", "Idempotency-Key header is required")
		return
	}

	var req struct {
		FromAccountID string `json:"from_account_id"`
		ToAccountID   string `json:"to_account_id"`
		Amount        int64  `json:"amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, r, http.StatusBadRequest, "request/invalid-body", "Invalid request body")
		return
	}

	fromID, err := uuid.Parse(req.FromAccountID)
	if err != nil {
		RespondError(w, r, http.StatusBadRequest, "request/invalid-from-account-id", "Invalid from_account_id")
		return
	}
	toID, err := uuid.Parse(req.ToAccountID)
	if err != nil {
		RespondError(w, r, http.StatusBadRequest, "request/invalid-to-account-id", "Invalid to_account_id")
		return
	}
	actorID, isAdmin, err := requestActor(r)
	if err != nil {
		RespondError(w, r, http.StatusUnauthorized, "auth/unauthorized", "Unauthorized")
		return
	}
	fromAcc, err := h.repo.GetAccount(r.Context(), fromID)
	if err != nil {
		RespondError(w, r, http.StatusBadRequest, "request/invalid-from-account-id", "Invalid from_account_id")
		return
	}
	if !isAdmin && fromAcc.UserID != actorID {
		RespondError(w, r, http.StatusForbidden, "auth/insufficient-permissions", "insufficient permissions")
		return
	}

	tx, err := h.svc.Transfer(r.Context(), fromID, toID, req.Amount, idempotencyKey)
	if err != nil {
		if err == models.ErrInsufficientFunds {
			RespondError(w, r, http.StatusBadRequest, "transfer/insufficient-funds", err.Error())
			return
		}
		if errors.Is(err, service.ErrInvalidAmount) || errors.Is(err, service.ErrReferenceRequired) || errors.Is(err, service.ErrSameAccountTransfer) || errors.Is(err, service.ErrCurrencyMismatch) {
			RespondError(w, r, http.StatusBadRequest, "transfer/invalid-request", err.Error())
			return
		}
		zap.L().Error("internal transfer failed", zap.Error(err), zap.String("from_account_id", fromID.String()), zap.String("to_account_id", toID.String()))
		RespondError(w, r, http.StatusInternalServerError, "transfer/internal-failure", "Transfer failed")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(tx)
}

func (h *TransferHandler) MakeExchangeTransfer(w http.ResponseWriter, r *http.Request) {
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		RespondError(w, r, http.StatusBadRequest, "idempotency/missing-key", "Idempotency-Key header is required")
		return
	}

	var req struct {
		FromAccountID string `json:"from_account_id"`
		ToAccountID   string `json:"to_account_id"`
		Amount        int64  `json:"amount"`
		FromCurrency  string `json:"from_currency"`
		ToCurrency    string `json:"to_currency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, r, http.StatusBadRequest, "request/invalid-body", "Invalid request body")
		return
	}

	// Validate inputs
	req.FromCurrency = strings.TrimSpace(req.FromCurrency)
	req.ToCurrency = strings.TrimSpace(req.ToCurrency)

	if req.Amount <= 0 {
		RespondError(w, r, http.StatusBadRequest, "request/invalid-amount", "Amount must be greater than zero")
		return
	}
	if req.FromCurrency == "" || req.ToCurrency == "" {
		RespondError(w, r, http.StatusBadRequest, "request/missing-currency", "from_currency and to_currency are required")
		return
	}

	fromID, err := uuid.Parse(req.FromAccountID)
	if err != nil {
		RespondError(w, r, http.StatusBadRequest, "request/invalid-from-account-id", "Invalid from_account_id")
		return
	}
	toID, err := uuid.Parse(req.ToAccountID)
	if err != nil {
		RespondError(w, r, http.StatusBadRequest, "request/invalid-to-account-id", "Invalid to_account_id")
		return
	}
	actorID, isAdmin, err := requestActor(r)
	if err != nil {
		RespondError(w, r, http.StatusUnauthorized, "auth/unauthorized", "Unauthorized")
		return
	}
	fromAcc, err := h.repo.GetAccount(r.Context(), fromID)
	if err != nil {
		RespondError(w, r, http.StatusBadRequest, "request/invalid-from-account-id", "Invalid from_account_id")
		return
	}
	if !isAdmin && fromAcc.UserID != actorID {
		RespondError(w, r, http.StatusForbidden, "auth/insufficient-permissions", "insufficient permissions")
		return
	}

	cmd := service.TransferExchangeCmd{
		FromAccountID: fromID,
		ToAccountID:   toID,
		Amount:        req.Amount,
		FromCurrency:  req.FromCurrency,
		ToCurrency:    req.ToCurrency,
		ReferenceID:   idempotencyKey,
	}

	tx, err := h.svc.TransferExchange(r.Context(), cmd)
	if err != nil {
		// Map errors to status codes
		if errors.Is(err, models.ErrInsufficientFunds) {
			RespondError(w, r, http.StatusBadRequest, "transfer/insufficient-funds", err.Error())
			return
		}
		if errors.Is(err, models.ErrUnsupportedCurrency) || errors.Is(err, models.ErrRateUnavailable) {
			RespondError(w, r, http.StatusBadRequest, "transfer/exchange-invalid-request", err.Error())
			return
		}
		if errors.Is(err, service.ErrInvalidAmount) || errors.Is(err, service.ErrReferenceRequired) || errors.Is(err, service.ErrSameAccountTransfer) || errors.Is(err, service.ErrSameCurrencyExchange) {
			RespondError(w, r, http.StatusBadRequest, "transfer/exchange-invalid-request", err.Error())
			return
		}

		zap.L().Error("exchange transfer failed", zap.Error(err))

		RespondError(w, r, http.StatusInternalServerError, "transfer/exchange-failed", "Exchange failed")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(tx)
}
