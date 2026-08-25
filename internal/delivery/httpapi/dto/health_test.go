package dto_test

import (
	"testing"
	"time"

	"urlshortener/internal/delivery/httpapi/dto"
	"urlshortener/internal/domain"
)

func TestFromHealth(t *testing.T) {
	checkedAt := time.Now().UTC()
	nextCheckAt := checkedAt.Add(10 * time.Minute)

	tests := []struct {
		name   string
		health *domain.LinkHealth
	}{
		{
			name: "never checked",
			health: &domain.LinkHealth{
				Code: "abc", Status: domain.HealthStatusUnknown, NextCheckAt: nextCheckAt,
			},
		},
		{
			name: "up",
			health: &domain.LinkHealth{
				Code: "abc", Status: domain.HealthStatusUp,
				LastCheckedAt: &checkedAt, NextCheckAt: nextCheckAt, LastStatusCode: 200,
			},
		},
		{
			name: "down",
			health: &domain.LinkHealth{
				Code: "abc", Status: domain.HealthStatusDown,
				LastCheckedAt: &checkedAt, NextCheckAt: nextCheckAt,
				ConsecutiveFails: 3, LastError: "connection refused",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dto.FromHealth(tt.health)

			if got.Code != tt.health.Code {
				t.Errorf("Code = %q, want %q", got.Code, tt.health.Code)
			}
			if got.Status != string(tt.health.Status) {
				t.Errorf("Status = %q, want %q", got.Status, tt.health.Status)
			}
			if got.ConsecutiveFails != tt.health.ConsecutiveFails {
				t.Errorf("ConsecutiveFails = %d, want %d", got.ConsecutiveFails, tt.health.ConsecutiveFails)
			}
			if got.LastError != tt.health.LastError {
				t.Errorf("LastError = %q, want %q", got.LastError, tt.health.LastError)
			}
			if (got.LastCheckedAt == nil) != (tt.health.LastCheckedAt == nil) {
				t.Errorf("LastCheckedAt = %v, want %v", got.LastCheckedAt, tt.health.LastCheckedAt)
			}
		})
	}
}
