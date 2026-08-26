package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"urlshortener/internal/auth"
	"urlshortener/internal/delivery/httpapi/dto"
	"urlshortener/internal/usecase/shortener"
)

// maxBodyBytes caps request bodies to guard against abuse independent of
// rate limiting — a cheap first line of defense that costs nothing even
// when the rate limiter is unavailable or disabled.
const maxBodyBytes = 1 << 20 // 1 MiB

// LinkHandler serves the link management endpoints: create, fetch
// metadata, deactivate.
type LinkHandler struct {
	svc *shortener.Service
}

// NewLinkHandler returns a LinkHandler backed by svc.
func NewLinkHandler(svc *shortener.Service) *LinkHandler {
	return &LinkHandler{svc: svc}
}

// Create handles POST /v1/links. Always installed behind middleware.Auth —
// the created link's OwnerID comes from the authenticated Principal, never
// from the request body, so a caller can't claim links on someone else's
// behalf.
func (h *LinkHandler) Create(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		// Unreachable if the router wires Auth in front of this route, as
		// it must — defensive rather than trusting that wiring silently.
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var req dto.CreateLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	link, err := h.svc.Create(r.Context(), shortener.CreateRequest{
		OriginalURL: req.URL,
		OwnerID:     principal.OwnerID,
		CustomAlias: req.CustomAlias,
		TTL:         time.Duration(req.TTLSeconds) * time.Second,
	})
	if err != nil {
		respondDomainError(w, err)
		return
	}

	respondJSON(w, http.StatusCreated, dto.FromDomain(link))
}

// Get handles GET /v1/links/{code}. Public: read-only metadata for a code
// you already have is not a secret the way creating or deactivating a
// link is.
func (h *LinkHandler) Get(w http.ResponseWriter, r *http.Request) {
	link, err := h.svc.Get(r.Context(), r.PathValue("code"))
	if err != nil {
		respondDomainError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, dto.FromDomain(link))
}

// Deactivate handles DELETE /v1/links/{code}. Always installed behind
// middleware.Auth, and additionally checks that the caller owns the link —
// authentication alone would let any valid API key deactivate anyone's
// links, not just its own.
func (h *LinkHandler) Deactivate(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	code := r.PathValue("code")

	link, err := h.svc.Get(r.Context(), code)
	if err != nil {
		respondDomainError(w, err)
		return
	}
	if link.OwnerID != principal.OwnerID {
		respondError(w, http.StatusForbidden, "not the owner of this link")
		return
	}

	if err := h.svc.Deactivate(r.Context(), code); err != nil {
		respondDomainError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
