package middleware

import (
	"net/http"
	"time"

	"go.uber.org/zap"
)

// LoggingMiddleware emits structured request logs enriched with the trace id.
func LoggingMiddleware(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rw, r)

			logger.Info("http_request",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", rw.status),
				zap.String("trace_id", TraceIDFromContext(r.Context())),
				zap.Duration("duration", time.Since(start)),
			)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(b []byte) (int, error) {
	if sr.status == 0 {
		sr.status = http.StatusOK
	}
	return sr.ResponseWriter.Write(b)
}
