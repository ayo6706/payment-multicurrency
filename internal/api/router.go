package api

import (
	"os"

	"github.com/ayo6706/payment-multicurrency/internal/api/handler"
	"github.com/ayo6706/payment-multicurrency/internal/api/middleware"
	"github.com/ayo6706/payment-multicurrency/internal/gateway"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/ayo6706/payment-multicurrency/internal/service"
	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Router struct {
	db   *pgxpool.Pool
	repo *repository.Repository
}

func NewRouter(db *pgxpool.Pool, repo *repository.Repository) *Router {
	return &Router{db: db, repo: repo}
}

func (api *Router) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(chiMiddleware.Logger)
	r.Use(chiMiddleware.Recoverer)

	// Services
	mockFX := service.NewMockExchangeRateService()
	transferSvc := service.NewTransferService(api.repo, api.db, mockFX)
	accountSvc := service.NewAccountService(api.repo)

	// Payout and Webhook Services
	mockGateway := gateway.NewMockGateway()
	payoutSvc := service.NewPayoutService(api.repo, api.db, mockGateway)

	// Get HMAC key from environment for webhook signature verification
	webhookHMACKey := "dev-key-change-in-production" // Default for development
	if key := os.Getenv("WEBHOOK_HMAC_KEY"); key != "" {
		webhookHMACKey = key
	}
	webhookSkipSig := os.Getenv("WEBHOOK_SKIP_SIG") == "true"
	webhookSvc := service.NewWebhookService(api.repo, api.db, webhookHMACKey, webhookSkipSig)

	// Handlers
	authHandler := handler.NewAuthHandler()
	userHandler := handler.NewUserHandler(api.repo)
	accountHandler := handler.NewAccountHandler(accountSvc)
	transferHandler := handler.NewTransferHandler(transferSvc)
	payoutHandler := handler.NewPayoutHandler(payoutSvc)
	webhookHandler := handler.NewWebhookHandler(webhookSvc)

	// Public Routes
	r.Post("/v1/auth/login", authHandler.Login)
	r.Post("/v1/users", userHandler.CreateUser)

	// Webhook endpoint (public, authenticated via HMAC signature)
	r.Post("/v1/webhooks/deposit", webhookHandler.HandleDepositWebhook)

	// Protected Routes
	r.Group(func(r chi.Router) {
		r.Use(middleware.AuthMiddleware)

		// Accounts
		r.Post("/v1/accounts", accountHandler.CreateAccount)
		r.Get("/v1/accounts/{id}/balance", accountHandler.GetBalance)
		r.Get("/v1/accounts/{id}/statement", accountHandler.GetStatement)

		// Transfers
		r.With(middleware.IdempotencyMiddleware).Post("/v1/transfers/internal", transferHandler.MakeInternalTransfer)
		r.With(middleware.IdempotencyMiddleware).Post("/v1/transfers/exchange", transferHandler.MakeExchangeTransfer)

		// Payouts (async)
		r.With(middleware.IdempotencyMiddleware).Post("/v1/payouts", payoutHandler.CreatePayout)
		r.Get("/v1/payouts/{id}", payoutHandler.GetPayout)
	})

	return r
}
