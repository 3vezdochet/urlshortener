package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"urlshortener/internal/delivery/httpapi/dto"
	"urlshortener/internal/usecase/shortener"
)

// maxBodyBytes caps request bodies to guard against abuse before any
// rate limiting is in place (planned for a later phase).
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

// Create handles POST /v1/links.
func (h *LinkHandler) Create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var req dto.CreateLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	link, err := h.svc.Create(r.Context(), shortener.CreateRequest{
		OriginalURL: req.URL,
		CustomAlias: req.CustomAlias,
		TTL:         time.Duration(req.TTLSeconds) * time.Second,
	})
	if err != nil {
		respondDomainError(w, err)
		return
	}

	respondJSON(w, http.StatusCreated, dto.FromDomain(link))
}

// Get handles GET /v1/links/{code}.
func (h *LinkHandler) Get(w http.ResponseWriter, r *http.Request) {
	link, err := h.svc.Get(r.Context(), r.PathValue("code"))
	if err != nil {
		respondDomainError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, dto.FromDomain(link))
}

// Deactivate handles DELETE /v1/links/{code}.
func (h *LinkHandler) Deactivate(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Deactivate(r.Context(), r.PathValue("code")); err != nil {
		respondDomainError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
