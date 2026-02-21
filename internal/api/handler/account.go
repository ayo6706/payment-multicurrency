package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/ayo6706/payment-multicurrency/internal/service"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type AccountHandler struct {
	svc *service.AccountService
}

func NewAccountHandler(svc *service.AccountService) *AccountHandler {
	return &AccountHandler{svc: svc}
}

func (h *AccountHandler) GetBalance(w http.ResponseWriter, r *http.Request) {
	actorID, isAdmin, err := requestActor(r)
	if err != nil {
		RespondError(w, r, http.StatusUnauthorized, "auth/unauthorized", "Unauthorized")
		return
	}

	accountIDStr := chi.URLParam(r, "id")
	accountID, err := uuid.Parse(accountIDStr)
	if err != nil {
		RespondError(w, r, http.StatusBadRequest, "request/invalid-account-id", "Invalid account ID")
		return
	}

	account, err := h.svc.GetBalance(r.Context(), accountID)
	if err != nil {
		zap.L().Error("get balance failed", zap.Error(err), zap.String("account_id", accountID.String()))
		RespondError(w, r, http.StatusInternalServerError, "account/balance-read-failed", "Failed to get balance")
		return
	}
	if !isAdmin && account.UserID != actorID {
		RespondError(w, r, http.StatusForbidden, "auth/insufficient-permissions", "insufficient permissions")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(account)
}

func (h *AccountHandler) GetStatement(w http.ResponseWriter, r *http.Request) {
	actorID, isAdmin, err := requestActor(r)
	if err != nil {
		RespondError(w, r, http.StatusUnauthorized, "auth/unauthorized", "Unauthorized")
		return
	}

	accountIDStr := chi.URLParam(r, "id")
	accountID, err := uuid.Parse(accountIDStr)
	if err != nil {
		RespondError(w, r, http.StatusBadRequest, "request/invalid-account-id", "Invalid account ID")
		return
	}
	account, err := h.svc.GetBalance(r.Context(), accountID)
	if err != nil {
		zap.L().Error("account authorization lookup failed", zap.Error(err), zap.String("account_id", accountID.String()))
		RespondError(w, r, http.StatusInternalServerError, "account/authorization-failed", "Failed to authorize account access")
		return
	}
	if !isAdmin && account.UserID != actorID {
		RespondError(w, r, http.StatusForbidden, "auth/insufficient-permissions", "insufficient permissions")
		return
	}

	pageStr := r.URL.Query().Get("page")
	pageSizeStr := r.URL.Query().Get("page_size")

	page, _ := strconv.Atoi(pageStr)
	pageSize, _ := strconv.Atoi(pageSizeStr)

	entries, err := h.svc.GetStatement(r.Context(), accountID, page, pageSize)
	if err != nil {
		zap.L().Error("get statement failed", zap.Error(err), zap.String("account_id", accountID.String()))
		RespondError(w, r, http.StatusInternalServerError, "account/statement-read-failed", "Failed to get statement")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

func (h *AccountHandler) CreateAccount(w http.ResponseWriter, r *http.Request) {
	actorID, isAdmin, err := requestActor(r)
	if err != nil {
		RespondError(w, r, http.StatusUnauthorized, "auth/unauthorized", "Unauthorized")
		return
	}

	var req struct {
		UserID   string `json:"user_id"`
		Currency string `json:"currency"`
		Balance  int64  `json:"balance"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, r, http.StatusBadRequest, "request/invalid-body", "Invalid request body")
		return
	}

	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		RespondError(w, r, http.StatusBadRequest, "request/invalid-user-id", "Invalid user_id")
		return
	}
	if !isAdmin && userID != actorID {
		RespondError(w, r, http.StatusForbidden, "auth/insufficient-permissions", "insufficient permissions")
		return
	}

	account, err := h.svc.CreateAccount(r.Context(), userID, req.Currency, req.Balance)
	if err != nil {
		if status, pType, msg, ok := mapDBError(err); ok {
			RespondError(w, r, status, pType, msg)
			return
		}
		zap.L().Error("create account failed", zap.Error(err), zap.String("user_id", userID.String()))
		RespondError(w, r, http.StatusInternalServerError, "account/create-failed", "Failed to create account")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(account)
}
