package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"urlshortener/internal/delivery/httpapi/dto"
	"urlshortener/internal/delivery/httpapi/handler"
	"urlshortener/internal/repository/memory"
	"urlshortener/internal/usecase/shortener"
)

func newTestService() *shortener.Service {
	return shortener.New(memory.NewLinkRepo(), memory.NewCodeGen(0))
}

func TestLinkHandler_Create(t *testing.T) {
	tests := []struct {
		name       string
		req        dto.CreateLinkRequest
		wantStatus int
	}{
		{
			name:       "valid url",
			req:        dto.CreateLinkRequest{URL: "https://example.com/path"},
			wantStatus: http.StatusCreated,
		},
		{
			name:       "invalid url",
			req:        dto.CreateLinkRequest{URL: "not-a-url"},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := handler.NewLinkHandler(newTestService())

			body, err := json.Marshal(tt.req)
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}

			req := httptest.NewRequest(http.MethodPost, "/v1/links", bytes.NewReader(body))
			rec := httptest.NewRecorder()

			h.Create(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestLinkHandler_Create_DuplicateAlias(t *testing.T) {
	h := handler.NewLinkHandler(newTestService())
	body, _ := json.Marshal(dto.CreateLinkRequest{URL: "https://example.com", CustomAlias: "promo"})

	first := httptest.NewRequest(http.MethodPost, "/v1/links", bytes.NewReader(body))
	h.Create(httptest.NewRecorder(), first)

	second := httptest.NewRequest(http.MethodPost, "/v1/links", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.Create(rec, second)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

func TestLinkHandler_Create_MalformedJSON(t *testing.T) {
	h := handler.NewLinkHandler(newTestService())

	req := httptest.NewRequest(http.MethodPost, "/v1/links", bytes.NewReader([]byte("{not json")))
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestLinkHandler_GetAndDeactivate(t *testing.T) {
	svc := newTestService()
	h := handler.NewLinkHandler(svc)

	body, _ := json.Marshal(dto.CreateLinkRequest{URL: "https://example.com", CustomAlias: "promo"})
	createReq := httptest.NewRequest(http.MethodPost, "/v1/links", bytes.NewReader(body))
	h.Create(httptest.NewRecorder(), createReq)

	getReq := httptest.NewRequest(http.MethodGet, "/v1/links/promo", nil)
	getReq.SetPathValue("code", "promo")
	getRec := httptest.NewRecorder()
	h.Get(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("Get status = %d, want %d, body=%s", getRec.Code, http.StatusOK, getRec.Body.String())
	}

	deactReq := httptest.NewRequest(http.MethodDelete, "/v1/links/promo", nil)
	deactReq.SetPathValue("code", "promo")
	deactRec := httptest.NewRecorder()
	h.Deactivate(deactRec, deactReq)

	if deactRec.Code != http.StatusNoContent {
		t.Fatalf("Deactivate status = %d, want %d", deactRec.Code, http.StatusNoContent)
	}

	// Get should still return the (now inactive) link — only the public
	// redirect hides deactivated links.
	getAfterReq := httptest.NewRequest(http.MethodGet, "/v1/links/promo", nil)
	getAfterReq.SetPathValue("code", "promo")
	getAfterRec := httptest.NewRecorder()
	h.Get(getAfterRec, getAfterReq)

	var got dto.LinkResponse
	if err := json.Unmarshal(getAfterRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Active {
		t.Error("Active = true after Deactivate, want false")
	}
}

func TestLinkHandler_Get_NotFound(t *testing.T) {
	h := handler.NewLinkHandler(newTestService())

	req := httptest.NewRequest(http.MethodGet, "/v1/links/missing", nil)
	req.SetPathValue("code", "missing")
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestRedirectHandler(t *testing.T) {
	svc := newTestService()
	linkHandler := handler.NewLinkHandler(svc)
	redirectHandler := handler.NewRedirectHandler(svc)

	body, _ := json.Marshal(dto.CreateLinkRequest{URL: "https://example.com/target", CustomAlias: "promo"})
	createReq := httptest.NewRequest(http.MethodPost, "/v1/links", bytes.NewReader(body))
	linkHandler.Create(httptest.NewRecorder(), createReq)

	redirectReq := httptest.NewRequest(http.MethodGet, "/r/promo", nil)
	redirectReq.SetPathValue("code", "promo")
	redirectRec := httptest.NewRecorder()

	redirectHandler.Redirect(redirectRec, redirectReq)

	if redirectRec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", redirectRec.Code, http.StatusFound)
	}
	if got := redirectRec.Header().Get("Location"); got != "https://example.com/target" {
		t.Errorf("Location = %q, want %q", got, "https://example.com/target")
	}
}

func TestRedirectHandler_NotFound(t *testing.T) {
	svc := newTestService()
	redirectHandler := handler.NewRedirectHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/r/missing", nil)
	req.SetPathValue("code", "missing")
	rec := httptest.NewRecorder()

	redirectHandler.Redirect(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestRedirectHandler_HidesDeactivatedLink(t *testing.T) {
	svc := newTestService()
	linkHandler := handler.NewLinkHandler(svc)
	redirectHandler := handler.NewRedirectHandler(svc)

	body, _ := json.Marshal(dto.CreateLinkRequest{URL: "https://example.com", CustomAlias: "promo"})
	createReq := httptest.NewRequest(http.MethodPost, "/v1/links", bytes.NewReader(body))
	linkHandler.Create(httptest.NewRecorder(), createReq)

	deactReq := httptest.NewRequest(http.MethodDelete, "/v1/links/promo", nil)
	deactReq.SetPathValue("code", "promo")
	linkHandler.Deactivate(httptest.NewRecorder(), deactReq)

	redirectReq := httptest.NewRequest(http.MethodGet, "/r/promo", nil)
	redirectReq.SetPathValue("code", "promo")
	rec := httptest.NewRecorder()

	redirectHandler.Redirect(rec, redirectReq)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
