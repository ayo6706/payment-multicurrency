package handler

import (
	"errors"
	"io"
	"net/http"

	"github.com/ayo6706/payment-multicurrency/internal/service"
	"go.uber.org/zap"
)

// WebhookHandler handles incoming webhook events from external systems.
type WebhookHandler struct {
	webhookSvc *service.WebhookService
}

// NewWebhookHandler creates a new WebhookHandler instance.
func NewWebhookHandler(webhookSvc *service.WebhookService) *WebhookHandler {
	return &WebhookHandler{
		webhookSvc: webhookSvc,
	}
}

// HandleDepositWebhook handles POST /v1/webhooks/deposit
// It verifies the HMAC signature and processes the deposit.
func (h *WebhookHandler) HandleDepositWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		zap.L().Error("read webhook body failed", zap.Error(err))
		RespondError(w, r, http.StatusBadRequest, "request/invalid-body", "Failed to read request body")
		return
	}

	signature := r.Header.Get("X-Webhook-Signature")

	// Call service to process the webhook
	resp, err := h.webhookSvc.HandleDepositWebhook(r.Context(), body, signature)
	if err != nil {
		zap.L().Error("process deposit webhook failed", zap.Error(err))
		if errors.Is(err, service.ErrInvalidSignature) {
			RespondError(w, r, http.StatusUnauthorized, "webhook/invalid-signature", "Invalid signature")
			return
		}
		if errors.Is(err, service.ErrDepositInProgress) {
			RespondError(w, r, http.StatusConflict, "webhook/in-progress", "Deposit for this reference is still processing")
			return
		}
		if errors.Is(err, service.ErrDepositPayloadMismatch) {
			RespondError(w, r, http.StatusConflict, "webhook/reference-mismatch", "Reference already used with different payload")
			return
		}
		if errors.Is(err, service.ErrInvalidWebhookPayload) {
			RespondError(w, r, http.StatusBadRequest, "webhook/invalid-request", "Invalid webhook payload")
			return
		}
		RespondError(w, r, http.StatusInternalServerError, "webhook/internal-failure", "Failed to process webhook")
		return
	}

	RespondJSON(w, http.StatusOK, resp)
}
