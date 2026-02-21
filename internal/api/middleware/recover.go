package middleware

import (
	"net/http"

	"github.com/ayo6706/payment-multicurrency/internal/api/problem"
	"go.uber.org/zap"
)

// RecoverMiddleware converts panics into RFC 7807 responses and logs stack context.
func RecoverMiddleware(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic recovered",
						zap.Any("panic", rec),
						zap.String("path", r.URL.Path),
						zap.String("method", r.Method),
						zap.String("request_id", TraceIDFromContext(r.Context())),
					)

					problem.Write(
						w,
						r,
						http.StatusInternalServerError,
						problem.Type("internal-server-error"),
						http.StatusText(http.StatusInternalServerError),
						"unexpected server error",
					)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
