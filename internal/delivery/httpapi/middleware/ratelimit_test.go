package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"urlshortener/internal/auth"
	"urlshortener/internal/delivery/httpapi/middleware"
	"urlshortener/internal/ratelimit"
)

type fakeLimiter struct {
	decision ratelimit.Decision
	err      error
	lastKey  string
}

func (f *fakeLimiter) Allow(_ context.Context, key string) (ratelimit.Decision, error) {
	f.lastKey = key
	return f.decision, f.err
}

func TestRateLimit_Allowed(t *testing.T) {
	limiter := &fakeLimiter{decision: ratelimit.Decision{Allowed: true}}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/r/abc", nil)

	middleware.RateLimit(limiter, middleware.ClientIPKey)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRateLimit_Denied(t *testing.T) {
	limiter := &fakeLimiter{decision: ratelimit.Decision{Allowed: false, RetryAfter: 5 * time.Second}}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/r/abc", nil)

	middleware.RateLimit(limiter, middleware.ClientIPKey)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
	if got := rec.Header().Get("Retry-After"); got != "5" {
		t.Errorf("Retry-After = %q, want %q", got, "5")
	}
}

func TestRateLimit_LimiterErrorFailsOpen(t *testing.T) {
	limiter := &fakeLimiter{err: errors.New("rate limiter unavailable")}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/r/abc", nil)

	middleware.RateLimit(limiter, middleware.ClientIPKey)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (limiter errors should fail open, not block traffic)", rec.Code, http.StatusOK)
	}
}

func TestClientIPKey(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		forwardFor string
		want       string
	}{
		{name: "plain remote addr", remoteAddr: "203.0.113.5:54321", want: "203.0.113.5"},
		{name: "forwarded for single", remoteAddr: "10.0.0.1:1234", forwardFor: "203.0.113.7", want: "203.0.113.7"},
		{name: "forwarded for chain uses first hop", remoteAddr: "10.0.0.1:1234", forwardFor: "203.0.113.7, 10.0.0.2", want: "203.0.113.7"},
		{name: "malformed remote addr falls back verbatim", remoteAddr: "not-a-host-port", want: "not-a-host-port"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/r/abc", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.forwardFor != "" {
				req.Header.Set("X-Forwarded-For", tt.forwardFor)
			}

			if got := middleware.ClientIPKey(req); got != tt.want {
				t.Errorf("ClientIPKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrincipalKey_UsesAuthenticatedOwner(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/links", nil)
	ctx := auth.WithPrincipal(req.Context(), auth.Principal{OwnerID: "frontend"})
	req = req.WithContext(ctx)

	if got := middleware.PrincipalKey(req); got != "frontend" {
		t.Errorf("PrincipalKey() = %q, want %q", got, "frontend")
	}
}

func TestPrincipalKey_FallsBackToIPWithoutPrincipal(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/links", nil)
	req.RemoteAddr = "203.0.113.5:54321"

	if got := middleware.PrincipalKey(req); got != "203.0.113.5" {
		t.Errorf("PrincipalKey() = %q, want %q", got, "203.0.113.5")
	}
}

func TestRateLimit_PassesKeyFromKeyFunc(t *testing.T) {
	limiter := &fakeLimiter{decision: ratelimit.Decision{Allowed: true}}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/r/abc", nil)

	middleware.RateLimit(limiter, func(*http.Request) string { return "fixed-key" })(next).ServeHTTP(rec, req)

	if limiter.lastKey != "fixed-key" {
		t.Errorf("limiter received key %q, want %q", limiter.lastKey, "fixed-key")
	}
}
