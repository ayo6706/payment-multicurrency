package middleware

import (
	"net/http"
	"time"

	"github.com/ayo6706/payment-multicurrency/internal/observability"
	"github.com/go-chi/chi/v5"
)

// MetricsMiddleware records request durations for Prometheus instrumentation.
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rw, r)

		pattern := routePattern(r)
		observability.ObserveHTTP(r.Method, pattern, rw.status, time.Since(start))
	})
}

func routePattern(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if pattern := rc.RoutePattern(); pattern != "" {
			return pattern
		}
	}
	return r.URL.Path
}
