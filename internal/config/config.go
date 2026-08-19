// Package config loads runtime configuration for the API service from
// environment variables. It deliberately avoids a config-file/flag
// library: three settings don't justify the dependency.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds runtime configuration for the API service.
type Config struct {
	HTTPAddr       string
	DatabaseURL    string
	DefaultLinkTTL time.Duration
}

// Load reads configuration from the environment, applying defaults where
// sensible and failing fast on anything required but missing or invalid.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr: getEnv("HTTP_ADDR", ":8080"),
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	cfg.DatabaseURL = dsn

	ttlSeconds, err := strconv.ParseInt(getEnv("DEFAULT_LINK_TTL_SECONDS", "0"), 10, 64)
	if err != nil {
		return Config{}, fmt.Errorf("parse DEFAULT_LINK_TTL_SECONDS: %w", err)
	}
	if ttlSeconds < 0 {
		return Config{}, fmt.Errorf("DEFAULT_LINK_TTL_SECONDS must not be negative")
	}
	cfg.DefaultLinkTTL = time.Duration(ttlSeconds) * time.Second

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
