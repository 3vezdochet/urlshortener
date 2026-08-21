package rediscache_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"urlshortener/internal/domain"
	"urlshortener/internal/repository/rediscache"
)

func newTestCache(t *testing.T) (*rediscache.Cache, *miniredis.Miniredis) {
	t.Helper()

	mr := miniredis.RunT(t) // fake in-process Redis server; t.Cleanup closes it
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	return rediscache.NewCache(client), mr
}

func TestCache_SetAndGet(t *testing.T) {
	cache, _ := newTestCache(t)
	ctx := context.Background()

	link := &domain.Link{
		Code: "abc", OriginalURL: "https://example.com",
		Active: true, CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}

	if err := cache.Set(ctx, link, time.Minute); err != nil {
		t.Fatalf("Set() unexpected error: %v", err)
	}

	got, ok, err := cache.Get(ctx, "abc")
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("Get() ok = false, want true")
	}
	if got.OriginalURL != link.OriginalURL {
		t.Errorf("OriginalURL = %q, want %q", got.OriginalURL, link.OriginalURL)
	}
	if got.Active != link.Active {
		t.Errorf("Active = %v, want %v", got.Active, link.Active)
	}
}

func TestCache_Get_Miss(t *testing.T) {
	cache, _ := newTestCache(t)

	_, ok, err := cache.Get(context.Background(), "missing")
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if ok {
		t.Error("Get() ok = true, want false for an uncached code")
	}
}

func TestCache_SetMissingAndIsMissing(t *testing.T) {
	cache, _ := newTestCache(t)
	ctx := context.Background()

	if err := cache.SetMissing(ctx, "gone", time.Minute); err != nil {
		t.Fatalf("SetMissing() unexpected error: %v", err)
	}

	missing, err := cache.IsMissing(ctx, "gone")
	if err != nil {
		t.Fatalf("IsMissing() unexpected error: %v", err)
	}
	if !missing {
		t.Error("IsMissing() = false, want true")
	}

	missing, err = cache.IsMissing(ctx, "never-set")
	if err != nil {
		t.Fatalf("IsMissing() unexpected error: %v", err)
	}
	if missing {
		t.Error("IsMissing() = true for a code that was never recorded, want false")
	}
}

func TestCache_Invalidate(t *testing.T) {
	cache, _ := newTestCache(t)
	ctx := context.Background()

	if err := cache.Set(ctx, &domain.Link{Code: "abc", OriginalURL: "https://example.com"}, time.Minute); err != nil {
		t.Fatalf("Set() unexpected error: %v", err)
	}
	if err := cache.SetMissing(ctx, "xyz", time.Minute); err != nil {
		t.Fatalf("SetMissing() unexpected error: %v", err)
	}

	if err := cache.Invalidate(ctx, "abc"); err != nil {
		t.Fatalf("Invalidate(abc) unexpected error: %v", err)
	}
	if err := cache.Invalidate(ctx, "xyz"); err != nil {
		t.Fatalf("Invalidate(xyz) unexpected error: %v", err)
	}

	if _, ok, _ := cache.Get(ctx, "abc"); ok {
		t.Error("Get(abc) ok = true after Invalidate, want false")
	}
	if missing, _ := cache.IsMissing(ctx, "xyz"); missing {
		t.Error("IsMissing(xyz) = true after Invalidate, want false")
	}
}

func TestCache_Invalidate_NonExistentCodeIsNotAnError(t *testing.T) {
	cache, _ := newTestCache(t)

	if err := cache.Invalidate(context.Background(), "never-existed"); err != nil {
		t.Fatalf("Invalidate() unexpected error: %v", err)
	}
}

func TestCache_Set_RespectsTTL(t *testing.T) {
	cache, mr := newTestCache(t)
	ctx := context.Background()

	link := &domain.Link{Code: "abc", OriginalURL: "https://example.com"}
	if err := cache.Set(ctx, link, 30*time.Second); err != nil {
		t.Fatalf("Set() unexpected error: %v", err)
	}

	mr.FastForward(31 * time.Second)

	_, ok, err := cache.Get(ctx, "abc")
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if ok {
		t.Error("Get() ok = true after TTL elapsed, want false")
	}
}

func TestCache_SetMissing_RespectsTTL(t *testing.T) {
	cache, mr := newTestCache(t)
	ctx := context.Background()

	if err := cache.SetMissing(ctx, "gone", 30*time.Second); err != nil {
		t.Fatalf("SetMissing() unexpected error: %v", err)
	}

	mr.FastForward(31 * time.Second)

	missing, err := cache.IsMissing(ctx, "gone")
	if err != nil {
		t.Fatalf("IsMissing() unexpected error: %v", err)
	}
	if missing {
		t.Error("IsMissing() = true after TTL elapsed, want false")
	}
}

func TestCache_PreservesExpiresAt(t *testing.T) {
	cache, _ := newTestCache(t)
	ctx := context.Background()

	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)
	link := &domain.Link{Code: "abc", OriginalURL: "https://example.com", ExpiresAt: &expires}

	if err := cache.Set(ctx, link, time.Minute); err != nil {
		t.Fatalf("Set() unexpected error: %v", err)
	}

	got, ok, err := cache.Get(ctx, "abc")
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("Get() ok = false, want true")
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(expires) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, expires)
	}
}

// The tests below close the underlying server first, so every call hits a
// real connection error — the branches a happy-path miniredis server never
// exercises.

func TestCache_Get_ConnectionError(t *testing.T) {
	cache, mr := newTestCache(t)
	mr.Close()

	if _, _, err := cache.Get(context.Background(), "abc"); err == nil {
		t.Fatal("Get() error = nil, want non-nil after the server closed")
	}
}

func TestCache_Set_ConnectionError(t *testing.T) {
	cache, mr := newTestCache(t)
	mr.Close()

	link := &domain.Link{Code: "abc", OriginalURL: "https://example.com"}
	if err := cache.Set(context.Background(), link, time.Minute); err == nil {
		t.Fatal("Set() error = nil, want non-nil after the server closed")
	}
}

func TestCache_SetMissing_ConnectionError(t *testing.T) {
	cache, mr := newTestCache(t)
	mr.Close()

	if err := cache.SetMissing(context.Background(), "abc", time.Minute); err == nil {
		t.Fatal("SetMissing() error = nil, want non-nil after the server closed")
	}
}

func TestCache_IsMissing_ConnectionError(t *testing.T) {
	cache, mr := newTestCache(t)
	mr.Close()

	if _, err := cache.IsMissing(context.Background(), "abc"); err == nil {
		t.Fatal("IsMissing() error = nil, want non-nil after the server closed")
	}
}

func TestCache_Invalidate_ConnectionError(t *testing.T) {
	cache, mr := newTestCache(t)
	mr.Close()

	if err := cache.Invalidate(context.Background(), "abc"); err == nil {
		t.Fatal("Invalidate() error = nil, want non-nil after the server closed")
	}
}
