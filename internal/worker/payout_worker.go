package worker

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ayo6706/payment-multicurrency/internal/service"
)

// PayoutWorker processes pending payouts in the background.
// It polls for pending payouts at regular intervals and processes them.
// Safe for concurrent instances thanks to FOR UPDATE SKIP LOCKED.
type PayoutWorker struct {
	payoutService *service.PayoutService
	pollInterval  time.Duration
	batchSize     int32
	stopCh        chan struct{}
}

// NewPayoutWorker creates a new PayoutWorker instance.
func NewPayoutWorker(payoutSvc *service.PayoutService) *PayoutWorker {
	return &PayoutWorker{
		payoutService: payoutSvc,
		pollInterval:  10 * time.Second, // Default: poll every 10 seconds
		batchSize:     10,                // Process up to 10 payouts at a time
		stopCh:        make(chan struct{}),
	}
}

// WithPollInterval sets the poll interval for the worker.
func (w *PayoutWorker) WithPollInterval(interval time.Duration) *PayoutWorker {
	w.pollInterval = interval
	return w
}

// WithBatchSize sets the batch size for the worker.
func (w *PayoutWorker) WithBatchSize(size int32) *PayoutWorker {
	w.batchSize = size
	return w
}

// Start begins the background worker.
// It runs in a loop until Stop is called or the context is canceled.
func (w *PayoutWorker) Start(ctx context.Context) {
	log.Printf("[PayoutWorker] Starting with poll interval: %v, batch size: %d", w.pollInterval, w.batchSize)

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[PayoutWorker] Context canceled, stopping...")
			return
		case <-w.stopCh:
			log.Println("[PayoutWorker] Stop signal received, stopping...")
			return
		case <-ticker.C:
			w.processBatch(ctx)
		}
	}
}

// Stop signals the worker to stop.
func (w *PayoutWorker) Stop() {
	close(w.stopCh)
}

// processBatch processes a single batch of pending payouts.
func (w *PayoutWorker) processBatch(ctx context.Context) {
	err := w.payoutService.ProcessPayouts(ctx, w.batchSize)
	if err != nil {
		log.Printf("[PayoutWorker] Error processing payouts: %v", err)
	}
}

// ProcessOnce processes a single batch immediately.
// Useful for testing or manual triggering.
func (w *PayoutWorker) ProcessOnce(ctx context.Context) error {
	return w.payoutService.ProcessPayouts(ctx, w.batchSize)
}

// Run starts the worker and returns a function that can be called to stop it.
// This is useful for starting the worker in a goroutine.
func (w *PayoutWorker) Run(ctx context.Context) func() {
	go w.Start(ctx)
	return w.Stop
}

// String returns a string representation of the worker.
func (w *PayoutWorker) String() string {
	return fmt.Sprintf("PayoutWorker(interval=%v, batch=%d)", w.pollInterval, w.batchSize)
}
