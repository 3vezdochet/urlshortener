// Package shortener implements the core use case: turning URLs into short
// codes and resolving codes back to URLs.
package shortener

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"urlshortener/internal/domain"
	"urlshortener/pkg/base62"
)

// Clock abstracts time.Now so tests can control the current time.
type Clock func() time.Time

// defaultCacheTTL and defaultNegativeCacheTTL are used when WithCache is
// configured but WithCacheTTL/WithNegativeCacheTTL aren't. The negative
// TTL is intentionally short: it only needs to survive a burst of repeated
// lookups of a nonexistent code, not outlive how long that code might stay
// nonexistent.
const (
	defaultCacheTTL         = time.Hour
	defaultNegativeCacheTTL = time.Minute
)

// HealthScheduler schedules a newly created link for its first
// availability check. A small, consumer-side interface — this package
// doesn't need to know anything else about how checking works, only that
// something can be told "start watching this code". Satisfied structurally
// by anything with this one method, including domain.HealthRepository.
type HealthScheduler interface {
	EnsureScheduled(ctx context.Context, code string, dueAt time.Time) error
}

// Cache lookup outcomes reported to CacheMetricsRecorder.ObserveCacheLookup.
const (
	CacheOutcomeHit         = "hit"          // found in cache, served without touching the repository
	CacheOutcomeNegativeHit = "negative_hit" // cache confirmed the code doesn't exist (SetMissing)
	CacheOutcomeMiss        = "miss"         // not in cache either way; fell through to the repository
	CacheOutcomeError       = "error"        // cache itself failed; treated as a miss, logged separately
)

// CacheMetricsRecorder receives one observation per Resolve cache lookup,
// classified by outcome (see the CacheOutcome* constants). The production
// implementation (see the sibling shortenermetrics package) records to
// Prometheus; this package itself has no Prometheus dependency.
type CacheMetricsRecorder interface {
	ObserveCacheLookup(outcome string)
}

// Service implements link shortening and resolution.
type Service struct {
	repo            domain.LinkRepository
	codeGen         domain.CodeGenerator
	cache           domain.LinkCache     // optional; nil disables caching entirely
	cacheMetrics    CacheMetricsRecorder // optional; nil records nothing
	healthScheduler HealthScheduler      // optional; nil disables availability checking entirely
	now             Clock
	logger          *slog.Logger

	ttl              time.Duration
	cacheTTL         time.Duration
	negativeCacheTTL time.Duration
}

// Option configures a Service.
type Option func(*Service)

// WithClock overrides the default time source (time.Now). Useful in tests.
func WithClock(c Clock) Option {
	return func(s *Service) { s.now = c }
}

// WithDefaultTTL sets the TTL applied to links created without an explicit
// expiration. The zero value disables the default (links never expire
// unless a TTL is requested explicitly).
func WithDefaultTTL(ttl time.Duration) Option {
	return func(s *Service) { s.ttl = ttl }
}

// WithCache enables cache-aside reads through cache for Resolve. Without
// this option, Resolve and Get always go straight to the repository.
func WithCache(cache domain.LinkCache) Option {
	return func(s *Service) { s.cache = cache }
}

// WithCacheTTL overrides how long a resolved link stays cached (default
// defaultCacheTTL). Has no effect unless WithCache is also set.
func WithCacheTTL(ttl time.Duration) Option {
	return func(s *Service) { s.cacheTTL = ttl }
}

// WithNegativeCacheTTL overrides how long a "code does not exist" result
// stays cached (default defaultNegativeCacheTTL). Has no effect unless
// WithCache is also set.
func WithNegativeCacheTTL(ttl time.Duration) Option {
	return func(s *Service) { s.negativeCacheTTL = ttl }
}

// WithCacheMetrics enables recording each Resolve cache lookup's outcome
// to rec (see the shortenermetrics package for the Prometheus
// implementation). Has no effect unless WithCache is also set.
func WithCacheMetrics(rec CacheMetricsRecorder) Option {
	return func(s *Service) { s.cacheMetrics = rec }
}

// WithHealthScheduler enables scheduling a first availability check for
// every link created. Without this option, Create doesn't touch
// availability checking at all — same "off unless configured" pattern as
// WithCache.
func WithHealthScheduler(hs HealthScheduler) Option {
	return func(s *Service) { s.healthScheduler = hs }
}

// WithLogger overrides the logger used to report cache errors (default
// slog.Default()). Cache failures never fail a request — they're logged
// and treated as a miss — so they need somewhere to go.
func WithLogger(logger *slog.Logger) Option {
	return func(s *Service) { s.logger = logger }
}

// New creates a Service backed by repo, using codeGen to derive short codes.
func New(repo domain.LinkRepository, codeGen domain.CodeGenerator, opts ...Option) *Service {
	s := &Service{
		repo:             repo,
		codeGen:          codeGen,
		now:              time.Now,
		logger:           slog.Default(),
		cacheTTL:         defaultCacheTTL,
		negativeCacheTTL: defaultNegativeCacheTTL,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// CreateRequest describes a request to shorten a URL.
type CreateRequest struct {
	OriginalURL string
	OwnerID     string
	CustomAlias string        // optional; if empty, a code is generated
	TTL         time.Duration // optional; overrides the service default
}

// Create validates req and stores a new short link, returning it.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*domain.Link, error) {
	if err := validateURL(req.OriginalURL); err != nil {
		return nil, err
	}

	code := req.CustomAlias
	if code == "" {
		id, err := s.codeGen.Next(ctx)
		if err != nil {
			return nil, fmt.Errorf("generate code: %w", err)
		}
		code = base62.Encode(id)
	}

	link := &domain.Link{
		Code:        code,
		OriginalURL: req.OriginalURL,
		OwnerID:     req.OwnerID,
		CreatedAt:   s.now(),
		Active:      true,
	}

	if ttl := effectiveTTL(req.TTL, s.ttl); ttl > 0 {
		expiresAt := link.CreatedAt.Add(ttl)
		link.ExpiresAt = &expiresAt
	}

	if err := s.repo.Create(ctx, link); err != nil {
		return nil, fmt.Errorf("store link: %w", err)
	}

	// A custom alias might have been negatively cached by an earlier
	// lookup of a code that didn't exist yet — clear that immediately
	// rather than waiting out the negative TTL.
	s.invalidateCache(ctx, code)
	s.scheduleHealthCheck(ctx, code)

	return link, nil
}

// Resolve returns the active, non-expired link for code. When caching is
// configured (WithCache), it's consulted first — a definitive cache
// answer (found, or confirmed missing via SetMissing) skips the
// repository entirely. Any cache error is logged and treated as a miss,
// falling through to the repository: the cache is a performance
// optimization, never a point of failure for the read path.
func (s *Service) Resolve(ctx context.Context, code string) (*domain.Link, error) {
	if s.cache != nil {
		link, hit, err := s.cache.Get(ctx, code)
		switch {
		case err != nil:
			s.logger.Warn("cache get failed", "code", code, "error", err)
			s.recordCacheOutcome(CacheOutcomeError)
		case hit:
			s.recordCacheOutcome(CacheOutcomeHit)
			return checkAvailable(link, s.now())
		default:
			missing, err := s.cache.IsMissing(ctx, code)
			switch {
			case err != nil:
				s.logger.Warn("cache is-missing check failed", "code", code, "error", err)
				s.recordCacheOutcome(CacheOutcomeError)
			case missing:
				s.recordCacheOutcome(CacheOutcomeNegativeHit)
				return nil, domain.ErrLinkNotFound
			default:
				s.recordCacheOutcome(CacheOutcomeMiss)
			}
		}
	}

	link, err := s.repo.GetByCode(ctx, code)
	if err != nil {
		if errors.Is(err, domain.ErrLinkNotFound) {
			s.cacheSetMissing(ctx, code)
		}
		return nil, fmt.Errorf("lookup link: %w", err)
	}

	s.cacheSet(ctx, link)

	return checkAvailable(link, s.now())
}

// Get returns the link for code as stored, without the active/expiry
// filtering Resolve applies, and without consulting the cache — this path
// is for owners inspecting their own links, not the hot redirect path, so
// it always reads the source of truth.
func (s *Service) Get(ctx context.Context, code string) (*domain.Link, error) {
	link, err := s.repo.GetByCode(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("lookup link: %w", err)
	}
	return link, nil
}

// Deactivate marks the link identified by code as inactive.
func (s *Service) Deactivate(ctx context.Context, code string) error {
	if err := s.repo.Deactivate(ctx, code); err != nil {
		return fmt.Errorf("deactivate link: %w", err)
	}

	s.invalidateCache(ctx, code)

	return nil
}

// checkAvailable applies Resolve's visibility rules to a link already
// fetched from cache or the repository: it must be active and not expired
// as of now.
func checkAvailable(link *domain.Link, now time.Time) (*domain.Link, error) {
	if !link.Active {
		return nil, domain.ErrLinkNotFound
	}
	if link.IsExpired(now) {
		return nil, domain.ErrLinkExpired
	}
	return link, nil
}

// cacheSet best-effort caches link. A failure is logged, never returned —
// the write to the repository already succeeded, so the request itself
// must not fail just because the cache is unavailable.
func (s *Service) cacheSet(ctx context.Context, link *domain.Link) {
	if s.cache == nil {
		return
	}
	if err := s.cache.Set(ctx, link, s.effectiveCacheTTL(link)); err != nil {
		s.logger.Warn("cache set failed", "code", link.Code, "error", err)
	}
}

// cacheSetMissing best-effort records code as confirmed absent.
func (s *Service) cacheSetMissing(ctx context.Context, code string) {
	if s.cache == nil {
		return
	}
	if err := s.cache.SetMissing(ctx, code, s.negativeCacheTTL); err != nil {
		s.logger.Warn("cache set-missing failed", "code", code, "error", err)
	}
}

// invalidateCache best-effort clears any cached entry for code.
func (s *Service) invalidateCache(ctx context.Context, code string) {
	if s.cache == nil {
		return
	}
	if err := s.cache.Invalidate(ctx, code); err != nil {
		s.logger.Warn("cache invalidate failed", "code", code, "error", err)
	}
}

// recordCacheOutcome best-effort reports a single Resolve cache lookup's
// outcome. A no-op unless WithCacheMetrics was configured.
func (s *Service) recordCacheOutcome(outcome string) {
	if s.cacheMetrics == nil {
		return
	}
	s.cacheMetrics.ObserveCacheLookup(outcome)
}

// scheduleHealthCheck best-effort schedules code's first availability
// check. A failure here is logged, never returned — the link itself was
// already created successfully, and checking is a secondary feature.
func (s *Service) scheduleHealthCheck(ctx context.Context, code string) {
	if s.healthScheduler == nil {
		return
	}
	if err := s.healthScheduler.EnsureScheduled(ctx, code, s.now()); err != nil {
		s.logger.Warn("schedule health check failed", "code", code, "error", err)
	}
}

// effectiveCacheTTL bounds a cache entry's lifetime to the link's own
// expiration, so an expiring link is never served from cache past the
// moment Resolve should start returning ErrLinkExpired. Computed against
// s.now(), not the real wall clock, so it stays correct under an injected
// Clock (tests) or real clock skew alike.
func (s *Service) effectiveCacheTTL(link *domain.Link) time.Duration {
	if link.ExpiresAt == nil {
		return s.cacheTTL
	}
	if until := link.ExpiresAt.Sub(s.now()); until < s.cacheTTL {
		if until < 0 {
			return 0
		}
		return until
	}
	return s.cacheTTL
}

func effectiveTTL(requested, fallback time.Duration) time.Duration {
	if requested > 0 {
		return requested
	}
	return fallback
}

func validateURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("%w: empty url", domain.ErrInvalidURL)
	}

	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrInvalidURL, err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: unsupported scheme %q", domain.ErrInvalidURL, u.Scheme)
	}

	if u.Host == "" {
		return fmt.Errorf("%w: missing host", domain.ErrInvalidURL)
	}

	return nil
}
