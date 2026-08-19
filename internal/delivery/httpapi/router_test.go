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

	"urlshortener/internal/delivery/httpapi"
	"urlshortener/internal/delivery/httpapi/dto"
	"urlshortener/internal/repository/memory"
	"urlshortener/internal/usecase/shortener"
)

type fakePinger struct{ err error }

func (p fakePinger) Ping(context.Context) error { return p.err }

func newTestRouter(t *testing.T, pinger *fakePinger) http.Handler {
	t.Helper()

	svc := shortener.New(memory.NewLinkRepo(), memory.NewCodeGen(0))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if pinger == nil {
		return httpapi.NewRouter(svc, nil, logger)
	}
	return httpapi.NewRouter(svc, pinger, logger)
}

func TestRouter_CreateThenRedirect(t *testing.T) {
	router := newTestRouter(t, nil)

	body, _ := json.Marshal(dto.CreateLinkRequest{URL: "https://example.com/path"})
	createReq := httptest.NewRequest(http.MethodPost, "/v1/links", bytes.NewReader(body))
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)

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

func TestRouter_RedirectNotFound(t *testing.T) {
	router := newTestRouter(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/r/missing", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestRouter_Healthz(t *testing.T) {
	router := newTestRouter(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRouter_ReadyzOmittedWithoutPinger(t *testing.T) {
	router := newTestRouter(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (route should not be registered)", rec.Code, http.StatusNotFound)
	}
}

func TestRouter_ReadyzReflectsPinger(t *testing.T) {
	pinger := &fakePinger{err: errors.New("db down")}
	router := newTestRouter(t, pinger)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestRouter_RequestIDPresentThroughFullChain(t *testing.T) {
	router := newTestRouter(t, nil)

	// Panic recovery itself is covered at the middleware unit level; this
	// just confirms RequestID survives being wrapped by Logging and
	// Recover in the real router, not only in isolation.
	req := httptest.NewRequest(http.MethodDelete, "/v1/links/missing", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("response missing X-Request-Id header")
	}
}
