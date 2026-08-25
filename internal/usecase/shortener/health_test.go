package shortener_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"urlshortener/internal/repository/memory"
	"urlshortener/internal/usecase/shortener"
)

// fakeHealthScheduler is an in-memory shortener.HealthScheduler double
// that records what it was asked to schedule and can be made to fail.
type fakeHealthScheduler struct {
	mu       sync.Mutex
	err      error
	calls    []string
	dueTimes map[string]time.Time
}

func newFakeHealthScheduler() *fakeHealthScheduler {
	return &fakeHealthScheduler{dueTimes: make(map[string]time.Time)}
}

func (f *fakeHealthScheduler) EnsureScheduled(_ context.Context, code string, dueAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, code)
	f.dueTimes[code] = dueAt
	return f.err
}

func (f *fakeHealthScheduler) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func TestService_Create_SchedulesHealthCheckWhenConfigured(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	scheduler := newFakeHealthScheduler()
	svc := shortener.New(memory.NewLinkRepo(), memory.NewCodeGen(0),
		shortener.WithHealthScheduler(scheduler),
		shortener.WithClock(func() time.Time { return now }),
	)

	link, err := svc.Create(context.Background(), shortener.CreateRequest{OriginalURL: "https://example.com"})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	if scheduler.callCount() != 1 {
		t.Fatalf("EnsureScheduled called %d times, want 1", scheduler.callCount())
	}
	if got := scheduler.dueTimes[link.Code]; !got.Equal(now) {
		t.Errorf("dueAt = %v, want %v", got, now)
	}
}

func TestService_Create_NoSchedulingWithoutHealthScheduler(t *testing.T) {
	// No WithHealthScheduler — Create must work exactly as it did before
	// checking existed, and obviously never panic on a nil scheduler.
	svc := shortener.New(memory.NewLinkRepo(), memory.NewCodeGen(0))

	if _, err := svc.Create(context.Background(), shortener.CreateRequest{OriginalURL: "https://example.com"}); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
}

func TestService_Create_SchedulerErrorDoesNotFailCreate(t *testing.T) {
	scheduler := newFakeHealthScheduler()
	scheduler.err = errors.New("health repo unavailable")
	svc := shortener.New(memory.NewLinkRepo(), memory.NewCodeGen(0), shortener.WithHealthScheduler(scheduler))

	link, err := svc.Create(context.Background(), shortener.CreateRequest{OriginalURL: "https://example.com"})
	if err != nil {
		t.Fatalf("Create() unexpected error despite scheduler failure: %v", err)
	}
	if link == nil {
		t.Fatal("Create() returned nil link despite scheduler failure")
	}
}
