package dto

import (
	"time"

	"urlshortener/internal/domain"
)

// HealthResponse is the JSON representation of a link's availability
// check state, returned by GET /v1/links/{code}/health.
type HealthResponse struct {
	Code             string     `json:"code"`
	Status           string     `json:"status"`
	LastCheckedAt    *time.Time `json:"last_checked_at,omitempty"`
	NextCheckAt      time.Time  `json:"next_check_at"`
	ConsecutiveFails int        `json:"consecutive_fails"`
	LastStatusCode   int        `json:"last_status_code,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
}

// FromHealth converts a domain.LinkHealth into its API representation.
func FromHealth(h *domain.LinkHealth) HealthResponse {
	return HealthResponse{
		Code:             h.Code,
		Status:           string(h.Status),
		LastCheckedAt:    h.LastCheckedAt,
		NextCheckAt:      h.NextCheckAt,
		ConsecutiveFails: h.ConsecutiveFails,
		LastStatusCode:   h.LastStatusCode,
		LastError:        h.LastError,
	}
}
