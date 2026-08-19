package dto_test

import (
	"testing"
	"time"

	"urlshortener/internal/delivery/httpapi/dto"
	"urlshortener/internal/domain"
)

func TestFromDomain(t *testing.T) {
	now := time.Now().UTC()
	expires := now.Add(time.Hour)

	tests := []struct {
		name string
		link *domain.Link
	}{
		{
			name: "link without expiry",
			link: &domain.Link{
				Code: "abc", OriginalURL: "https://example.com",
				CreatedAt: now, Active: true,
			},
		},
		{
			name: "link with expiry",
			link: &domain.Link{
				Code: "xyz", OriginalURL: "https://example.com/other",
				CreatedAt: now, ExpiresAt: &expires, Active: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dto.FromDomain(tt.link)

			if got.Code != tt.link.Code {
				t.Errorf("Code = %q, want %q", got.Code, tt.link.Code)
			}
			if got.OriginalURL != tt.link.OriginalURL {
				t.Errorf("OriginalURL = %q, want %q", got.OriginalURL, tt.link.OriginalURL)
			}
			if got.Active != tt.link.Active {
				t.Errorf("Active = %v, want %v", got.Active, tt.link.Active)
			}
			if (got.ExpiresAt == nil) != (tt.link.ExpiresAt == nil) {
				t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, tt.link.ExpiresAt)
			}
		})
	}
}
