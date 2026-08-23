// Package ratelimit defines the port the HTTP layer uses to check whether
// a request should be allowed, independent of how that decision is made.
// The production implementation (grpcclient) calls out to an existing
// standalone GCRA rate limiter service over gRPC; tests use a fake.
package ratelimit

import (
	"context"
	"time"
)

// Decision is the result of a rate limit check.
type Decision struct {
	Allowed bool
	// RetryAfter is meaningful only when Allowed is false: how long the
	// caller should wait before trying again.
	RetryAfter time.Duration
}

// Limiter decides whether a request identified by key is allowed right
// now. Implementations must be safe for concurrent use.
type Limiter interface {
	Allow(ctx context.Context, key string) (Decision, error)
}
