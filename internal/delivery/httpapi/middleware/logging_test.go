package middleware_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"urlshortener/internal/delivery/httpapi/middleware"
)

func TestLogging_RecordsStatusAndPath(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/brew", nil)

	middleware.Logging(logger)(next).ServeHTTP(rec, req)

	out := buf.String()
	if !strings.Contains(out, "418") {
		t.Errorf("log output missing status code 418, got: %s", out)
	}
	if !strings.Contains(out, "/brew") {
		t.Errorf("log output missing path /brew, got: %s", out)
	}
}

func TestLogging_DefaultsToOKWhenHandlerDoesNotSetStatus(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	middleware.Logging(logger)(next).ServeHTTP(rec, req)

	if !strings.Contains(buf.String(), "200") {
		t.Errorf("log output missing default status 200, got: %s", buf.String())
	}
}
