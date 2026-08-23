// Package grpcclient implements ratelimit.Limiter by calling an existing,
// standalone GCRA rate limiter service over gRPC — the one built for the
// rate-limiter portfolio project, reused here instead of rewritten.
//
// This package depends on generated code (ratelimitv1) that doesn't exist
// until you run `make proto` against api/ratelimit/v1/ratelimit.proto (see
// the repo root README's "Rate limiting" section) — it will not compile
// until then. Nothing outside this package depends on it: middleware and
// cmd/api only see the ratelimit.Limiter interface.
package grpcclient

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	ratelimitv1 "urlshortener/internal/ratelimit/grpcclient/ratelimitv1"

	"urlshortener/internal/ratelimit"
)

// Client is a ratelimit.Limiter backed by a gRPC call to an external GCRA
// rate limiter service.
type Client struct {
	stub ratelimitv1.RateLimiterClient
	conn *grpc.ClientConn
}

// Dial connects to the rate limiter service at addr (host:port, no
// scheme). The caller owns the returned Client's lifecycle and must call
// Close when done. The connection is unencrypted (insecure transport
// credentials) — this is meant for a trusted internal network the same
// way the Postgres/Redis connections in this project are; put it behind
// mTLS or a service mesh before crossing a trust boundary.
func Dial(addr string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial rate limiter: %w", err)
	}
	return &Client{stub: ratelimitv1.NewRateLimiterClient(conn), conn: conn}, nil
}

// Close closes the underlying gRPC connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// Allow implements ratelimit.Limiter.
func (c *Client) Allow(ctx context.Context, key string) (ratelimit.Decision, error) {
	resp, err := c.stub.Allow(ctx, &ratelimitv1.AllowRequest{Key: key})
	if err != nil {
		return ratelimit.Decision{}, fmt.Errorf("rate limiter Allow: %w", err)
	}

	return ratelimit.Decision{
		Allowed:    resp.GetAllowed(),
		RetryAfter: time.Duration(resp.GetRetryAfterMs()) * time.Millisecond,
	}, nil
}

// Compile-time check that Client satisfies the port.
var _ ratelimit.Limiter = (*Client)(nil)
