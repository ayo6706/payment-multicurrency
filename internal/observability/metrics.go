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

		prometheus.MustRegister(httpDurationHistogram, ledgerImbalanceCounter)
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
