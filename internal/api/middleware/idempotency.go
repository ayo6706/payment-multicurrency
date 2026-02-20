package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"

	"github.com/ayo6706/payment-multicurrency/internal/idempotency"
	"go.uber.org/zap"
)

var idempotentMethods = map[string]struct{}{
	http.MethodPost:   {},
	http.MethodPut:    {},
	http.MethodPatch:  {},
	http.MethodDelete: {},
}

// IdempotencyMiddleware enforces the Idempotency-Key contract for mutating requests.
func IdempotencyMiddleware(store *idempotency.Store, logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := idempotentMethods[r.Method]; !ok || store == nil {
				next.ServeHTTP(w, r)
				return
			}

			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				http.Error(w, "Idempotency-Key header is required", http.StatusBadRequest)
				return
			}

			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Failed to read request body", http.StatusBadRequest)
				return
			}
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

			reqHash := hashRequest(r.Method, r.URL.Path, bodyBytes)
			rec, err := store.Lookup(r.Context(), key, reqHash)
			if err == nil {
				respondFromRecord(w, rec)
				return
			}
			if errors.Is(err, idempotency.ErrHashMismatch) {
				http.Error(w, "conflicting idempotency key", http.StatusConflict)
				return
			}
			if errors.Is(err, idempotency.ErrInProgress) {
				rec, waitErr := store.WaitForCompletion(r.Context(), key, reqHash)
				if waitErr == nil {
					respondFromRecord(w, rec)
					return
				}
				logger.Warn("idempotency wait failed", zap.Error(waitErr))
				http.Error(w, "idempotency processing", http.StatusConflict)
				return
			}
			if err != idempotency.ErrNotFound {
				logger.Warn("idempotency lookup failed", zap.Error(err))
			}

			reserved, err := store.Reserve(r.Context(), key, reqHash, r.Method, r.URL.Path)
			if err != nil {
				logger.Error("idempotency reserve failed", zap.Error(err))
				http.Error(w, "idempotency unavailable", http.StatusInternalServerError)
				return
			}
			if !reserved {
				rec, waitErr := store.WaitForCompletion(r.Context(), key, reqHash)
				if waitErr == nil {
					respondFromRecord(w, rec)
					return
				}
				logger.Warn("idempotency wait failed", zap.Error(waitErr))
				http.Error(w, "idempotency processing", http.StatusConflict)
				return
			}

			recorder := &bodyRecorder{ResponseWriter: w}
			next.ServeHTTP(recorder, r)

			contentType := recorder.Header().Get("Content-Type")
			if contentType == "" {
				contentType = "application/json"
			}

			if recorder.status == 0 {
				recorder.status = http.StatusOK
			}

			if _, err := store.Finalize(r.Context(), key, reqHash, recorder.status, recorder.body.Bytes(), contentType); err != nil {
				logger.Warn("idempotency finalize failed", zap.Error(err), zap.String("key", key))
			}
		})
	}
}

func hashRequest(method, path string, body []byte) string {
	sum := sha256.Sum256(append([]byte(method+"|"+path+"|"), body...))
	return hex.EncodeToString(sum[:])
}

type bodyRecorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (br *bodyRecorder) WriteHeader(code int) {
	br.status = code
	br.ResponseWriter.WriteHeader(code)
}

func (br *bodyRecorder) Write(b []byte) (int, error) {
	if br.status == 0 {
		br.status = http.StatusOK
	}
	br.body.Write(b)
	return br.ResponseWriter.Write(b)
}

func respondFromRecord(w http.ResponseWriter, rec *idempotency.Record) {
	w.Header().Set("Content-Type", rec.ContentType)
	w.Header().Set("X-Idempotent-Replay", rec.ServedBy)
	w.WriteHeader(rec.Status)
	_, _ = w.Write(rec.Body)
}
