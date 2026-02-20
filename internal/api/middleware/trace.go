package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

// TraceMiddleware ensures each request has a trace identifier propagated via context and headers.
func TraceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get("X-Trace-ID")
		if traceID == "" {
			traceID = uuid.NewString()
		}
		ctx := contextWithTraceID(r.Context(), traceID)
		w.Header().Set("X-Trace-ID", traceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func contextWithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceContextKey, traceID)
}
