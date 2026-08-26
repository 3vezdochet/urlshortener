// Package httpapi assembles the HTTP transport for the API service: route
// registration, the middleware chain, and server construction. It has no
// PostgreSQL/pgx import of its own — see handler.Pinger — and no gRPC
// import either — see ratelimit.Limiter.
package httpapi

import (
	"log/slog"
	"net/http"

	"urlshortener/internal/auth"
	"urlshortener/internal/delivery/httpapi/handler"
	"urlshortener/internal/delivery/httpapi/middleware"
	"urlshortener/internal/ratelimit"
	"urlshortener/internal/usecase/shortener"
)

// NewRouter wires up all routes and the middleware chain.
//
// keyStore is required: POST /v1/links and DELETE /v1/links/{code} are
// always behind API key auth, there's no "disabled" mode for that — a
// KeyStore with zero keys (auth.StaticKeyStore{}) rejects every request,
// which is a valid and safe way to run without issuing keys yet.
//
// limiter, healthGetter, and pinger are all optional (nil-safe): without
// limiter, no route is rate-limited; without healthGetter, GET
// /v1/links/{code}/health is omitted; without pinger, GET /readyz is
// omitted. All three are omitted entirely rather than registered-but-broken.
func NewRouter(
	svc *shortener.Service,
	keyStore auth.KeyStore,
	limiter ratelimit.Limiter,
	healthGetter handler.HealthGetter,
	pinger handler.Pinger,
	logger *slog.Logger,
) http.Handler {
	mux := http.NewServeMux()

	linkHandler := handler.NewLinkHandler(svc)
	redirectHandler := handler.NewRedirectHandler(svc)

	// Authenticated, mutating routes: Auth first (so PrincipalKey below has
	// something to read), then rate-limited per owner.
	mux.Handle("POST /v1/links", withAuth(linkHandler.Create, keyStore, limiter, middleware.PrincipalKey))
	mux.Handle("DELETE /v1/links/{code}", withAuth(linkHandler.Deactivate, keyStore, limiter, middleware.PrincipalKey))

	// Public routes. The redirect is the highest-traffic, most abuse-prone
	// path, so it's rate-limited by client IP even though it's anonymous.
	mux.HandleFunc("GET /v1/links/{code}", linkHandler.Get)
	mux.Handle("GET /r/{code}", withRateLimit(redirectHandler.Redirect, limiter, middleware.ClientIPKey))

	if healthGetter != nil {
		mux.HandleFunc("GET /v1/links/{code}/health", handler.NewLinkHealthHandler(healthGetter).Get)
	}

	mux.HandleFunc("GET /healthz", handler.Healthz)
	if pinger != nil {
		mux.HandleFunc("GET /readyz", handler.Readyz(pinger))
	}

	// Order matters: RequestID must be outermost so Logging can read the
	// id, and Logging must wrap Recover (not the other way round) so it
	// still records the final status after a recovered panic.
	var h http.Handler = mux
	h = middleware.Recover(h)
	h = middleware.Logging(logger)(h)
	h = middleware.RequestID(h)

	return h
}

// withAuth wraps next with API key auth and, if limiter is non-nil, rate
// limiting keyed by keyFn — applied in that order so keyFn can read the
// Principal Auth just attached to the request context.
func withAuth(next http.HandlerFunc, keyStore auth.KeyStore, limiter ratelimit.Limiter, keyFn middleware.KeyFunc) http.Handler {
	var h http.Handler = next
	if limiter != nil {
		h = middleware.RateLimit(limiter, keyFn)(h)
	}
	h = middleware.Auth(keyStore)(h)
	return h
}

// withRateLimit wraps next with rate limiting if limiter is non-nil,
// otherwise returns next unwrapped.
func withRateLimit(next http.HandlerFunc, limiter ratelimit.Limiter, keyFn middleware.KeyFunc) http.Handler {
	if limiter == nil {
		return next
	}
	return middleware.RateLimit(limiter, keyFn)(next)
}
