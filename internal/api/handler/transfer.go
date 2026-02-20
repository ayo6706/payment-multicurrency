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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Idempotency-Key header is required"})
		return
	}

	var req struct {
		FromAccountID string `json:"from_account_id"`
		ToAccountID   string `json:"to_account_id"`
		Amount        int64  `json:"amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
		return
	}

	fromID, err := uuid.Parse(req.FromAccountID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid from_account_id"})
		return
	}
	toID, err := uuid.Parse(req.ToAccountID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid to_account_id"})
		return
	}
	actorID, isAdmin, err := requestActor(r)
	if err != nil {
		RespondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	fromAcc, err := h.repo.GetAccount(r.Context(), fromID)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid from_account_id")
		return
	}
	if !isAdmin && fromAcc.UserID != actorID {
		RespondError(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	tx, err := h.svc.Transfer(r.Context(), fromID, toID, req.Amount, idempotencyKey)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		if err == models.ErrInsufficientFunds {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Transfer failed: " + err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(tx)
}

func (h *TransferHandler) MakeExchangeTransfer(w http.ResponseWriter, r *http.Request) {
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Idempotency-Key header is required"})
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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
		return
	}

	// Validate inputs
	req.FromCurrency = strings.TrimSpace(req.FromCurrency)
	req.ToCurrency = strings.TrimSpace(req.ToCurrency)

	if req.Amount <= 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Amount must be greater than zero"})
		return
	}
	if req.FromCurrency == "" || req.ToCurrency == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "from_currency and to_currency are required"})
		return
	}

	fromID, err := uuid.Parse(req.FromAccountID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid from_account_id"})
		return
	}
	toID, err := uuid.Parse(req.ToAccountID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid to_account_id"})
		return
	}
	actorID, isAdmin, err := requestActor(r)
	if err != nil {
		RespondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	fromAcc, err := h.repo.GetAccount(r.Context(), fromID)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid from_account_id")
		return
	}
	if !isAdmin && fromAcc.UserID != actorID {
		RespondError(w, http.StatusForbidden, "insufficient permissions")
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
		w.Header().Set("Content-Type", "application/json")
		// Map errors to status codes
		if errors.Is(err, models.ErrInsufficientFunds) {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		if errors.Is(err, models.ErrUnsupportedCurrency) || errors.Is(err, models.ErrRateUnavailable) {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		zap.L().Error("exchange transfer failed", zap.Error(err))

		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Exchange failed"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(tx)
}
