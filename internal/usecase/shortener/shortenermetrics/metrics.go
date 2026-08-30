// Package shortenermetrics implements shortener.CacheMetricsRecorder on
// Prometheus. Kept separate from the shortener package itself so its core
// logic has no Prometheus dependency and stays independently testable
// with a fake recorder.
package shortenermetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"urlshortener/internal/usecase/shortener"
)

// Metrics records cache-aside lookup outcomes to Prometheus.
type Metrics struct {
	cacheLookupsTotal *prometheus.CounterVec
}

// New creates the collector and registers it with reg.
func New(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		cacheLookupsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "shortener_cache_lookups_total",
			Help: "Cache-aside lookups performed by Resolve, labeled by outcome (hit/negative_hit/miss/error).",
		}, []string{"outcome"}),
	}
	reg.MustRegister(m.cacheLookupsTotal)
	return m
}

// ObserveCacheLookup implements shortener.CacheMetricsRecorder.
func (m *Metrics) ObserveCacheLookup(outcome string) {
	m.cacheLookupsTotal.WithLabelValues(outcome).Inc()
}

// Compile-time check that Metrics satisfies the port.
var _ shortener.CacheMetricsRecorder = (*Metrics)(nil)
