// Package httpprobe implements checker.Prober with real HTTP requests.
package httpprobe

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

const defaultTimeout = 5 * time.Second

// Prober checks target availability with a GET request under a bounded
// timeout. GET, not HEAD: plenty of real servers mishandle or block HEAD
// (some WAFs and CDNs reject it outright), so GET is the more reliable
// default for "is this actually reachable" even though it costs a body
// download this project doesn't otherwise need.
type Prober struct {
	client  *http.Client
	timeout time.Duration
}

// Option configures a Prober.
type Option func(*Prober)

// WithTimeout overrides the per-request timeout (default 5s).
func WithTimeout(d time.Duration) Option {
	return func(p *Prober) { p.timeout = d }
}

// WithHTTPClient overrides the underlying *http.Client (default
// http.DefaultClient) — e.g. to customize redirect handling or transport
// settings.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Prober) { p.client = c }
}

// New returns a Prober ready to use.
func New(opts ...Option) *Prober {
	p := &Prober{client: http.DefaultClient, timeout: defaultTimeout}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Probe reports the target as up (nil error) for any response with status
// < 500 — a 404 means the server is alive and answering, even if that
// particular page is gone, so it still counts as available. 5xx responses
// and transport-level failures (timeout, DNS, connection refused) are
// reported as down via a non-nil error.
func (p *Prober) Probe(ctx context.Context, url string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		return resp.StatusCode, fmt.Errorf("server error: %d", resp.StatusCode)
	}

	return resp.StatusCode, nil
}
