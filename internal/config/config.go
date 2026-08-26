// Package config loads runtime configuration for the API service from
// environment variables. It deliberately avoids a config-file/flag
// library: the settings here don't justify the dependency.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"urlshortener/internal/auth"
)

// Config holds runtime configuration for the API service.
type Config struct {
	HTTPAddr       string
	DatabaseURL    string
	DefaultLinkTTL time.Duration

	// RedisAddr enables the cache-aside read layer when non-empty. Caching
	// is an optimization, not a hard dependency: leave it unset and the
	// service runs exactly as it did before caching existed, straight
	// against PostgreSQL.
	RedisAddr string
	// CacheTTL and NegativeCacheTTL are zero (meaning "use the shortener
	// package's own default") unless the corresponding env var is set.
	CacheTTL         time.Duration
	NegativeCacheTTL time.Duration

	// APIKeys authenticates POST/DELETE /v1/links — always enforced, not
	// optional. An empty store (API_KEYS unset) is valid and safe: it
	// just rejects every request until keys are issued, rather than
	// silently allowing unauthenticated writes.
	APIKeys auth.StaticKeyStore
	// RateLimiterAddr enables gRPC rate limiting when non-empty — like
	// RedisAddr, an optional protective layer, not a hard dependency.
	RateLimiterAddr string

	// CheckerInterval is how often cmd/checker polls for due availability
	// checks. Unused by cmd/api.
	CheckerInterval time.Duration
}

// Load reads configuration from the environment, applying defaults where
// sensible and failing fast on anything required but missing or invalid.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:        getEnv("HTTP_ADDR", ":8080"),
		RedisAddr:       os.Getenv("REDIS_ADDR"),
		RateLimiterAddr: os.Getenv("RATE_LIMITER_ADDR"),
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	cfg.DatabaseURL = dsn

	apiKeys, err := auth.ParseStaticKeys(os.Getenv("API_KEYS"))
	if err != nil {
		return Config{}, fmt.Errorf("parse API_KEYS: %w", err)
	}
	cfg.APIKeys = apiKeys

	defaultTTL, err := parseSecondsEnv("DEFAULT_LINK_TTL_SECONDS", "0")
	if err != nil {
		return Config{}, err
	}
	cfg.DefaultLinkTTL = defaultTTL

	cacheTTL, err := parseSecondsEnv("CACHE_TTL_SECONDS", "0")
	if err != nil {
		return Config{}, err
	}
	cfg.CacheTTL = cacheTTL

	negativeCacheTTL, err := parseSecondsEnv("NEGATIVE_CACHE_TTL_SECONDS", "0")
	if err != nil {
		return Config{}, err
	}
	cfg.NegativeCacheTTL = negativeCacheTTL

	checkerInterval, err := parseSecondsEnv("CHECKER_INTERVAL_SECONDS", "30")
	if err != nil {
		return Config{}, err
	}
	cfg.CheckerInterval = checkerInterval

	return cfg, nil
}

// parseSecondsEnv reads key as a non-negative integer number of seconds,
// falling back to fallback (also seconds) when key is unset.
func parseSecondsEnv(key, fallback string) (time.Duration, error) {
	seconds, err := strconv.ParseInt(getEnv(key, fallback), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	if seconds < 0 {
		return 0, fmt.Errorf("%s must not be negative", key)
	}
	return time.Duration(seconds) * time.Second, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
