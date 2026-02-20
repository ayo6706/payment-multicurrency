package handler

import (
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
		RespondError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}

	signature := r.Header.Get("X-Webhook-Signature")

	// Call service to process the webhook
	resp, err := h.webhookSvc.HandleDepositWebhook(r.Context(), body, signature)
	if err != nil {
		zap.L().Error("process deposit webhook failed", zap.Error(err))
		if err.Error() == "invalid signature" {
			RespondError(w, http.StatusUnauthorized, "Invalid signature")
			return
		}
		RespondError(w, http.StatusBadRequest, err.Error())
		return
	}

	RespondJSON(w, http.StatusOK, resp)
}
