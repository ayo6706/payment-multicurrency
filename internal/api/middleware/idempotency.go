package middleware

import (
	"net/http"
)

// IdempotencyMiddleware ensures that the Idempotency-Key header is present for mutating requests.
// For Issue 4, this is a basic check.
func IdempotencyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				http.Error(w, "Idempotency-Key header is required", http.StatusBadRequest)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
