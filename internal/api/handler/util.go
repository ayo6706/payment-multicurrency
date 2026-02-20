package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ayo6706/payment-multicurrency/internal/api/middleware"
	"github.com/google/uuid"
)

// RespondJSON writes a JSON response.
func RespondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// RespondError writes an error response.
func RespondError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func requestActor(r *http.Request) (uuid.UUID, bool, error) {
	userID := middleware.UserIDFromContext(r.Context())
	if userID == "" {
		return uuid.Nil, false, errors.New("missing user in auth context")
	}

	actorID, err := uuid.Parse(userID)
	if err != nil {
		return uuid.Nil, false, errors.New("invalid user_id in auth context")
	}

	return actorID, middleware.UserRoleFromContext(r.Context()) == "admin", nil
}
