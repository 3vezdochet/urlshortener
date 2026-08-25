package handler

import (
	"context"
	"net/http"

	"urlshortener/internal/delivery/httpapi/dto"
	"urlshortener/internal/domain"
)

// HealthGetter is satisfied by domain.HealthRepository (e.g.
// *postgres.HealthRepo) directly — same method name and signature, so no
// adapter is needed between the checker's storage and this read path.
type HealthGetter interface {
	GetByCode(ctx context.Context, code string) (*domain.LinkHealth, error)
}

// LinkHealthHandler serves GET /v1/links/{code}/health.
type LinkHealthHandler struct {
	getter HealthGetter
}

// NewLinkHealthHandler returns a LinkHealthHandler backed by getter.
func NewLinkHealthHandler(getter HealthGetter) *LinkHealthHandler {
	return &LinkHealthHandler{getter: getter}
}

// Get handles GET /v1/links/{code}/health. Public, like the link metadata
// endpoint — availability status isn't a secret.
func (h *LinkHealthHandler) Get(w http.ResponseWriter, r *http.Request) {
	health, err := h.getter.GetByCode(r.Context(), r.PathValue("code"))
	if err != nil {
		respondDomainError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, dto.FromHealth(health))
}
