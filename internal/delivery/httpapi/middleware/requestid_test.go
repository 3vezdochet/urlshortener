package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"urlshortener/internal/delivery/httpapi/middleware"
)

func TestRequestID_GeneratesWhenAbsent(t *testing.T) {
	var gotID string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotID = middleware.RequestIDFromContext(r.Context())
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	middleware.RequestID(next).ServeHTTP(rec, req)

	if gotID == "" {
		t.Error("RequestIDFromContext() returned empty string")
	}
	if got := rec.Header().Get("X-Request-Id"); got != gotID {
		t.Errorf("X-Request-Id header = %q, want %q", got, gotID)
	}
}

func TestRequestID_ReusesInboundHeader(t *testing.T) {
	const inbound = "trace-123"

	var gotID string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotID = middleware.RequestIDFromContext(r.Context())
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", inbound)

	middleware.RequestID(next).ServeHTTP(rec, req)

	if gotID != inbound {
		t.Errorf("RequestIDFromContext() = %q, want %q", gotID, inbound)
	}
}

func TestRequestID_MissingFromContext(t *testing.T) {
	if got := middleware.RequestIDFromContext(context.Background()); got != "" {
		t.Errorf("RequestIDFromContext() on bare context = %q, want empty", got)
	}
}
