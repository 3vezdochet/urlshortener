package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"urlshortener/internal/delivery/httpapi/middleware"
)

// fakeMetricsRecorder is a middleware.HTTPMetricsRecorder double — this
// alone is why the interface split from the Prometheus adapter is worth
// it: WrapMetrics is fully testable here without client_golang.
type fakeMetricsRecorder struct {
	mu    sync.Mutex
	calls []recordedRequest
}

type recordedRequest struct {
	method, route, status string
	duration              time.Duration
}

func (f *fakeMetricsRecorder) RecordRequest(method, route, status string, duration time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, recordedRequest{method: method, route: route, status: status, duration: duration})
}

func TestWrapMetrics_RecordsRequest(t *testing.T) {
	rec := &fakeMetricsRecorder{}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/links", nil)

	middleware.WrapMetrics(rec, "POST /v1/links", next).ServeHTTP(w, req)

	if len(rec.calls) != 1 {
		t.Fatalf("RecordRequest called %d times, want 1", len(rec.calls))
	}
	got := rec.calls[0]
	if got.method != http.MethodPost || got.route != "POST /v1/links" || got.status != "201" {
		t.Errorf("recorded = %+v, want method=POST route=\"POST /v1/links\" status=201", got)
	}
}

func TestWrapMetrics_DefaultsToOKWhenHandlerDoesNotSetStatus(t *testing.T) {
	rec := &fakeMetricsRecorder{}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	middleware.WrapMetrics(rec, "GET /healthz", next).ServeHTTP(w, req)

	if len(rec.calls) != 1 || rec.calls[0].status != "200" {
		t.Fatalf("recorded = %+v, want status=200", rec.calls)
	}
}

func TestWrapMetrics_NilRecorderIsPassthrough(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/r/abc", nil)

	middleware.WrapMetrics(nil, "GET /r/{code}", next).ServeHTTP(w, req)

	if !called {
		t.Error("wrapped handler was never called")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}
