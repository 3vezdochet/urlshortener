package config_test

import (
	"testing"
	"time"

	"urlshortener/internal/config"
)

var allEnvKeys = []string{
	"DATABASE_URL", "HTTP_ADDR", "DEFAULT_LINK_TTL_SECONDS",
	"REDIS_ADDR", "CACHE_TTL_SECONDS", "NEGATIVE_CACHE_TTL_SECONDS",
}

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
			name: "custom values including redis",
			env: map[string]string{
				"DATABASE_URL":               "postgres://localhost/db",
				"HTTP_ADDR":                  ":9090",
				"DEFAULT_LINK_TTL_SECONDS":   "3600",
				"REDIS_ADDR":                 "localhost:6379",
				"CACHE_TTL_SECONDS":          "1800",
				"NEGATIVE_CACHE_TTL_SECONDS": "30",
			},
		},
		{
			name:    "missing database url",
			env:     map[string]string{},
			wantErr: true,
		},
		{
			name: "invalid link ttl",
			env: map[string]string{
				"DATABASE_URL":             "postgres://localhost/db",
				"DEFAULT_LINK_TTL_SECONDS": "not-a-number",
			},
			wantErr: true,
		},
		{
			name: "negative link ttl",
			env: map[string]string{
				"DATABASE_URL":             "postgres://localhost/db",
				"DEFAULT_LINK_TTL_SECONDS": "-1",
			},
			wantErr: true,
		},
		{
			name: "invalid cache ttl",
			env: map[string]string{
				"DATABASE_URL":      "postgres://localhost/db",
				"CACHE_TTL_SECONDS": "not-a-number",
			},
			wantErr: true,
		},
		{
			name: "negative negative-cache ttl",
			env: map[string]string{
				"DATABASE_URL":               "postgres://localhost/db",
				"NEGATIVE_CACHE_TTL_SECONDS": "-5",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range allEnvKeys {
				t.Setenv(key, tt.env[key])
			}

			_, err := config.Load()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoad_RedisDisabledByDefault(t *testing.T) {
	for _, key := range allEnvKeys {
		t.Setenv(key, "")
	}
	t.Setenv("DATABASE_URL", "postgres://localhost/db")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.RedisAddr != "" {
		t.Errorf("RedisAddr = %q, want empty (caching should be opt-in)", cfg.RedisAddr)
	}
}

func TestLoad_ParsesSecondsAsDuration(t *testing.T) {
	for _, key := range allEnvKeys {
		t.Setenv(key, "")
	}
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("CACHE_TTL_SECONDS", "120")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.CacheTTL != 2*time.Minute {
		t.Errorf("CacheTTL = %v, want %v", cfg.CacheTTL, 2*time.Minute)
	}
}
