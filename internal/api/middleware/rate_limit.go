package middleware

import (
	"fmt"
	"net/http"
	"time"

	"github.com/ayo6706/payment-multicurrency/internal/api/problem"
	"github.com/go-chi/httprate"
)

// PublicRateLimiter limits requests per IP for unauthenticated routes.
func PublicRateLimiter(rps int) func(http.Handler) http.Handler {
	return httprate.Limit(rps, time.Second,
		httprate.WithLimitHandler(func(w http.ResponseWriter, r *http.Request) {
			problem.Write(
				w,
				r,
				http.StatusTooManyRequests,
				problem.Type("rate-limit-exceeded"),
				http.StatusText(http.StatusTooManyRequests),
				fmt.Sprintf("Rate limit of %d req/s exceeded for this IP", rps),
			)
		}),
	)
}

// AuthRateLimiter limits authenticated users using their user ID as the key.
func AuthRateLimiter(rps int) func(http.Handler) http.Handler {
	return httprate.Limit(rps, time.Second,
		httprate.WithKeyFuncs(func(r *http.Request) (string, error) {
			if userID := UserIDFromContext(r.Context()); userID != "" {
				return userID, nil
			}
			return httprate.KeyByIP(r)
		}),
		httprate.WithLimitHandler(func(w http.ResponseWriter, r *http.Request) {
			problem.Write(
				w,
				r,
				http.StatusTooManyRequests,
				problem.Type("rate-limit-exceeded"),
				http.StatusText(http.StatusTooManyRequests),
				fmt.Sprintf("Rate limit of %d req/s exceeded for this user", rps),
			)
		}),
	)
}
