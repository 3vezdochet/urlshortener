package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"

	"urlshortener/internal/auth"
	"urlshortener/internal/ratelimit"
)

// KeyFunc extracts the rate-limit key from a request.
type KeyFunc func(r *http.Request) string

// RateLimit rejects a request with 429 once limiter denies the key
// extracted by keyFn. A Limiter error is logged and treated as "allow" —
// like the cache, the limiter is a protective layer, not a point of
// failure: an outage there must not take down the whole API.
func RateLimit(limiter ratelimit.Limiter, keyFn KeyFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			decision, err := limiter.Allow(r.Context(), keyFn(r))
			if err != nil {
				slog.Warn("rate limiter unavailable, allowing request",
					"error", err, "request_id", RequestIDFromContext(r.Context()))
				next.ServeHTTP(w, r)
				return
			}

			if !decision.Allowed {
				if decision.RetryAfter > 0 {
					w.Header().Set("Retry-After", strconv.Itoa(int(decision.RetryAfter.Seconds())))
				}
				writeAuthError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ClientIPKey extracts a rate-limit key from the client's address,
// preferring the first hop of X-Forwarded-For when present (behind a
// trusted load balancer) and falling back to RemoteAddr. Suitable for
// unauthenticated routes like the public redirect.
func ClientIPKey(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if i := strings.IndexByte(fwd, ','); i >= 0 {
			return strings.TrimSpace(fwd[:i])
		}
		return strings.TrimSpace(fwd)
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// PrincipalKey extracts the rate-limit key from the authenticated
// Principal set by Auth, so each API key gets its own limit regardless of
// which IP it's called from. Must be installed after Auth in the chain;
// falls back to ClientIPKey if somehow no Principal is present.
func PrincipalKey(r *http.Request) string {
	p, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		return ClientIPKey(r)
	}
	return p.OwnerID
}
