package api

import (
	"github.com/ayo6706/payment-multicurrency/internal/api/handler"
	"github.com/ayo6706/payment-multicurrency/internal/api/middleware"
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
	transferSvc := service.NewTransferService(api.repo, api.db)
	accountSvc := service.NewAccountService(api.repo)

	// Handlers
	authHandler := handler.NewAuthHandler()
	userHandler := handler.NewUserHandler(api.repo)
	accountHandler := handler.NewAccountHandler(accountSvc)
	transferHandler := handler.NewTransferHandler(transferSvc)

	// Public Routes
	r.Post("/v1/auth/login", authHandler.Login)
	r.Post("/v1/users", userHandler.CreateUser)

	// Protected Routes
	r.Group(func(r chi.Router) {
		r.Use(middleware.AuthMiddleware)

		// Accounts
		r.Post("/v1/accounts", accountHandler.CreateAccount)
		r.Get("/v1/accounts/{id}/balance", accountHandler.GetBalance)
		r.Get("/v1/accounts/{id}/statement", accountHandler.GetStatement)

		// Transfers
		r.With(middleware.IdempotencyMiddleware).Post("/v1/transfers/internal", transferHandler.MakeInternalTransfer)
	})

	return r
}
