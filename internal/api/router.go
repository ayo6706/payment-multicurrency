package api

import (
	"github.com/ayo6706/payment-multicurrency/internal/api/handler"
	"github.com/ayo6706/payment-multicurrency/internal/api/middleware"
	"github.com/ayo6706/payment-multicurrency/internal/api/spec"
	"github.com/ayo6706/payment-multicurrency/internal/config"
	"github.com/ayo6706/payment-multicurrency/internal/idempotency"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/ayo6706/payment-multicurrency/internal/service"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	httpSwagger "github.com/swaggo/http-swagger/v2"
	"go.uber.org/zap"
)

type Router struct {
	cfg         *config.Config
	logger      *zap.Logger
	db          *pgxpool.Pool
	repo        *repository.Repository
	idemStore   *idempotency.Store
	redis       redis.Cmdable
	accountSvc  *service.AccountService
	transferSvc *service.TransferService
	payoutSvc   *service.PayoutService
	webhookSvc  *service.WebhookService
}

func NewRouter(
	cfg *config.Config,
	logger *zap.Logger,
	db *pgxpool.Pool,
	repo *repository.Repository,
	idemStore *idempotency.Store,
	redis redis.Cmdable,
	accountSvc *service.AccountService,
	transferSvc *service.TransferService,
	payoutSvc *service.PayoutService,
	webhookSvc *service.WebhookService,
) *Router {
	return &Router{
		cfg:         cfg,
		logger:      logger,
		db:          db,
		repo:        repo,
		idemStore:   idemStore,
		redis:       redis,
		accountSvc:  accountSvc,
		transferSvc: transferSvc,
		payoutSvc:   payoutSvc,
		webhookSvc:  webhookSvc,
	}
}

func (api *Router) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RecoverMiddleware(api.logger))
	r.Use(middleware.TraceMiddleware)
	r.Use(middleware.LoggingMiddleware(api.logger))
	r.Use(middleware.MetricsMiddleware)

	accountSvc := api.accountSvc
	transferSvc := api.transferSvc
	payoutSvc := api.payoutSvc
	webhookSvc := api.webhookSvc
	if accountSvc == nil || transferSvc == nil || payoutSvc == nil || webhookSvc == nil {
		panic("router dependencies are not configured")
	}

	authHandler := handler.NewAuthHandler(api.repo)
	userHandler := handler.NewUserHandler(api.repo)
	accountHandler := handler.NewAccountHandler(accountSvc)
	transferHandler := handler.NewTransferHandler(transferSvc, api.repo)
	payoutHandler := handler.NewPayoutHandler(payoutSvc, api.repo)
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
		public.Get("/openapi.yaml", spec.OpenAPIHandler())
		public.Get("/swagger/*", httpSwagger.Handler(httpSwagger.URL("/openapi.yaml")))
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
		auth.With(middleware.RequireRole("admin")).Get("/v1/payouts/manual-review", payoutHandler.ListManualReviewPayouts)
		auth.With(middleware.RequireRole("admin")).Post("/v1/payouts/{id}/resolve", payoutHandler.ResolveManualReviewPayout)
		auth.Get("/v1/payouts/{id}", payoutHandler.GetPayout)
	})

	return r
}
