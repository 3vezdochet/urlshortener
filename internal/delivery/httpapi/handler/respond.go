// Package handler implements the HTTP handlers for the link shortener API.
package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"urlshortener/internal/delivery/httpapi/dto"
	"urlshortener/internal/domain"
)

func respondJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("encode response", "error", err)
	}
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, dto.ErrorResponse{Error: message})
}

// respondDomainError maps a domain/usecase error to the appropriate HTTP
// status and a safe, generic message. Internal error details are logged
// server-side and never sent to the client.
func respondDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidURL):
		respondError(w, http.StatusBadRequest, "invalid url")
	case errors.Is(err, domain.ErrLinkExists):
		respondError(w, http.StatusConflict, "link already exists")
	case errors.Is(err, domain.ErrLinkNotFound):
		respondError(w, http.StatusNotFound, "link not found")
	case errors.Is(err, domain.ErrLinkExpired):
		respondError(w, http.StatusGone, "link expired")
	default:
		slog.Error("unhandled error", "error", err)
		respondError(w, http.StatusInternalServerError, "internal server error")
	}
}
