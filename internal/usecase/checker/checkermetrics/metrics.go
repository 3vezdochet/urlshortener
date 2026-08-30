// Package checkermetrics implements checker.MetricsRecorder on
// Prometheus. Kept separate from the checker package itself so checker's
// core logic has no Prometheus dependency and stays independently
// testable with a fake recorder.
package checkermetrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"urlshortener/internal/usecase/checker"
)

// Metrics records checker observations to Prometheus.
type Metrics struct {
	checksTotal   *prometheus.CounterVec
	checkDuration prometheus.Histogram
	batchSize     prometheus.Histogram
}

// New creates the checker's collectors and registers them with reg.
func New(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		checksTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "checker_checks_total",
			Help: "Total availability checks performed, labeled by result (up/down).",
		}, []string{"result"}),
		checkDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "checker_check_duration_seconds",
			Help:    "Availability check latency in seconds.",
			Buckets: prometheus.DefBuckets,
		}),
		batchSize: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "checker_claimed_batch_size",
			Help:    "Number of due checks claimed per RunOnce call.",
			Buckets: []float64{0, 1, 5, 10, 25, 50, 100, 250},
		}),
	}
	reg.MustRegister(m.checksTotal, m.checkDuration, m.batchSize)
	return m
}

// ObserveBatch implements checker.MetricsRecorder.
func (m *Metrics) ObserveBatch(n int) {
	m.batchSize.Observe(float64(n))
}

// ObserveCheck implements checker.MetricsRecorder.
func (m *Metrics) ObserveCheck(up bool, duration time.Duration) {
	result := "down"
	if up {
		result = "up"
	}
	m.checksTotal.WithLabelValues(result).Inc()
	m.checkDuration.Observe(duration.Seconds())
}

// Compile-time check that Metrics satisfies the port.
var _ checker.MetricsRecorder = (*Metrics)(nil)
