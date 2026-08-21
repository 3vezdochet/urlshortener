package rediscache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"urlshortener/internal/domain"
)

const (
	linkKeyPrefix    = "link:"
	missingKeyPrefix = "link:missing:"
)

// missingValue is stored under a missing-key; its content is irrelevant —
// only the key's presence (and Redis's own TTL expiring it) matters.
const missingValue = "1"

// Cache is a Redis-backed domain.LinkCache. Positive and negative entries
// live under separate key prefixes so Invalidate can remove both in one
// round trip regardless of which (if either) is currently set.
type Cache struct {
	client *redis.Client
}

// NewCache returns a Cache backed by client. The caller owns the client's
// lifecycle (including closing it) — see NewClient.
func NewCache(client *redis.Client) *Cache {
	return &Cache{client: client}
}

func (c *Cache) Get(ctx context.Context, code string) (*domain.Link, bool, error) {
	raw, err := c.client.Get(ctx, linkKey(code)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("redis get: %w", err)
	}

	var link domain.Link
	if err := json.Unmarshal(raw, &link); err != nil {
		return nil, false, fmt.Errorf("unmarshal cached link: %w", err)
	}

	return &link, true, nil
}

func (c *Cache) Set(ctx context.Context, link *domain.Link, ttl time.Duration) error {
	raw, err := json.Marshal(link)
	if err != nil {
		return fmt.Errorf("marshal link: %w", err)
	}

	if err := c.client.Set(ctx, linkKey(link.Code), raw, ttl).Err(); err != nil {
		return fmt.Errorf("redis set: %w", err)
	}
	return nil
}

func (c *Cache) SetMissing(ctx context.Context, code string, ttl time.Duration) error {
	if err := c.client.Set(ctx, missingKey(code), missingValue, ttl).Err(); err != nil {
		return fmt.Errorf("redis set missing: %w", err)
	}
	return nil
}

func (c *Cache) IsMissing(ctx context.Context, code string) (bool, error) {
	err := c.client.Get(ctx, missingKey(code)).Err()
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, redis.Nil):
		return false, nil
	default:
		return false, fmt.Errorf("redis get missing: %w", err)
	}
}

func (c *Cache) Invalidate(ctx context.Context, code string) error {
	if err := c.client.Del(ctx, linkKey(code), missingKey(code)).Err(); err != nil {
		return fmt.Errorf("redis del: %w", err)
	}
	return nil
}

func linkKey(code string) string    { return linkKeyPrefix + code }
func missingKey(code string) string { return missingKeyPrefix + code }
