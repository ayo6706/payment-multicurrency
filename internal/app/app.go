package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ayo6706/payment-multicurrency/internal/api"
	"github.com/ayo6706/payment-multicurrency/internal/db"
	"github.com/ayo6706/payment-multicurrency/internal/gateway"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/ayo6706/payment-multicurrency/internal/service"
	"github.com/ayo6706/payment-multicurrency/internal/worker"
	"github.com/joho/godotenv"
)

// Run bootstraps the HTTP server and payout worker, blocking until shutdown.
func Run() error {
	_ = godotenv.Load()

	log.Println("Starting Payment System...")

	pool, err := db.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer pool.Close()
	log.Println("Connected to Database successfully!")

	repo := repository.NewRepository(pool)

	mockGateway := gateway.NewMockGateway()
	payoutSvc := service.NewPayoutService(repo, pool, mockGateway)

	payoutWorker := worker.NewPayoutWorker(payoutSvc)

	if interval := os.Getenv("PAYOUT_POLL_INTERVAL"); interval != "" {
		if d, err := time.ParseDuration(interval); err == nil {
			payoutWorker.WithPollInterval(d)
		}
	}
	if batchSize := os.Getenv("PAYOUT_BATCH_SIZE"); batchSize != "" {
		var size int32
		if _, err := fmt.Sscanf(batchSize, "%d", &size); err == nil && size > 0 {
			payoutWorker.WithBatchSize(size)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stopWorker := payoutWorker.Run(ctx)
	log.Println("[Worker] Payout worker started")

	router := api.NewRouter(pool, repo)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      router.Routes(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Printf("Server starting on port %s", port)
		serverErr <- server.ListenAndServe()
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	select {
	case <-sigChan:
		log.Println("Shutdown signal received, stopping...")
	case err := <-serverErr:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("server error: %w", err)
		}
	}

	log.Println("Stopping payout worker...")
	stopWorker()

	log.Println("Shutting down HTTP server...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Error during server shutdown: %v", err)
	}

	log.Println("Shutdown complete")
	return nil
}
