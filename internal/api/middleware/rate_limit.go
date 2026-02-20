package middleware

import (
	"net/http"
	"time"

	"github.com/go-chi/httprate"
)

// PublicRateLimiter limits requests per IP for unauthenticated routes.
func PublicRateLimiter(rps int) func(http.Handler) http.Handler {
	return httprate.Limit(rps, time.Second)
}

// AuthRateLimiter limits authenticated users using their user ID as the key.
func AuthRateLimiter(rps int) func(http.Handler) http.Handler {
	return httprate.Limit(rps, time.Second, httprate.WithKeyFuncs(func(r *http.Request) (string, error) {
		if userID := UserIDFromContext(r.Context()); userID != "" {
			return userID, nil
		}
		return httprate.KeyByIP(r)
	}))
}
