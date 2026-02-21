package handler

import (
	"encoding/json"
	"net/http"

	"github.com/ayo6706/payment-multicurrency/internal/models"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type UserHandler struct {
	repo *repository.Repository
}

func NewUserHandler(repo *repository.Repository) *UserHandler {
	return &UserHandler{repo: repo}
}

func (h *UserHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Email    string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, r, http.StatusBadRequest, "request/invalid-body", "Invalid request body")
		return
	}

	user := &models.User{
		ID:       uuid.New(),
		Username: req.Username,
		Email:    req.Email,
		Role:     "user",
	}
	if err := h.repo.CreateUser(r.Context(), user); err != nil {
		if status, pType, msg, ok := mapDBError(err); ok {
			RespondError(w, r, status, pType, msg)
			return
		}
		zap.L().Error("create user failed", zap.Error(err), zap.String("email", req.Email))
		RespondError(w, r, http.StatusInternalServerError, "user/create-failed", "Failed to create user")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(user)
}
