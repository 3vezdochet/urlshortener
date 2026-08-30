package checker_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"urlshortener/internal/domain"
	"urlshortener/internal/usecase/checker"
)

// --- fakeHealthRepo -----------------------------------------------------

type healthRow struct {
	code             string
	originalURL      string
	status           domain.HealthStatus
	lastCheckedAt    *time.Time
	nextCheckAt      time.Time
	consecutiveFails int
	lastStatusCode   int
	lastError        string
}

type fakeHealthRepo struct {
	mu            sync.Mutex
	rows          map[string]*healthRow
	claimDueCalls int
	recordCalls   []domain.HealthResult
	claimErr      error
}

func newFakeHealthRepo() *fakeHealthRepo {
	return &fakeHealthRepo{rows: make(map[string]*healthRow)}
}

func (r *fakeHealthRepo) addLink(code, url string, dueAt time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows[code] = &healthRow{code: code, originalURL: url, status: domain.HealthStatusUnknown, nextCheckAt: dueAt}
}

func (r *fakeHealthRepo) addLinkWithFails(code, url string, dueAt time.Time, consecutiveFails int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows[code] = &healthRow{
		code: code, originalURL: url, status: domain.HealthStatusDown,
		nextCheckAt: dueAt, consecutiveFails: consecutiveFails,
	}
}

func (r *fakeHealthRepo) EnsureScheduled(_ context.Context, code string, dueAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.rows[code]; exists {
		return nil
	}
	r.rows[code] = &healthRow{code: code, nextCheckAt: dueAt}
	return nil
}

func (r *fakeHealthRepo) ClaimDue(_ context.Context, now time.Time, limit int, leaseFor time.Duration) ([]domain.DueCheck, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.claimDueCalls++
	if r.claimErr != nil {
		return nil, r.claimErr
	}

	var due []domain.DueCheck
	for _, row := range r.rows {
		if len(due) >= limit {
			break
		}
		if row.nextCheckAt.After(now) {
			continue
		}
		due = append(due, domain.DueCheck{
			Code: row.code, OriginalURL: row.originalURL, ConsecutiveFails: row.consecutiveFails,
		})
		row.nextCheckAt = now.Add(leaseFor)
	}

	sort.Slice(due, func(i, j int) bool { return due[i].Code < due[j].Code })
	return due, nil
}

func (r *fakeHealthRepo) Record(_ context.Context, result domain.HealthResult, nextCheckAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.recordCalls = append(r.recordCalls, result)

	row, ok := r.rows[result.Code]
	if !ok {
		return domain.ErrLinkNotFound
	}

	status := domain.HealthStatusUp
	if !result.Up {
		status = domain.HealthStatusDown
	}
	checkedAt := result.CheckedAt

	row.status = status
	row.lastCheckedAt = &checkedAt
	row.nextCheckAt = nextCheckAt
	row.consecutiveFails = result.ConsecutiveFails
	row.lastStatusCode = result.StatusCode
	if result.Err != nil {
		row.lastError = result.Err.Error()
	} else {
		row.lastError = ""
	}
	return nil
}

func (r *fakeHealthRepo) GetByCode(_ context.Context, code string) (*domain.LinkHealth, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	row, ok := r.rows[code]
	if !ok {
		return nil, domain.ErrLinkNotFound
	}
	return &domain.LinkHealth{
		Code: row.code, Status: row.status, LastCheckedAt: row.lastCheckedAt,
		NextCheckAt: row.nextCheckAt, ConsecutiveFails: row.consecutiveFails,
		LastStatusCode: row.lastStatusCode, LastError: row.lastError,
	}, nil
}

// --- fakeProber -----------------------------------------------------------

type proberResponse struct {
	statusCode int
	err        error
}

type fakeProber struct {
	mu        sync.Mutex
	responses map[string]proberResponse
	calls     []string
}

func newFakeProber() *fakeProber {
	return &fakeProber{responses: make(map[string]proberResponse)}
}

func (p *fakeProber) fail(url string, statusCode int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.responses[url] = proberResponse{statusCode: statusCode, err: err}
}

func (p *fakeProber) Probe(_ context.Context, url string) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, url)
	if resp, ok := p.responses[url]; ok {
		return resp.statusCode, resp.err
	}
	return 200, nil
}

// --- tests -----------------------------------------------------------------

func TestService_RunOnce_NoDueChecks(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	repo := newFakeHealthRepo()
	repo.addLink("abc", "https://example.com", now.Add(time.Hour)) // not due yet
	prober := newFakeProber()

	svc := checker.New(repo, prober, checker.WithClock(func() time.Time { return now }))

	n, err := svc.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("RunOnce() = %d, want 0", n)
	}
	if len(prober.calls) != 0 {
		t.Errorf("prober called %d times, want 0", len(prober.calls))
	}
}

func TestService_RunOnce_MarksUp(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	repo := newFakeHealthRepo()
	repo.addLink("abc", "https://example.com", now)
	prober := newFakeProber() // defaults to 200, nil (up)

	svc := checker.New(repo, prober,
		checker.WithClock(func() time.Time { return now }),
		checker.WithUpInterval(10*time.Minute),
	)

	n, err := svc.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() unexpected error: %v", err)
	}
	if n != 1 {
		t.Fatalf("RunOnce() = %d, want 1", n)
	}

	health, err := repo.GetByCode(context.Background(), "abc")
	if err != nil {
		t.Fatalf("GetByCode() unexpected error: %v", err)
	}
	if health.Status != domain.HealthStatusUp {
		t.Errorf("Status = %q, want %q", health.Status, domain.HealthStatusUp)
	}
	if health.ConsecutiveFails != 0 {
		t.Errorf("ConsecutiveFails = %d, want 0", health.ConsecutiveFails)
	}
	if health.LastCheckedAt == nil || !health.LastCheckedAt.Equal(now) {
		t.Errorf("LastCheckedAt = %v, want %v", health.LastCheckedAt, now)
	}
	wantNext := now.Add(10 * time.Minute)
	if !health.NextCheckAt.Equal(wantNext) {
		t.Errorf("NextCheckAt = %v, want %v", health.NextCheckAt, wantNext)
	}
}

func TestService_RunOnce_MarksDown(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	repo := newFakeHealthRepo()
	repo.addLink("abc", "https://dead.example.com", now)
	prober := newFakeProber()
	prober.fail("https://dead.example.com", 0, errors.New("connection refused"))

	svc := checker.New(repo, prober,
		checker.WithClock(func() time.Time { return now }),
		checker.WithDownInterval(time.Minute),
	)

	if _, err := svc.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() unexpected error: %v", err)
	}

	health, err := repo.GetByCode(context.Background(), "abc")
	if err != nil {
		t.Fatalf("GetByCode() unexpected error: %v", err)
	}
	if health.Status != domain.HealthStatusDown {
		t.Errorf("Status = %q, want %q", health.Status, domain.HealthStatusDown)
	}
	if health.ConsecutiveFails != 1 {
		t.Errorf("ConsecutiveFails = %d, want 1", health.ConsecutiveFails)
	}
	if health.LastError == "" {
		t.Error("LastError is empty, want the probe error message")
	}
	wantNext := now.Add(time.Minute) // base backoff, first failure
	if !health.NextCheckAt.Equal(wantNext) {
		t.Errorf("NextCheckAt = %v, want %v", health.NextCheckAt, wantNext)
	}
}

func TestService_RunOnce_ExponentialBackoffCappedAtMax(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name             string
		priorFails       int // ConsecutiveFails going into this check
		downInterval     time.Duration
		maxDownInterval  time.Duration
		wantBackoffAtCap bool
	}{
		{name: "first failure: base interval", priorFails: 0, downInterval: time.Minute, maxDownInterval: time.Hour},
		{name: "third failure: base * 4", priorFails: 2, downInterval: time.Minute, maxDownInterval: time.Hour},
		{name: "many failures: capped", priorFails: 30, downInterval: time.Minute, maxDownInterval: time.Hour, wantBackoffAtCap: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFakeHealthRepo()
			repo.addLinkWithFails("abc", "https://dead.example.com", now, tt.priorFails)
			prober := newFakeProber()
			prober.fail("https://dead.example.com", 0, errors.New("timeout"))

			svc := checker.New(repo, prober,
				checker.WithClock(func() time.Time { return now }),
				checker.WithDownInterval(tt.downInterval),
				checker.WithMaxDownInterval(tt.maxDownInterval),
			)

			if _, err := svc.RunOnce(context.Background()); err != nil {
				t.Fatalf("RunOnce() unexpected error: %v", err)
			}

			health, err := repo.GetByCode(context.Background(), "abc")
			if err != nil {
				t.Fatalf("GetByCode() unexpected error: %v", err)
			}

			gotBackoff := health.NextCheckAt.Sub(now)
			if tt.wantBackoffAtCap {
				if gotBackoff != tt.maxDownInterval {
					t.Errorf("backoff = %v, want capped at %v", gotBackoff, tt.maxDownInterval)
				}
				return
			}

			wantBackoff := tt.downInterval * time.Duration(1<<tt.priorFails)
			if gotBackoff != wantBackoff {
				t.Errorf("backoff = %v, want %v", gotBackoff, wantBackoff)
			}
		})
	}
}

func TestService_RunOnce_RespectsBatchSize(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	repo := newFakeHealthRepo()
	for i := 0; i < 10; i++ {
		code := fmt.Sprintf("code-%02d", i)
		repo.addLink(code, "https://example.com/"+code, now)
	}
	prober := newFakeProber()

	svc := checker.New(repo, prober,
		checker.WithClock(func() time.Time { return now }),
		checker.WithBatchSize(3),
	)

	n, err := svc.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() unexpected error: %v", err)
	}
	if n != 3 {
		t.Errorf("RunOnce() = %d, want 3 (batch size)", n)
	}
}

func TestService_RunOnce_ClaimError(t *testing.T) {
	repo := newFakeHealthRepo()
	repo.claimErr = errors.New("db unavailable")
	prober := newFakeProber()

	svc := checker.New(repo, prober)

	_, err := svc.RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce() error = nil, want non-nil")
	}
}

func TestService_RunOnce_ChecksEveryLinkInABatch(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	repo := newFakeHealthRepo()
	repo.addLink("abc", "https://example.com/abc", now)
	repo.addLink("def", "https://example.com/def", now)
	prober := newFakeProber()

	svc := checker.New(repo, prober, checker.WithClock(func() time.Time { return now }))

	n, err := svc.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() unexpected error: %v", err)
	}
	if n != 2 {
		t.Fatalf("RunOnce() = %d, want 2", n)
	}
	if len(prober.calls) != 2 {
		t.Errorf("prober called %d times, want 2", len(prober.calls))
	}
}

func TestService_RunOnce_ConcurrencyLimit(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	repo := newFakeHealthRepo()
	for i := 0; i < 9; i++ {
		code := fmt.Sprintf("code-%02d", i)
		repo.addLink(code, "https://example.com/"+code, now)
	}

	tp := &trackingProber{delay: 20 * time.Millisecond}
	svc := checker.New(repo, tp,
		checker.WithClock(func() time.Time { return now }),
		checker.WithConcurrency(3),
	)

	if _, err := svc.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() unexpected error: %v", err)
	}

	tp.mu.Lock()
	defer tp.mu.Unlock()
	if tp.maxInFlight > 3 {
		t.Errorf("max concurrent probes = %d, want <= 3", tp.maxInFlight)
	}
	if tp.maxInFlight < 1 {
		t.Error("max concurrent probes = 0, probes never ran")
	}
}

type trackingProber struct {
	mu          sync.Mutex
	inFlight    int
	maxInFlight int
	delay       time.Duration
}

func (p *trackingProber) Probe(_ context.Context, _ string) (int, error) {
	p.mu.Lock()
	p.inFlight++
	if p.inFlight > p.maxInFlight {
		p.maxInFlight = p.inFlight
	}
	p.mu.Unlock()

	time.Sleep(p.delay)

	p.mu.Lock()
	p.inFlight--
	p.mu.Unlock()

	return 200, nil
}

// --- fakeMetricsRecorder ----------------------------------------------------

// fakeMetricsRecorder is a checker.MetricsRecorder double. This is the
// payoff of splitting the Prometheus adapter into checkermetrics: Service's
// interaction with metrics is verifiable here without client_golang.
type fakeMetricsRecorder struct {
	mu           sync.Mutex
	batches      []int
	checkResults []bool // true = up, false = down, in call order
}

func (f *fakeMetricsRecorder) ObserveBatch(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches = append(f.batches, n)
}

func (f *fakeMetricsRecorder) ObserveCheck(up bool, _ time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkResults = append(f.checkResults, up)
}

func TestService_RunOnce_ReportsToMetricsRecorder(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	repo := newFakeHealthRepo()
	repo.addLink("up", "https://example.com/up", now)
	repo.addLink("down", "https://example.com/down", now)

	prober := newFakeProber()
	prober.fail("https://example.com/down", 0, errors.New("connection refused"))

	metrics := &fakeMetricsRecorder{}
	svc := checker.New(repo, prober,
		checker.WithClock(func() time.Time { return now }),
		checker.WithMetrics(metrics),
	)

	if _, err := svc.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() unexpected error: %v", err)
	}

	metrics.mu.Lock()
	defer metrics.mu.Unlock()

	if len(metrics.batches) != 1 || metrics.batches[0] != 2 {
		t.Errorf("batches recorded = %v, want [2]", metrics.batches)
	}
	if len(metrics.checkResults) != 2 {
		t.Fatalf("check results recorded = %d, want 2", len(metrics.checkResults))
	}

	var ups, downs int
	for _, up := range metrics.checkResults {
		if up {
			ups++
		} else {
			downs++
		}
	}
	if ups != 1 || downs != 1 {
		t.Errorf("recorded ups=%d downs=%d, want ups=1 downs=1", ups, downs)
	}
}

func TestService_RunOnce_NilMetricsRecorderIsSafe(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	repo := newFakeHealthRepo()
	repo.addLink("abc", "https://example.com", now)
	prober := newFakeProber()

	// No WithMetrics — must not panic, must behave exactly as before
	// metrics existed.
	svc := checker.New(repo, prober, checker.WithClock(func() time.Time { return now }))

	if _, err := svc.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() unexpected error: %v", err)
	}
}
