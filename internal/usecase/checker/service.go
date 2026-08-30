// Package checker implements the availability-checking use case: claiming
// links whose target URL is due for a check, probing them, and recording
// the outcome with the next check scheduled by a fixed interval (healthy)
// or exponential backoff (failing).
package checker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"urlshortener/internal/domain"
)

// Prober performs the actual availability check against a URL. The
// production implementation (httpprobe) issues a real HTTP request;
// tests use a fake.
type Prober interface {
	// Probe reports the HTTP status code received (0 if no response was
	// received at all — timeout, DNS failure, connection refused, ...)
	// and an error, which is non-nil exactly when the target should be
	// considered down.
	Probe(ctx context.Context, url string) (statusCode int, err error)
}

// Clock abstracts time.Now so tests can control the current time.
type Clock func() time.Time

const (
	defaultBatchSize       = 100
	defaultConcurrency     = 10
	defaultLeaseFor        = 2 * time.Minute
	defaultUpInterval      = 10 * time.Minute
	defaultDownInterval    = time.Minute
	defaultMaxDownInterval = time.Hour
)

// Service runs availability checks for links whose next check is due.
type Service struct {
	repo    domain.HealthRepository
	prober  Prober
	now     Clock
	logger  *slog.Logger
	metrics MetricsRecorder // optional; nil disables metrics entirely

	batchSize       int
	concurrency     int
	leaseFor        time.Duration
	upInterval      time.Duration
	downInterval    time.Duration
	maxDownInterval time.Duration
}

// Option configures a Service.
type Option func(*Service)

// WithClock overrides the default time source (time.Now). Useful in tests.
func WithClock(c Clock) Option { return func(s *Service) { s.now = c } }

// WithLogger overrides the logger used for check failures and record
// errors (default slog.Default()).
func WithLogger(l *slog.Logger) Option { return func(s *Service) { s.logger = l } }

// WithBatchSize caps how many due checks RunOnce claims at a time
// (default 100).
func WithBatchSize(n int) Option { return func(s *Service) { s.batchSize = n } }

// WithConcurrency caps how many checks run at once within a batch
// (default 10).
func WithConcurrency(n int) Option { return func(s *Service) { s.concurrency = n } }

// WithLeaseDuration sets how long a claimed check is reserved before it
// becomes claimable again if never recorded — e.g. this process crashed
// mid-check (default 2 minutes).
func WithLeaseDuration(d time.Duration) Option { return func(s *Service) { s.leaseFor = d } }

// WithUpInterval sets how long after a successful check the next one is
// due (default 10 minutes).
func WithUpInterval(d time.Duration) Option { return func(s *Service) { s.upInterval = d } }

// WithDownInterval sets the base backoff interval after the first
// failure; each additional consecutive failure doubles it, up to
// WithMaxDownInterval (default base: 1 minute).
func WithDownInterval(d time.Duration) Option { return func(s *Service) { s.downInterval = d } }

// WithMaxDownInterval caps the exponential backoff for a repeatedly
// failing link (default 1 hour) — otherwise a link down for days would
// eventually get rechecked at absurdly long intervals.
func WithMaxDownInterval(d time.Duration) Option { return func(s *Service) { s.maxDownInterval = d } }

// WithMetrics enables recording checks performed and batch sizes claimed
// to rec (see the checkermetrics package for the Prometheus
// implementation). Without this option, RunOnce records nothing — the
// same "off unless configured" pattern as WithCache in the shortener
// package.
func WithMetrics(rec MetricsRecorder) Option { return func(s *Service) { s.metrics = rec } }

// New creates a Service backed by repo (state) and prober (the actual
// availability check).
func New(repo domain.HealthRepository, prober Prober, opts ...Option) *Service {
	s := &Service{
		repo: repo, prober: prober, now: time.Now, logger: slog.Default(),
		batchSize: defaultBatchSize, concurrency: defaultConcurrency,
		leaseFor: defaultLeaseFor, upInterval: defaultUpInterval,
		downInterval: defaultDownInterval, maxDownInterval: defaultMaxDownInterval,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// RunOnce claims and checks a single batch of due links, returning how
// many were claimed. Call it repeatedly (e.g. from a ticker loop in
// cmd/checker) to keep checking continuously — it does not loop itself.
func (s *Service) RunOnce(ctx context.Context) (int, error) {
	due, err := s.repo.ClaimDue(ctx, s.now(), s.batchSize, s.leaseFor)
	if err != nil {
		return 0, fmt.Errorf("claim due checks: %w", err)
	}
	s.recordBatch(len(due))
	if len(due) == 0 {
		return 0, nil
	}

	sem := make(chan struct{}, s.concurrency)
	var wg sync.WaitGroup
	for _, check := range due {
		wg.Add(1)
		sem <- struct{}{}
		go func(check domain.DueCheck) {
			defer wg.Done()
			defer func() { <-sem }()
			s.checkOne(ctx, check)
		}(check)
	}
	wg.Wait()

	return len(due), nil
}

func (s *Service) checkOne(ctx context.Context, check domain.DueCheck) {
	checkedAt := s.now()
	statusCode, err := s.prober.Probe(ctx, check.OriginalURL)
	up := err == nil
	s.recordCheck(up, s.now().Sub(checkedAt))

	consecutiveFails := 0
	if !up {
		consecutiveFails = check.ConsecutiveFails + 1
	}

	result := domain.HealthResult{
		Code: check.Code, CheckedAt: checkedAt, Up: up,
		StatusCode: statusCode, Err: err, ConsecutiveFails: consecutiveFails,
	}

	if recErr := s.repo.Record(ctx, result, s.nextCheckAt(checkedAt, up, consecutiveFails)); recErr != nil {
		s.logger.Error("record health check result", "code", check.Code, "error", recErr)
	}

	if !up {
		s.logger.Warn("link check failed",
			"code", check.Code, "url", check.OriginalURL,
			"error", err, "consecutive_fails", consecutiveFails)
	}
}

// recordBatch best-effort reports the claimed batch size.
func (s *Service) recordBatch(n int) {
	if s.metrics == nil {
		return
	}
	s.metrics.ObserveBatch(n)
}

// recordCheck best-effort reports a single check's outcome and duration.
func (s *Service) recordCheck(up bool, duration time.Duration) {
	if s.metrics == nil {
		return
	}
	s.metrics.ObserveCheck(up, duration)
}

// nextCheckAt applies a fixed interval for a healthy link and exponential
// backoff (capped at maxDownInterval) for a failing one — a link that's
// been down a while gets rechecked more slowly, instead of hammering a
// dead target at a constant rate.
func (s *Service) nextCheckAt(from time.Time, up bool, consecutiveFails int) time.Time {
	if up {
		return from.Add(s.upInterval)
	}

	shift := consecutiveFails - 1
	if shift > 20 {
		shift = 20 // avoid overflowing time.Duration for a very long-failing link
	}
	backoff := s.downInterval * time.Duration(1<<shift)
	if backoff > s.maxDownInterval || backoff <= 0 {
		backoff = s.maxDownInterval
	}
	return from.Add(backoff)
}
