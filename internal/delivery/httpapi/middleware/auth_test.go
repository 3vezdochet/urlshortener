package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"urlshortener/internal/auth"
	"urlshortener/internal/delivery/httpapi/middleware"
)

func TestAuth_MissingKey(t *testing.T) {
	store, _ := auth.ParseStaticKeys("sk_abc:frontend")
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/links", nil)

	middleware.Auth(store)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAuth_UnknownKey(t *testing.T) {
	store, _ := auth.ParseStaticKeys("sk_abc:frontend")
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/links", nil)
	req.Header.Set("X-API-Key", "sk_wrong")

	middleware.Auth(store)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAuth_ValidKey_AttachesPrincipal(t *testing.T) {
	store, _ := auth.ParseStaticKeys("sk_abc:frontend:Frontend Prod")

	var gotPrincipal auth.Principal
	var gotOK bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPrincipal, gotOK = auth.PrincipalFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/links", nil)
	req.Header.Set("X-API-Key", "sk_abc")

	middleware.Auth(store)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !gotOK {
		t.Fatal("PrincipalFromContext() ok = false in downstream handler, want true")
	}
	if gotPrincipal.OwnerID != "frontend" {
		t.Errorf("OwnerID = %q, want %q", gotPrincipal.OwnerID, "frontend")
	}
}

type erroringKeyStore struct{}

func (erroringKeyStore) Lookup(_ context.Context, _ string) (auth.Principal, bool, error) {
	return auth.Principal{}, false, errors.New("store unavailable")
}

func TestAuth_StoreError(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/links", nil)
	req.Header.Set("X-API-Key", "sk_abc")

	middleware.Auth(erroringKeyStore{})(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
