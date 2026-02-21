package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"

	"github.com/ayo6706/payment-multicurrency/internal/api/problem"
	"github.com/ayo6706/payment-multicurrency/internal/idempotency"
	"github.com/ayo6706/payment-multicurrency/internal/observability"
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
				observability.IncrementIdempotencyEvent("missing_key")
				problem.Write(w, r, http.StatusBadRequest, problem.Type("idempotency/missing-key"), http.StatusText(http.StatusBadRequest), "Idempotency-Key header is required")
				return
			}

			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				problem.Write(w, r, http.StatusBadRequest, problem.Type("request/invalid-body"), http.StatusText(http.StatusBadRequest), "Failed to read request body")
				return
			}
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

			reqHash := hashRequest(r.Method, r.URL.Path, bodyBytes)
			rec, err := store.Lookup(r.Context(), key, reqHash)
			if err == nil {
				observability.IncrementIdempotencyEvent("replay")
				respondFromRecord(w, rec)
				return
			}
			if errors.Is(err, idempotency.ErrHashMismatch) {
				observability.IncrementIdempotencyEvent("hash_mismatch")
				problem.Write(w, r, http.StatusConflict, problem.Type("idempotency/key-conflict"), http.StatusText(http.StatusConflict), "conflicting idempotency key")
				return
			}
			if errors.Is(err, idempotency.ErrInProgress) {
				rec, waitErr := store.WaitForCompletion(r.Context(), key, reqHash)
				if waitErr == nil {
					observability.IncrementIdempotencyEvent("replay_after_wait")
					respondFromRecord(w, rec)
					return
				}
				observability.IncrementIdempotencyEvent("in_progress_conflict")
				logger.Warn("idempotency wait failed", zap.Error(waitErr))
				problem.Write(w, r, http.StatusConflict, problem.Type("idempotency/in-progress"), http.StatusText(http.StatusConflict), "idempotency processing")
				return
			}
			if err != idempotency.ErrNotFound {
				observability.IncrementIdempotencyEvent("lookup_error")
				logger.Warn("idempotency lookup failed", zap.Error(err))
			}

			reserved, err := store.Reserve(r.Context(), key, reqHash, r.Method, r.URL.Path)
			if err != nil {
				observability.IncrementIdempotencyEvent("reserve_error")
				logger.Error("idempotency reserve failed", zap.Error(err))
				problem.Write(w, r, http.StatusInternalServerError, problem.Type("idempotency/unavailable"), http.StatusText(http.StatusInternalServerError), "idempotency unavailable")
				return
			}
			if !reserved {
				rec, waitErr := store.WaitForCompletion(r.Context(), key, reqHash)
				if waitErr == nil {
					observability.IncrementIdempotencyEvent("replay_after_reserve")
					respondFromRecord(w, rec)
					return
				}
				observability.IncrementIdempotencyEvent("in_progress_conflict")
				logger.Warn("idempotency wait failed", zap.Error(waitErr))
				problem.Write(w, r, http.StatusConflict, problem.Type("idempotency/in-progress"), http.StatusText(http.StatusConflict), "idempotency processing")
				return
			}
			observability.IncrementIdempotencyEvent("reserved")

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
				observability.IncrementIdempotencyEvent("finalize_error")
				logger.Warn("idempotency finalize failed", zap.Error(err), zap.String("key", key))
			} else {
				observability.IncrementIdempotencyEvent("finalized")
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
