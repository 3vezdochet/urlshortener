package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"urlshortener/internal/auth"
	"urlshortener/internal/delivery/httpapi/dto"
	"urlshortener/internal/delivery/httpapi/handler"
	"urlshortener/internal/repository/memory"
	"urlshortener/internal/usecase/shortener"
)

func newTestService() *shortener.Service {
	return shortener.New(memory.NewLinkRepo(), memory.NewCodeGen(0))
}

// asPrincipal returns req with an authenticated Principal attached to its
// context, standing in for what middleware.Auth would have done — these
// are handler-level tests, invoked below Auth, not through it.
func asPrincipal(req *http.Request, ownerID string) *http.Request {
	ctx := auth.WithPrincipal(req.Context(), auth.Principal{OwnerID: ownerID})
	return req.WithContext(ctx)
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

			req := asPrincipal(httptest.NewRequest(http.MethodPost, "/v1/links", bytes.NewReader(body)), "owner-1")
			rec := httptest.NewRecorder()

			h.Create(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestLinkHandler_Create_RequiresPrincipal(t *testing.T) {
	h := handler.NewLinkHandler(newTestService())
	body, _ := json.Marshal(dto.CreateLinkRequest{URL: "https://example.com"})

	// No asPrincipal here — simulates the route somehow being reached
	// without going through Auth first.
	req := httptest.NewRequest(http.MethodPost, "/v1/links", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestLinkHandler_Create_SetsOwnerFromPrincipal(t *testing.T) {
	svc := newTestService()
	h := handler.NewLinkHandler(svc)

	body, _ := json.Marshal(dto.CreateLinkRequest{URL: "https://example.com", CustomAlias: "promo"})
	req := asPrincipal(httptest.NewRequest(http.MethodPost, "/v1/links", bytes.NewReader(body)), "owner-1")
	h.Create(httptest.NewRecorder(), req)

	link, err := svc.Get(req.Context(), "promo")
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if link.OwnerID != "owner-1" {
		t.Errorf("OwnerID = %q, want %q (should come from the Principal, not request body)", link.OwnerID, "owner-1")
	}
}

func TestLinkHandler_Create_DuplicateAlias(t *testing.T) {
	h := handler.NewLinkHandler(newTestService())
	body, _ := json.Marshal(dto.CreateLinkRequest{URL: "https://example.com", CustomAlias: "promo"})

	first := asPrincipal(httptest.NewRequest(http.MethodPost, "/v1/links", bytes.NewReader(body)), "owner-1")
	h.Create(httptest.NewRecorder(), first)

	second := asPrincipal(httptest.NewRequest(http.MethodPost, "/v1/links", bytes.NewReader(body)), "owner-1")
	rec := httptest.NewRecorder()
	h.Create(rec, second)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

func TestLinkHandler_Create_MalformedJSON(t *testing.T) {
	h := handler.NewLinkHandler(newTestService())

	req := asPrincipal(httptest.NewRequest(http.MethodPost, "/v1/links", bytes.NewReader([]byte("{not json"))), "owner-1")
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
	createReq := asPrincipal(httptest.NewRequest(http.MethodPost, "/v1/links", bytes.NewReader(body)), "owner-1")
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
	deactReq = asPrincipal(deactReq, "owner-1")
	deactRec := httptest.NewRecorder()
	h.Deactivate(deactRec, deactReq)

	if deactRec.Code != http.StatusNoContent {
		t.Fatalf("Deactivate status = %d, want %d, body=%s", deactRec.Code, http.StatusNoContent, deactRec.Body.String())
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

func TestLinkHandler_Deactivate_RequiresPrincipal(t *testing.T) {
	svc := newTestService()
	h := handler.NewLinkHandler(svc)

	body, _ := json.Marshal(dto.CreateLinkRequest{URL: "https://example.com", CustomAlias: "promo"})
	createReq := asPrincipal(httptest.NewRequest(http.MethodPost, "/v1/links", bytes.NewReader(body)), "owner-1")
	h.Create(httptest.NewRecorder(), createReq)

	deactReq := httptest.NewRequest(http.MethodDelete, "/v1/links/promo", nil)
	deactReq.SetPathValue("code", "promo")
	rec := httptest.NewRecorder()
	h.Deactivate(rec, deactReq)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestLinkHandler_Deactivate_RejectsNonOwner(t *testing.T) {
	svc := newTestService()
	h := handler.NewLinkHandler(svc)

	body, _ := json.Marshal(dto.CreateLinkRequest{URL: "https://example.com", CustomAlias: "promo"})
	createReq := asPrincipal(httptest.NewRequest(http.MethodPost, "/v1/links", bytes.NewReader(body)), "owner-1")
	h.Create(httptest.NewRecorder(), createReq)

	deactReq := httptest.NewRequest(http.MethodDelete, "/v1/links/promo", nil)
	deactReq.SetPathValue("code", "promo")
	deactReq = asPrincipal(deactReq, "someone-else")
	rec := httptest.NewRecorder()
	h.Deactivate(rec, deactReq)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}

	// And the link must still be active — the rejected attempt shouldn't
	// have had any effect.
	link, err := svc.Get(deactReq.Context(), "promo")
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if !link.Active {
		t.Error("Active = false after a rejected non-owner Deactivate, want true")
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
	createReq := asPrincipal(httptest.NewRequest(http.MethodPost, "/v1/links", bytes.NewReader(body)), "owner-1")
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
	createReq := asPrincipal(httptest.NewRequest(http.MethodPost, "/v1/links", bytes.NewReader(body)), "owner-1")
	linkHandler.Create(httptest.NewRecorder(), createReq)

	deactReq := httptest.NewRequest(http.MethodDelete, "/v1/links/promo", nil)
	deactReq.SetPathValue("code", "promo")
	deactReq = asPrincipal(deactReq, "owner-1")
	linkHandler.Deactivate(httptest.NewRecorder(), deactReq)

	redirectReq := httptest.NewRequest(http.MethodGet, "/r/promo", nil)
	redirectReq.SetPathValue("code", "promo")
	rec := httptest.NewRecorder()

	redirectHandler.Redirect(rec, redirectReq)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
