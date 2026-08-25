package httpprobe_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"urlshortener/internal/usecase/checker/httpprobe"
)

func TestProber_Up(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := httpprobe.New()
	status, err := p.Probe(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Probe() unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("status = %d, want %d", status, http.StatusOK)
	}
}

func TestProber_ClientErrorStillCountsAsUp(t *testing.T) {
	// A 404 means the server is alive and answering — that's "up" for
	// availability purposes, even though the specific page is gone.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := httpprobe.New()
	status, err := p.Probe(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Probe() unexpected error: %v", err)
	}
	if status != http.StatusNotFound {
		t.Errorf("status = %d, want %d", status, http.StatusNotFound)
	}
}

func TestProber_ServerErrorCountsAsDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := httpprobe.New()
	status, err := p.Probe(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("Probe() error = nil, want non-nil for a 5xx response")
	}
	if status != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", status, http.StatusInternalServerError)
	}
}

func TestProber_ConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.URL
	srv.Close() // nothing listens here anymore

	p := httpprobe.New()
	status, err := p.Probe(context.Background(), addr)
	if err == nil {
		t.Fatal("Probe() error = nil, want non-nil for a closed connection")
	}
	if status != 0 {
		t.Errorf("status = %d, want 0 (no response received)", status)
	}
}

func TestProber_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(200 * time.Millisecond):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	p := httpprobe.New(httpprobe.WithTimeout(20 * time.Millisecond))
	status, err := p.Probe(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("Probe() error = nil, want non-nil for a timeout")
	}
	if status != 0 {
		t.Errorf("status = %d, want 0 (no response received in time)", status)
	}
}

func TestProber_InvalidURL(t *testing.T) {
	p := httpprobe.New()
	_, err := p.Probe(context.Background(), "not a url at all://[[[")
	if err == nil {
		t.Fatal("Probe() error = nil, want non-nil for a malformed URL")
	}
}
