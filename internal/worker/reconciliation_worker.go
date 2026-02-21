package worker

import (
	"context"
	"sync"
	"time"

	"github.com/ayo6706/payment-multicurrency/internal/observability"
	"github.com/ayo6706/payment-multicurrency/internal/service"
	"go.uber.org/zap"
)

// ReconciliationWorker runs periodic ledger reconciliation checks.
type ReconciliationWorker struct {
	svc      *service.ReconciliationService
	interval time.Duration
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewReconciliationWorker constructs a worker with a default daily interval.
func NewReconciliationWorker(svc *service.ReconciliationService) *ReconciliationWorker {
	return &ReconciliationWorker{
		svc:      svc,
		interval: 24 * time.Hour,
		stopCh:   make(chan struct{}),
	}
}

// WithInterval updates the run interval.
func (w *ReconciliationWorker) WithInterval(interval time.Duration) *ReconciliationWorker {
	if interval > 0 {
		w.interval = interval
	}
	return w
}

// Start blocks and runs reconciliation at the configured interval.
func (w *ReconciliationWorker) Start(ctx context.Context) {
	zap.L().Info("reconciliation worker starting", zap.Duration("interval", w.interval))
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// Run once immediately at startup.
	w.runOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			zap.L().Info("reconciliation worker context canceled")
			return
		case <-w.stopCh:
			zap.L().Info("reconciliation worker stop signal received")
			return
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

// Stop stops the running worker loop.
func (w *ReconciliationWorker) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
}

// Run starts the worker in a goroutine and returns a stop function.
func (w *ReconciliationWorker) Run(ctx context.Context) func() {
	go w.Start(ctx)
	return w.Stop
}

func (w *ReconciliationWorker) runOnce(ctx context.Context) {
	if err := w.svc.Run(ctx); err != nil {
		observability.IncrementWorkerRun("reconciliation", "failed")
		zap.L().Error("reconciliation run failed", zap.Error(err))
		return
	}
	observability.IncrementWorkerRun("reconciliation", "success")
}
