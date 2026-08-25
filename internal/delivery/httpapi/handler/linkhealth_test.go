package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"urlshortener/internal/delivery/httpapi/dto"
	"urlshortener/internal/delivery/httpapi/handler"
	"urlshortener/internal/domain"
)

type fakeHealthGetter struct {
	health *domain.LinkHealth
	err    error
}

func (f fakeHealthGetter) GetByCode(context.Context, string) (*domain.LinkHealth, error) {
	return f.health, f.err
}

func TestLinkHealthHandler_Get(t *testing.T) {
	checkedAt := time.Now().UTC()
	getter := fakeHealthGetter{health: &domain.LinkHealth{
		Code: "abc", Status: domain.HealthStatusUp,
		LastCheckedAt: &checkedAt, NextCheckAt: checkedAt.Add(10 * time.Minute),
	}}
	h := handler.NewLinkHealthHandler(getter)

	req := httptest.NewRequest(http.MethodGet, "/v1/links/abc/health", nil)
	req.SetPathValue("code", "abc")
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got dto.HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Status != string(domain.HealthStatusUp) {
		t.Errorf("Status = %q, want %q", got.Status, domain.HealthStatusUp)
	}
}

func TestLinkHealthHandler_Get_NotFound(t *testing.T) {
	getter := fakeHealthGetter{err: domain.ErrLinkNotFound}
	h := handler.NewLinkHealthHandler(getter)

	req := httptest.NewRequest(http.MethodGet, "/v1/links/missing/health", nil)
	req.SetPathValue("code", "missing")
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
