package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ayo6706/payment-multicurrency/internal/api"
	"github.com/ayo6706/payment-multicurrency/internal/api/middleware"
	"github.com/ayo6706/payment-multicurrency/internal/config"
	"github.com/ayo6706/payment-multicurrency/internal/db"
	"github.com/ayo6706/payment-multicurrency/internal/gateway"
	"github.com/ayo6706/payment-multicurrency/internal/idempotency"
	"github.com/ayo6706/payment-multicurrency/internal/observability"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/ayo6706/payment-multicurrency/internal/service"
	"github.com/ayo6706/payment-multicurrency/internal/worker"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Run bootstraps the HTTP server and payout worker, blocking until shutdown.
func Run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger, err := newLogger(cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer logger.Sync()
	zap.ReplaceGlobals(logger)
	observability.Init()
	middleware.SetJWTSecret(cfg.JWTSecret)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()

	redisClient, err := newRedisClient(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis: %w", err)
	}
	defer redisClient.Close()

	idemStore := idempotency.NewStore(redisClient, pool, cfg.IdempotencyTTL)
	repo := repository.NewRepository(pool)
	store := repository.NewStore(pool)

	mockGateway := gateway.NewMockGateway()
	payoutSvc := service.NewPayoutService(store, mockGateway)
	payoutWorker := worker.NewPayoutWorker(payoutSvc)
	payoutWorker.WithPollInterval(cfg.PayoutPollInterval)
	payoutWorker.WithBatchSize(cfg.PayoutBatchSize)

	stopWorker := payoutWorker.Run(ctx)
	logger.Info("payout worker started", zap.Duration("interval", cfg.PayoutPollInterval), zap.Int32("batch", cfg.PayoutBatchSize))

	router := api.NewRouter(cfg, logger, pool, repo, idemStore, redisClient)

	server := &http.Server{
		Addr:         ":" + cfg.HTTPPort,
		Handler:      router.Routes(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("http server starting", zap.String("port", cfg.HTTPPort))
		serverErr <- server.ListenAndServe()
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	select {
	case <-sigChan:
		logger.Info("shutdown signal received")
	case err := <-serverErr:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("server error: %w", err)
		}
	}

	logger.Info("stopping payout worker")
	stopWorker()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("http server shutdown failed", zap.Error(err))
	}

	logger.Info("shutdown complete")
	return nil
}

func newLogger(level string) (*zap.Logger, error) {
	cfg := zap.NewProductionConfig()
	switch strings.ToLower(level) {
	case "debug":
		cfg.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "info", "":
		cfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	case "warn":
		cfg.Level = zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		cfg.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	default:
		cfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}
	return cfg.Build()
}

func newRedisClient(url string) (*redis.Client, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	client := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return client, nil
}
