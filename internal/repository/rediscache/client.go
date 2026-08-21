// Package rediscache provides a Redis-backed domain.LinkCache: the
// cache-aside layer in front of PostgreSQL for the redirect hot path.
//
// Named rediscache, not redis, so importers never have to alias between
// this package and github.com/redis/go-redis/v9 — the same reasoning as
// internal/delivery/httpapi versus net/http.
package rediscache

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// NewClient creates a Redis client for addr and verifies connectivity
// with a ping before returning. Callers own the client's lifecycle and
// must call Close when done.
func NewClient(ctx context.Context, addr string) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{Addr: addr})

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return client, nil
}
