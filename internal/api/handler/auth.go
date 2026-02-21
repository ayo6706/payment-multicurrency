package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/ayo6706/payment-multicurrency/internal/api/middleware"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type AuthHandler struct {
	repo *repository.Repository
}

func NewAuthHandler(repo *repository.Repository) *AuthHandler {
	return &AuthHandler{repo: repo}
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID string `json:"user_id"` // Mock login by UserID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, r, http.StatusBadRequest, "request/invalid-body", "Invalid request body")
		return
	}

	uid, err := uuid.Parse(req.UserID)
	if err != nil {
		RespondError(w, r, http.StatusBadRequest, "request/invalid-user-id", "Invalid user_id")
		return
	}

	user, err := h.repo.GetUser(r.Context(), uid)
	if err != nil {
		RespondError(w, r, http.StatusNotFound, "auth/user-not-found", "User not found")
		return
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": uid.String(),
		"role":    user.Role,
		"iss":     middleware.JWTIssuer(),
		"aud":     middleware.JWTAudience(),
		"sub":     uid.String(),
		"iat":     time.Now().Unix(),
		"nbf":     time.Now().Add(-30 * time.Second).Unix(),
		"exp":     time.Now().Add(24 * time.Hour).Unix(),
		"jti":     uuid.NewString(),
	})

	tokenString, err := token.SignedString(middleware.JWTSecret())
	if err != nil {
		RespondError(w, r, http.StatusInternalServerError, "auth/token-sign-failed", "Failed to sign token")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"token": tokenString,
	})
}
