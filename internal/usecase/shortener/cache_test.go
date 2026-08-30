package shortener_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"urlshortener/internal/domain"
	"urlshortener/internal/repository/memory"
	"urlshortener/internal/usecase/shortener"
)

// fakeCache is an in-memory domain.LinkCache double with call counters and
// an injectable error, so tests can assert both behavior (does Resolve
// return the right thing) and interaction (did it actually skip the
// repository, did it record a miss).
type fakeCache struct {
	mu       sync.Mutex
	positive map[string]cachedLink
	negative map[string]bool
	getErr   error

	getCalls        int
	setCalls        int
	setMissingCalls int
	invalidateCalls int
}

type cachedLink struct {
	link *domain.Link
	ttl  time.Duration
}

func newFakeCache() *fakeCache {
	return &fakeCache{
		positive: make(map[string]cachedLink),
		negative: make(map[string]bool),
	}
}

func (c *fakeCache) Get(_ context.Context, code string) (*domain.Link, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.getCalls++

	if c.getErr != nil {
		return nil, false, c.getErr
	}
	entry, ok := c.positive[code]
	if !ok {
		return nil, false, nil
	}
	linkCopy := *entry.link
	return &linkCopy, true, nil
}

func (c *fakeCache) Set(_ context.Context, link *domain.Link, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setCalls++

	linkCopy := *link
	c.positive[link.Code] = cachedLink{link: &linkCopy, ttl: ttl}
	return nil
}

func (c *fakeCache) SetMissing(_ context.Context, code string, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setMissingCalls++
	c.negative[code] = true
	return nil
}

func (c *fakeCache) IsMissing(_ context.Context, code string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.negative[code], nil
}

func (c *fakeCache) Invalidate(_ context.Context, code string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.invalidateCalls++
	delete(c.positive, code)
	delete(c.negative, code)
	return nil
}

func (c *fakeCache) ttlFor(code string) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.positive[code].ttl
}

// countingRepo decorates a domain.LinkRepository, counting GetByCode calls
// so tests can assert the cache actually short-circuits repository access
// rather than merely not breaking anything.
type countingRepo struct {
	domain.LinkRepository
	getByCodeCalls int32
}

func (r *countingRepo) GetByCode(ctx context.Context, code string) (*domain.Link, error) {
	atomic.AddInt32(&r.getByCodeCalls, 1)
	return r.LinkRepository.GetByCode(ctx, code)
}

func (r *countingRepo) calls() int {
	return int(atomic.LoadInt32(&r.getByCodeCalls))
}

func TestService_Resolve_CachesOnMissThenSkipsRepo(t *testing.T) {
	repo := &countingRepo{LinkRepository: memory.NewLinkRepo()}
	cache := newFakeCache()
	svc := shortener.New(repo, memory.NewCodeGen(0), shortener.WithCache(cache))
	ctx := context.Background()

	created, err := svc.Create(ctx, shortener.CreateRequest{OriginalURL: "https://example.com", CustomAlias: "promo"})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	// Create doesn't touch the cache's Get path, so the repo call count
	// below starts fresh from Resolve alone.

	if _, err := svc.Resolve(ctx, created.Code); err != nil {
		t.Fatalf("first Resolve() unexpected error: %v", err)
	}
	if got := repo.calls(); got != 1 {
		t.Fatalf("repo.GetByCode calls after first Resolve = %d, want 1", got)
	}
	if cache.setCalls != 1 {
		t.Errorf("cache.Set calls = %d, want 1", cache.setCalls)
	}

	if _, err := svc.Resolve(ctx, created.Code); err != nil {
		t.Fatalf("second Resolve() unexpected error: %v", err)
	}
	if got := repo.calls(); got != 1 {
		t.Errorf("repo.GetByCode calls after second Resolve = %d, want still 1 (should be served from cache)", got)
	}
}

func TestService_Resolve_NegativeCacheSkipsRepo(t *testing.T) {
	repo := &countingRepo{LinkRepository: memory.NewLinkRepo()}
	cache := newFakeCache()
	svc := shortener.New(repo, memory.NewCodeGen(0), shortener.WithCache(cache))
	ctx := context.Background()

	_, err := svc.Resolve(ctx, "missing")
	if !errors.Is(err, domain.ErrLinkNotFound) {
		t.Fatalf("first Resolve() error = %v, want %v", err, domain.ErrLinkNotFound)
	}
	if got := repo.calls(); got != 1 {
		t.Fatalf("repo.GetByCode calls after first Resolve = %d, want 1", got)
	}
	if cache.setMissingCalls != 1 {
		t.Errorf("cache.SetMissing calls = %d, want 1", cache.setMissingCalls)
	}

	_, err = svc.Resolve(ctx, "missing")
	if !errors.Is(err, domain.ErrLinkNotFound) {
		t.Fatalf("second Resolve() error = %v, want %v", err, domain.ErrLinkNotFound)
	}
	if got := repo.calls(); got != 1 {
		t.Errorf("repo.GetByCode calls after second Resolve = %d, want still 1 (negative cache should short-circuit)", got)
	}
}

func TestService_Resolve_CacheErrorFallsThroughToRepo(t *testing.T) {
	repo := &countingRepo{LinkRepository: memory.NewLinkRepo()}
	cache := newFakeCache()
	cache.getErr = errors.New("redis: connection refused")
	svc := shortener.New(repo, memory.NewCodeGen(0), shortener.WithCache(cache))
	ctx := context.Background()

	created, err := svc.Create(ctx, shortener.CreateRequest{OriginalURL: "https://example.com", CustomAlias: "promo"})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	got, err := svc.Resolve(ctx, created.Code)
	if err != nil {
		t.Fatalf("Resolve() unexpected error despite cache failure: %v", err)
	}
	if got.OriginalURL != created.OriginalURL {
		t.Errorf("OriginalURL = %q, want %q", got.OriginalURL, created.OriginalURL)
	}
	if repo.calls() != 1 {
		t.Errorf("repo.GetByCode calls = %d, want 1 (should have fallen through)", repo.calls())
	}
}

func TestService_Resolve_CachedLinkStillRespectsAvailability(t *testing.T) {
	repo := &countingRepo{LinkRepository: memory.NewLinkRepo()}
	cache := newFakeCache()
	svc := shortener.New(repo, memory.NewCodeGen(0), shortener.WithCache(cache))
	ctx := context.Background()

	// Seed the cache directly with an inactive link, bypassing Resolve's
	// normal write path, to prove checkAvailable is applied to cache hits
	// too — not just to links freshly loaded from the repository.
	if err := cache.Set(ctx, &domain.Link{Code: "promo", OriginalURL: "https://example.com", Active: false}, time.Hour); err != nil {
		t.Fatalf("cache.Set() unexpected error: %v", err)
	}

	_, err := svc.Resolve(ctx, "promo")
	if !errors.Is(err, domain.ErrLinkNotFound) {
		t.Fatalf("Resolve() error = %v, want %v", err, domain.ErrLinkNotFound)
	}
	if repo.calls() != 0 {
		t.Errorf("repo.GetByCode calls = %d, want 0 (should have been served from cache)", repo.calls())
	}
}

func TestService_Create_InvalidatesStaleNegativeCache(t *testing.T) {
	repo := &countingRepo{LinkRepository: memory.NewLinkRepo()}
	cache := newFakeCache()
	svc := shortener.New(repo, memory.NewCodeGen(0), shortener.WithCache(cache))
	ctx := context.Background()

	// Simulate an earlier lookup of "promo" before it existed.
	if err := cache.SetMissing(ctx, "promo", time.Minute); err != nil {
		t.Fatalf("cache.SetMissing() unexpected error: %v", err)
	}

	if _, err := svc.Create(ctx, shortener.CreateRequest{OriginalURL: "https://example.com", CustomAlias: "promo"}); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	if missing, _ := cache.IsMissing(ctx, "promo"); missing {
		t.Error("IsMissing(\"promo\") = true after Create, want false (Create should invalidate)")
	}

	// And the read path should now go through to the repo and succeed,
	// rather than trusting the stale negative entry.
	if _, err := svc.Resolve(ctx, "promo"); err != nil {
		t.Fatalf("Resolve() after Create unexpected error: %v", err)
	}
}

func TestService_Deactivate_InvalidatesCache(t *testing.T) {
	repo := &countingRepo{LinkRepository: memory.NewLinkRepo()}
	cache := newFakeCache()
	svc := shortener.New(repo, memory.NewCodeGen(0), shortener.WithCache(cache))
	ctx := context.Background()

	created, err := svc.Create(ctx, shortener.CreateRequest{OriginalURL: "https://example.com", CustomAlias: "promo"})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if _, err := svc.Resolve(ctx, created.Code); err != nil {
		t.Fatalf("Resolve() unexpected error: %v", err)
	}
	if cache.setCalls != 1 {
		t.Fatalf("cache.Set calls before Deactivate = %d, want 1", cache.setCalls)
	}

	if err := svc.Deactivate(ctx, created.Code); err != nil {
		t.Fatalf("Deactivate() unexpected error: %v", err)
	}
	// Create already invalidates once (to clear any stale negative entry
	// for the code it just claimed), so assert the delta from Deactivate,
	// not an absolute count.
	if cache.invalidateCalls != 2 {
		t.Errorf("cache.Invalidate calls = %d, want 2 (1 from Create, 1 from Deactivate)", cache.invalidateCalls)
	}

	// The stale positive entry must be gone — Resolve has to consult the
	// repo again and see the link is now inactive, not serve the cached
	// (still-active) copy from before Deactivate.
	_, err = svc.Resolve(ctx, created.Code)
	if !errors.Is(err, domain.ErrLinkNotFound) {
		t.Fatalf("Resolve() after Deactivate error = %v, want %v", err, domain.ErrLinkNotFound)
	}
}

func TestService_Resolve_CacheTTLBoundedByExpiry(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	repo := &countingRepo{LinkRepository: memory.NewLinkRepo()}
	cache := newFakeCache()
	svc := shortener.New(repo, memory.NewCodeGen(0),
		shortener.WithCache(cache),
		shortener.WithClock(clock),
		shortener.WithCacheTTL(time.Hour), // default cache TTL, much longer than the link's own TTL below
	)
	ctx := context.Background()

	created, err := svc.Create(ctx, shortener.CreateRequest{
		OriginalURL: "https://example.com",
		CustomAlias: "promo",
		TTL:         30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	if _, err := svc.Resolve(ctx, created.Code); err != nil {
		t.Fatalf("Resolve() unexpected error: %v", err)
	}

	got := cache.ttlFor(created.Code)
	if got <= 0 || got > 30*time.Second {
		t.Errorf("cached TTL = %v, want (0, 30s] (bounded by the link's own expiry, not the 1h default)", got)
	}
}

func TestService_Resolve_NoCacheConfigured(t *testing.T) {
	// WithCache not set: Resolve must work exactly as before caching
	// existed, straight against the repository every time.
	svc := shortener.New(memory.NewLinkRepo(), memory.NewCodeGen(0))
	ctx := context.Background()

	created, err := svc.Create(ctx, shortener.CreateRequest{OriginalURL: "https://example.com"})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	got, err := svc.Resolve(ctx, created.Code)
	if err != nil {
		t.Fatalf("Resolve() unexpected error: %v", err)
	}
	if got.OriginalURL != created.OriginalURL {
		t.Errorf("OriginalURL = %q, want %q", got.OriginalURL, created.OriginalURL)
	}
}

// --- cache metrics ----------------------------------------------------------

// fakeCacheMetricsRecorder is a shortener.CacheMetricsRecorder double —
// verifies Resolve classifies each cache lookup correctly without needing
// client_golang.
type fakeCacheMetricsRecorder struct {
	mu       sync.Mutex
	outcomes []string
}

func (f *fakeCacheMetricsRecorder) ObserveCacheLookup(outcome string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outcomes = append(f.outcomes, outcome)
}

func (f *fakeCacheMetricsRecorder) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.outcomes) == 0 {
		return ""
	}
	return f.outcomes[len(f.outcomes)-1]
}

func TestService_Resolve_RecordsCacheHit(t *testing.T) {
	repo := memory.NewLinkRepo()
	cache := newFakeCache()
	metrics := &fakeCacheMetricsRecorder{}
	svc := shortener.New(repo, memory.NewCodeGen(0), shortener.WithCache(cache), shortener.WithCacheMetrics(metrics))
	ctx := context.Background()

	created, err := svc.Create(ctx, shortener.CreateRequest{OriginalURL: "https://example.com", CustomAlias: "promo"})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	// First Resolve populates the cache (a miss); second is the hit under test.
	if _, err := svc.Resolve(ctx, created.Code); err != nil {
		t.Fatalf("first Resolve() unexpected error: %v", err)
	}

	if _, err := svc.Resolve(ctx, created.Code); err != nil {
		t.Fatalf("second Resolve() unexpected error: %v", err)
	}

	if got := metrics.last(); got != shortener.CacheOutcomeHit {
		t.Errorf("last recorded outcome = %q, want %q", got, shortener.CacheOutcomeHit)
	}
}

func TestService_Resolve_RecordsCacheMiss(t *testing.T) {
	repo := memory.NewLinkRepo()
	cache := newFakeCache()
	metrics := &fakeCacheMetricsRecorder{}
	svc := shortener.New(repo, memory.NewCodeGen(0), shortener.WithCache(cache), shortener.WithCacheMetrics(metrics))
	ctx := context.Background()

	created, err := svc.Create(ctx, shortener.CreateRequest{OriginalURL: "https://example.com", CustomAlias: "promo"})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	if _, err := svc.Resolve(ctx, created.Code); err != nil {
		t.Fatalf("Resolve() unexpected error: %v", err)
	}

	if got := metrics.last(); got != shortener.CacheOutcomeMiss {
		t.Errorf("last recorded outcome = %q, want %q", got, shortener.CacheOutcomeMiss)
	}
}

func TestService_Resolve_RecordsCacheNegativeHit(t *testing.T) {
	repo := memory.NewLinkRepo()
	cache := newFakeCache()
	metrics := &fakeCacheMetricsRecorder{}
	svc := shortener.New(repo, memory.NewCodeGen(0), shortener.WithCache(cache), shortener.WithCacheMetrics(metrics))
	ctx := context.Background()

	// First Resolve of an unknown code negatively caches it; second is
	// the negative hit under test.
	if _, err := svc.Resolve(ctx, "missing"); !errors.Is(err, domain.ErrLinkNotFound) {
		t.Fatalf("first Resolve() error = %v, want %v", err, domain.ErrLinkNotFound)
	}

	if _, err := svc.Resolve(ctx, "missing"); !errors.Is(err, domain.ErrLinkNotFound) {
		t.Fatalf("second Resolve() error = %v, want %v", err, domain.ErrLinkNotFound)
	}

	if got := metrics.last(); got != shortener.CacheOutcomeNegativeHit {
		t.Errorf("last recorded outcome = %q, want %q", got, shortener.CacheOutcomeNegativeHit)
	}
}

func TestService_Resolve_RecordsCacheError(t *testing.T) {
	repo := memory.NewLinkRepo()
	cache := newFakeCache()
	cache.getErr = errors.New("redis: connection refused")
	metrics := &fakeCacheMetricsRecorder{}
	svc := shortener.New(repo, memory.NewCodeGen(0), shortener.WithCache(cache), shortener.WithCacheMetrics(metrics))
	ctx := context.Background()

	created, err := svc.Create(ctx, shortener.CreateRequest{OriginalURL: "https://example.com", CustomAlias: "promo"})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	if _, err := svc.Resolve(ctx, created.Code); err != nil {
		t.Fatalf("Resolve() unexpected error despite cache failure: %v", err)
	}

	if got := metrics.last(); got != shortener.CacheOutcomeError {
		t.Errorf("last recorded outcome = %q, want %q", got, shortener.CacheOutcomeError)
	}
}

func TestService_Resolve_NilCacheMetricsRecorderIsSafe(t *testing.T) {
	repo := memory.NewLinkRepo()
	cache := newFakeCache()
	// WithCache but no WithCacheMetrics — must not panic.
	svc := shortener.New(repo, memory.NewCodeGen(0), shortener.WithCache(cache))
	ctx := context.Background()

	created, err := svc.Create(ctx, shortener.CreateRequest{OriginalURL: "https://example.com"})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if _, err := svc.Resolve(ctx, created.Code); err != nil {
		t.Fatalf("Resolve() unexpected error: %v", err)
	}
}
