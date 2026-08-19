// Package dto defines the JSON request/response shapes for the HTTP API,
// kept separate from internal/domain so the wire format can evolve (or
// version) independently of the domain model.
package dto

import (
	"time"

	"urlshortener/internal/domain"
)

// CreateLinkRequest is the JSON body for POST /v1/links.
type CreateLinkRequest struct {
	URL         string `json:"url"`
	CustomAlias string `json:"custom_alias,omitempty"`
	TTLSeconds  int64  `json:"ttl_seconds,omitempty"`
}

// LinkResponse is the JSON representation of a link returned by the API.
// It intentionally omits OwnerID — that's an internal detail, not
// something every caller of GET /v1/links/{code} should see.
type LinkResponse struct {
	Code        string     `json:"code"`
	OriginalURL string     `json:"original_url"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Active      bool       `json:"active"`
}

// FromDomain converts a domain.Link into its API representation.
func FromDomain(link *domain.Link) LinkResponse {
	return LinkResponse{
		Code:        link.Code,
		OriginalURL: link.OriginalURL,
		CreatedAt:   link.CreatedAt,
		ExpiresAt:   link.ExpiresAt,
		Active:      link.Active,
	}
}

// ErrorResponse is the JSON body returned for any non-2xx response.
type ErrorResponse struct {
	Error string `json:"error"`
}
