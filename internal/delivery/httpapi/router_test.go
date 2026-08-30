package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"urlshortener/internal/auth"
	"urlshortener/internal/delivery/httpapi"
	"urlshortener/internal/delivery/httpapi/dto"
	"urlshortener/internal/domain"
	"urlshortener/internal/ratelimit"
	"urlshortener/internal/repository/memory"
	"urlshortener/internal/usecase/shortener"
)

const (
	testAPIKey  = "test-key"
	testOwnerID = "test-owner"
)

type fakePinger struct{ err error }

func (p fakePinger) Ping(context.Context) error { return p.err }

type fakeLimiter struct {
	decision ratelimit.Decision
	err      error
}

func (f fakeLimiter) Allow(context.Context, string) (ratelimit.Decision, error) {
	return f.decision, f.err
}

type fakeHealthGetter struct {
	health *domain.LinkHealth
	err    error
}

func (f fakeHealthGetter) GetByCode(context.Context, string) (*domain.LinkHealth, error) {
	return f.health, f.err
}

type fakeHTTPMetricsRecorder struct {
	calls int
}

func (f *fakeHTTPMetricsRecorder) RecordRequest(string, string, string, time.Duration) {
	f.calls++
}

// newTestDeps builds a valid httpapi.Deps with a single API key
// (testAPIKey / testOwnerID) and every optional field left at its zero
// value (nil) — tests override individual fields from the returned struct.
func newTestDeps(t *testing.T) httpapi.Deps {
	t.Helper()

	keyStore, err := auth.ParseStaticKeys(testAPIKey + ":" + testOwnerID)
	if err != nil {
		t.Fatalf("ParseStaticKeys() unexpected error: %v", err)
	}

	return httpapi.Deps{
		Service:  shortener.New(memory.NewLinkRepo(), memory.NewCodeGen(0)),
		KeyStore: keyStore,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func createLinkRequest(body []byte) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/links", bytes.NewReader(body))
	req.Header.Set("X-API-Key", testAPIKey)
	return req
}

func TestRouter_CreateThenRedirect(t *testing.T) {
	router := httpapi.NewRouter(newTestDeps(t))

	body, _ := json.Marshal(dto.CreateLinkRequest{URL: "https://example.com/path"})
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createLinkRequest(body))

	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d, body=%s", createRec.Code, http.StatusCreated, createRec.Body.String())
	}
	if createRec.Header().Get("X-Request-Id") == "" {
		t.Error("response missing X-Request-Id header")
	}

	var created dto.LinkResponse
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Code == "" {
		t.Fatal("created.Code is empty")
	}

	redirectReq := httptest.NewRequest(http.MethodGet, "/r/"+created.Code, nil)
	redirectRec := httptest.NewRecorder()
	router.ServeHTTP(redirectRec, redirectReq)

	if redirectRec.Code != http.StatusFound {
		t.Fatalf("redirect status = %d, want %d", redirectRec.Code, http.StatusFound)
	}
	if got := redirectRec.Header().Get("Location"); got != created.OriginalURL {
		t.Errorf("Location = %q, want %q", got, created.OriginalURL)
	}
}

func TestRouter_Create_RequiresAPIKey(t *testing.T) {
	router := httpapi.NewRouter(newTestDeps(t))

	body, _ := json.Marshal(dto.CreateLinkRequest{URL: "https://example.com"})
	req := httptest.NewRequest(http.MethodPost, "/v1/links", bytes.NewReader(body)) // no X-API-Key
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestRouter_Create_RejectsUnknownAPIKey(t *testing.T) {
	router := httpapi.NewRouter(newTestDeps(t))

	body, _ := json.Marshal(dto.CreateLinkRequest{URL: "https://example.com"})
	req := httptest.NewRequest(http.MethodPost, "/v1/links", bytes.NewReader(body))
	req.Header.Set("X-API-Key", "not-a-real-key")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestRouter_Deactivate_RejectsNonOwner(t *testing.T) {
	deps := newTestDeps(t)
	keyStore, err := auth.ParseStaticKeys(testAPIKey + ":" + testOwnerID + ",other-key:other-owner")
	if err != nil {
		t.Fatalf("ParseStaticKeys() unexpected error: %v", err)
	}
	deps.KeyStore = keyStore
	router := httpapi.NewRouter(deps)

	body, _ := json.Marshal(dto.CreateLinkRequest{URL: "https://example.com", CustomAlias: "promo"})
	router.ServeHTTP(httptest.NewRecorder(), createLinkRequest(body))

	deactReq := httptest.NewRequest(http.MethodDelete, "/v1/links/promo", nil)
	deactReq.Header.Set("X-API-Key", "other-key")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, deactReq)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestRouter_RedirectNotFound(t *testing.T) {
	router := httpapi.NewRouter(newTestDeps(t))

	req := httptest.NewRequest(http.MethodGet, "/r/missing", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestRouter_Redirect_RateLimited(t *testing.T) {
	deps := newTestDeps(t)
	deps.Limiter = fakeLimiter{decision: ratelimit.Decision{Allowed: false, RetryAfter: 0}}
	router := httpapi.NewRouter(deps)

	req := httptest.NewRequest(http.MethodGet, "/r/anything", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
}

func TestRouter_Redirect_NotRateLimitedWhenLimiterUnset(t *testing.T) {
	router := httpapi.NewRouter(newTestDeps(t))

	req := httptest.NewRequest(http.MethodGet, "/r/missing", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	// Still a plain 404 (unknown code), not 429 — confirms rate limiting
	// is truly off, not just currently allowing.
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestRouter_Healthz(t *testing.T) {
	router := httpapi.NewRouter(newTestDeps(t))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRouter_ReadyzOmittedWithoutPinger(t *testing.T) {
	router := httpapi.NewRouter(newTestDeps(t))

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (route should not be registered)", rec.Code, http.StatusNotFound)
	}
}

func TestRouter_ReadyzReflectsPinger(t *testing.T) {
	deps := newTestDeps(t)
	deps.Pinger = fakePinger{err: errors.New("db down")}
	router := httpapi.NewRouter(deps)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestRouter_HealthEndpointOmittedWithoutGetter(t *testing.T) {
	router := httpapi.NewRouter(newTestDeps(t))

	req := httptest.NewRequest(http.MethodGet, "/v1/links/abc/health", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (route should not be registered)", rec.Code, http.StatusNotFound)
	}
}

func TestRouter_HealthEndpointReflectsGetter(t *testing.T) {
	deps := newTestDeps(t)
	deps.HealthGetter = fakeHealthGetter{health: &domain.LinkHealth{Code: "abc", Status: domain.HealthStatusUp}}
	router := httpapi.NewRouter(deps)

	req := httptest.NewRequest(http.MethodGet, "/v1/links/abc/health", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestRouter_MetricsEndpointOmittedWithoutHandler(t *testing.T) {
	router := httpapi.NewRouter(newTestDeps(t))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (route should not be registered)", rec.Code, http.StatusNotFound)
	}
}

func TestRouter_MetricsEndpointServesProvidedHandler(t *testing.T) {
	deps := newTestDeps(t)
	deps.MetricsHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("# metrics placeholder\n"))
	})
	router := httpapi.NewRouter(deps)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "# metrics placeholder\n" {
		t.Errorf("body = %q, want the provided handler's own output", rec.Body.String())
	}
}

func TestRouter_HTTPMetrics_RecordsOnConfiguredRoutes(t *testing.T) {
	rec := &fakeHTTPMetricsRecorder{}
	deps := newTestDeps(t)
	deps.HTTPMetrics = rec
	router := httpapi.NewRouter(deps)

	req := httptest.NewRequest(http.MethodGet, "/r/missing", nil)
	router.ServeHTTP(httptest.NewRecorder(), req)

	if rec.calls != 1 {
		t.Errorf("RecordRequest called %d times, want 1", rec.calls)
	}
}

func TestRouter_HTTPMetrics_NotRecordedForLivenessOrMetrics(t *testing.T) {
	httpMetrics := &fakeHTTPMetricsRecorder{}
	deps := newTestDeps(t)
	deps.HTTPMetrics = httpMetrics
	deps.MetricsHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	router := httpapi.NewRouter(deps)

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if httpMetrics.calls != 0 {
		t.Errorf("RecordRequest called %d times for /healthz and /metrics, want 0", httpMetrics.calls)
	}
}

func TestRouter_RequestIDPresentThroughFullChain(t *testing.T) {
	router := httpapi.NewRouter(newTestDeps(t))

	// Panic recovery itself is covered at the middleware unit level; this
	// just confirms RequestID survives being wrapped by every other layer
	// (Logging, Recover, Auth) in the real router, not only in isolation.
	req := httptest.NewRequest(http.MethodDelete, "/v1/links/missing", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("response missing X-Request-Id header")
	}
}
