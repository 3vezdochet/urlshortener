package handler

import (
	"net/http"

	"urlshortener/internal/usecase/shortener"
)

// RedirectHandler serves the public redirect endpoint.
type RedirectHandler struct {
	svc *shortener.Service
}

// NewRedirectHandler returns a RedirectHandler backed by svc.
func NewRedirectHandler(svc *shortener.Service) *RedirectHandler {
	return &RedirectHandler{svc: svc}
}

// Redirect handles GET /r/{code}: a 302 to the original URL, or a mapped
// domain error status (404/410) if the code is unknown, inactive, or
// expired. Deliberately uses Service.Resolve, not Get — the public
// redirect must not reveal or follow deactivated/expired links.
func (h *RedirectHandler) Redirect(w http.ResponseWriter, r *http.Request) {
	link, err := h.svc.Resolve(r.Context(), r.PathValue("code"))
	if err != nil {
		respondDomainError(w, err)
		return
	}

	http.Redirect(w, r, link.OriginalURL, http.StatusFound)
}
