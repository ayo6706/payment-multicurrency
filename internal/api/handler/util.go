package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/ayo6706/payment-multicurrency/internal/api/middleware"
	"github.com/ayo6706/payment-multicurrency/internal/api/problem"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// RespondJSON writes a JSON response.
func RespondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// RespondError writes an error response.
func RespondError(w http.ResponseWriter, r *http.Request, status int, problemType, message string) {
	if problemType != "" && problemType != "about:blank" && !strings.HasPrefix(problemType, "http") {
		problemType = problem.Type(problemType)
	}
	problem.Write(w, r, status, problemType, http.StatusText(status), message)
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

func mapDBError(err error) (status int, problemType, message string, ok bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return 0, "", "", false
	}

	switch pgErr.Code {
	case "23505": // unique_violation
		return http.StatusConflict, "db/unique-violation", "resource already exists", true
	case "23503": // foreign_key_violation
		return http.StatusBadRequest, "db/foreign-key-violation", "invalid reference", true
	case "23514": // check_violation
		return http.StatusBadRequest, "db/check-violation", "request violates data constraints", true
	case "23502": // not_null_violation
		return http.StatusBadRequest, "db/not-null-violation", "missing required field", true
	default:
		return 0, "", "", false
	}
}
