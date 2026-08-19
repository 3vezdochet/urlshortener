package config_test

import (
	"testing"

	"urlshortener/internal/config"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{
			name: "minimal valid config",
			env:  map[string]string{"DATABASE_URL": "postgres://localhost/db"},
		},
		{
			name: "custom values",
			env: map[string]string{
				"DATABASE_URL":             "postgres://localhost/db",
				"HTTP_ADDR":                ":9090",
				"DEFAULT_LINK_TTL_SECONDS": "3600",
			},
		},
		{
			name:    "missing database url",
			env:     map[string]string{},
			wantErr: true,
		},
		{
			name: "invalid ttl",
			env: map[string]string{
				"DATABASE_URL":             "postgres://localhost/db",
				"DEFAULT_LINK_TTL_SECONDS": "not-a-number",
			},
			wantErr: true,
		},
		{
			name: "negative ttl",
			env: map[string]string{
				"DATABASE_URL":             "postgres://localhost/db",
				"DEFAULT_LINK_TTL_SECONDS": "-1",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range []string{"DATABASE_URL", "HTTP_ADDR", "DEFAULT_LINK_TTL_SECONDS"} {
				t.Setenv(key, tt.env[key])
			}

			_, err := config.Load()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
