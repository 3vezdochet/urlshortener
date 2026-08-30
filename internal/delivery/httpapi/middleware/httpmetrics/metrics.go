// Package httpmetrics implements middleware.HTTPMetricsRecorder on
// Prometheus. Kept separate from the middleware package itself so
// middleware — and everything else in it, like Auth and RateLimit — has
// no Prometheus dependency and stays independently testable.
package httpmetrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"urlshortener/internal/delivery/httpapi/middleware"
)

// Metrics records HTTP request counts and latencies to Prometheus.
type Metrics struct {
	requestsTotal   *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
}

// New creates the collectors and registers them with reg.
func New(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		requestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests processed, labeled by method, route, and status.",
		}, []string{"method", "route", "status"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency in seconds, labeled by method and route.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
	}
	reg.MustRegister(m.requestsTotal, m.requestDuration)
	return m
}

// RecordRequest implements middleware.HTTPMetricsRecorder.
func (m *Metrics) RecordRequest(method, route, status string, duration time.Duration) {
	m.requestsTotal.WithLabelValues(method, route, status).Inc()
	m.requestDuration.WithLabelValues(method, route).Observe(duration.Seconds())
}

// Compile-time check that Metrics satisfies the port.
var _ middleware.HTTPMetricsRecorder = (*Metrics)(nil)
