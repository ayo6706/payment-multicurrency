package observability

import (
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	registerOnce           sync.Once
	httpDurationHistogram  *prometheus.HistogramVec
	ledgerImbalanceCounter *prometheus.CounterVec
	idempotencyCounter     *prometheus.CounterVec
	manualReviewQueueGauge prometheus.Gauge
	manualReviewCounter    *prometheus.CounterVec
	workerRunCounter       *prometheus.CounterVec
)

// Init registers all Prometheus collectors.
func Init() {
	registerOnce.Do(func() {
		httpDurationHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "path", "status"})

		ledgerImbalanceCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ledger_imbalance_total",
			Help: "Number of times double-entry balances diverged",
		}, []string{"currency"})

		idempotencyCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "idempotency_events_total",
			Help: "Idempotency middleware outcomes",
		}, []string{"outcome"})

		manualReviewQueueGauge = prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "payout_manual_review_queue_size",
			Help: "Current number of payouts waiting in manual review",
		})

		manualReviewCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "payout_manual_review_transitions_total",
			Help: "Manual review transitions and resolutions",
		}, []string{"action"})

		workerRunCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worker_runs_total",
			Help: "Background worker run outcomes",
		}, []string{"worker", "result"})

		prometheus.MustRegister(
			httpDurationHistogram,
			ledgerImbalanceCounter,
			idempotencyCounter,
			manualReviewQueueGauge,
			manualReviewCounter,
			workerRunCounter,
		)
	})
}

func ObserveHTTP(method, path string, status int, duration time.Duration) {
	if httpDurationHistogram == nil {
		return
	}
	httpDurationHistogram.WithLabelValues(method, path, strconv.Itoa(status)).Observe(duration.Seconds())
}

func IncrementLedgerImbalance(currency string) {
	if ledgerImbalanceCounter == nil {
		return
	}
	ledgerImbalanceCounter.WithLabelValues(currency).Inc()
}

func IncrementIdempotencyEvent(outcome string) {
	if idempotencyCounter == nil {
		return
	}
	idempotencyCounter.WithLabelValues(outcome).Inc()
}

func SetManualReviewQueueSize(size int64) {
	if manualReviewQueueGauge == nil {
		return
	}
	manualReviewQueueGauge.Set(float64(size))
}

func IncrementManualReviewTransition(action string) {
	if manualReviewCounter == nil {
		return
	}
	manualReviewCounter.WithLabelValues(action).Inc()
}

func IncrementWorkerRun(worker, result string) {
	if workerRunCounter == nil {
		return
	}
	workerRunCounter.WithLabelValues(worker, result).Inc()
}
