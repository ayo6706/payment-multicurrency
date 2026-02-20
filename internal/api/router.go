package api

import (
	"github.com/ayo6706/payment-multicurrency/internal/api/handler"
	"github.com/ayo6706/payment-multicurrency/internal/api/middleware"
	"github.com/ayo6706/payment-multicurrency/internal/config"
	"github.com/ayo6706/payment-multicurrency/internal/gateway"
	"github.com/ayo6706/payment-multicurrency/internal/idempotency"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/ayo6706/payment-multicurrency/internal/service"
	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type Router struct {
	cfg       *config.Config
	logger    *zap.Logger
	db        *pgxpool.Pool
	repo      *repository.Repository
	idemStore *idempotency.Store
	redis     redis.Cmdable
}

func NewRouter(cfg *config.Config, logger *zap.Logger, db *pgxpool.Pool, repo *repository.Repository, idemStore *idempotency.Store, redis redis.Cmdable) *Router {
	return &Router{cfg: cfg, logger: logger, db: db, repo: repo, idemStore: idemStore, redis: redis}
}

func (api *Router) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(chiMiddleware.Recoverer)
	r.Use(middleware.TraceMiddleware)
	r.Use(middleware.LoggingMiddleware(api.logger))
	r.Use(middleware.MetricsMiddleware)

	mockFX := service.NewMockExchangeRateService()
	store := repository.NewStore(api.db)
	transferSvc := service.NewTransferService(store, mockFX)
	accountSvc := service.NewAccountService(api.repo)
	mockGateway := gateway.NewMockGateway()
	payoutSvc := service.NewPayoutService(store, mockGateway)
	webhookSvc := service.NewWebhookService(store, api.cfg.WebhookHMACKey, api.cfg.WebhookSkipSignature)

	authHandler := handler.NewAuthHandler(api.repo)
	userHandler := handler.NewUserHandler(api.repo)
	accountHandler := handler.NewAccountHandler(accountSvc)
	transferHandler := handler.NewTransferHandler(transferSvc, api.repo)
	payoutHandler := handler.NewPayoutHandler(payoutSvc)
	webhookHandler := handler.NewWebhookHandler(webhookSvc)
	healthHandler := handler.NewHealthHandler(api.db, api.redis)

	r.Group(func(public chi.Router) {
		public.Use(middleware.PublicRateLimiter(api.cfg.PublicRateLimitRPS))

		public.Post("/v1/auth/login", authHandler.Login)
		public.Post("/v1/users", userHandler.CreateUser)
		public.Post("/v1/webhooks/deposit", webhookHandler.HandleDepositWebhook)
		public.Get("/health/live", healthHandler.Live)
		public.Get("/health/ready", healthHandler.Ready)
		public.Handle("/metrics", promhttp.Handler())
	})

	r.Group(func(auth chi.Router) {
		auth.Use(middleware.AuthMiddleware)
		auth.Use(middleware.AuthRateLimiter(api.cfg.AuthRateLimitRPS))

		auth.Post("/v1/accounts", accountHandler.CreateAccount)
		auth.Get("/v1/accounts/{id}/balance", accountHandler.GetBalance)
		auth.Get("/v1/accounts/{id}/statement", accountHandler.GetStatement)

		auth.With(middleware.IdempotencyMiddleware(api.idemStore, api.logger)).Post("/v1/transfers/internal", transferHandler.MakeInternalTransfer)
		auth.With(middleware.IdempotencyMiddleware(api.idemStore, api.logger)).Post("/v1/transfers/exchange", transferHandler.MakeExchangeTransfer)

		auth.With(middleware.IdempotencyMiddleware(api.idemStore, api.logger), middleware.RequireRole("admin")).Post("/v1/payouts", payoutHandler.CreatePayout)
		auth.Get("/v1/payouts/{id}", payoutHandler.GetPayout)
	})

	return r
}
