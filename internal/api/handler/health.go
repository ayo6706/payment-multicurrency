package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// HealthHandler exposes Kubernetes-style liveness and readiness endpoints.
type HealthHandler struct {
	db    *pgxpool.Pool
	redis redis.Cmdable
}

func NewHealthHandler(db *pgxpool.Pool, redis redis.Cmdable) *HealthHandler {
	return &HealthHandler{db: db, redis: redis}
}

// Live always reports OK – if the process is up, it's live.
func (h *HealthHandler) Live(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// Ready checks dependencies like DB and Redis.
func (h *HealthHandler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 1*time.Second)
	defer cancel()

	if err := h.db.Ping(ctx); err != nil {
		RespondError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	if h.redis != nil {
		if err := h.redis.Ping(ctx).Err(); err != nil {
			RespondError(w, http.StatusServiceUnavailable, "redis unavailable")
			return
		}
	}

	RespondJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
