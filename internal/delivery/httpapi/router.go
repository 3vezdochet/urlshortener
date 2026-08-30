// Package httpapi assembles the HTTP transport for the API service: route
// registration, the middleware chain, and server construction. It has no
// PostgreSQL/pgx import of its own — see handler.Pinger — no gRPC import
// either — see ratelimit.Limiter — and no Prometheus import either — see
// middleware.HTTPMetricsRecorder and Deps.MetricsHandler below.
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

// Deps bundles NewRouter's dependencies. Service, KeyStore, and Logger are
// required. Everything else is optional (nil-safe) and simply omits the
// feature it backs rather than registering something broken — see each
// field's own doc.
type Deps struct {
	Service *shortener.Service
	Logger  *slog.Logger

	// KeyStore is required: POST /v1/links and DELETE /v1/links/{code}
	// are always behind API key auth, there's no "disabled" mode for
	// that — a KeyStore with zero keys (auth.StaticKeyStore{}) rejects
	// every request, which is a valid and safe way to run without
	// issuing keys yet, not the same as skipping the check.
	KeyStore auth.KeyStore

	// Limiter rate-limits POST /v1/links (per owner) and GET /r/{code}
	// (per IP) when set; nil means no route is rate-limited.
	Limiter ratelimit.Limiter
	// HealthGetter backs GET /v1/links/{code}/health; nil omits the route.
	HealthGetter handler.HealthGetter
	// Pinger backs GET /readyz; nil omits the route.
	Pinger handler.Pinger
	// HTTPMetrics records a Prometheus observation per request on every
	// route when set; nil records nothing.
	HTTPMetrics middleware.HTTPMetricsRecorder
	// MetricsHandler, if set, is served at GET /metrics — typically
	// promhttp.HandlerFor(reg, ...). Built by the caller (cmd/api) so this
	// package never has to import Prometheus itself; nil omits the route.
	MetricsHandler http.Handler
}

// NewRouter wires up all routes and the middleware chain per deps.
func NewRouter(deps Deps) http.Handler {
	mux := http.NewServeMux()

	linkHandler := handler.NewLinkHandler(deps.Service)
	redirectHandler := handler.NewRedirectHandler(deps.Service)

	// Authenticated, mutating routes: Auth first (so PrincipalKey below has
	// something to read), then rate-limited per owner, then metered.
	mux.Handle("POST /v1/links", deps.wrapMetrics("POST /v1/links",
		deps.withAuth(linkHandler.Create, middleware.PrincipalKey)))
	mux.Handle("DELETE /v1/links/{code}", deps.wrapMetrics("DELETE /v1/links/{code}",
		deps.withAuth(linkHandler.Deactivate, middleware.PrincipalKey)))

	// Public routes. The redirect is the highest-traffic, most abuse-prone
	// path, so it's rate-limited by client IP even though it's anonymous.
	mux.Handle("GET /v1/links/{code}", deps.wrapMetrics("GET /v1/links/{code}",
		http.HandlerFunc(linkHandler.Get)))
	mux.Handle("GET /r/{code}", deps.wrapMetrics("GET /r/{code}",
		deps.withRateLimit(redirectHandler.Redirect, middleware.ClientIPKey)))

	if deps.HealthGetter != nil {
		mux.Handle("GET /v1/links/{code}/health", deps.wrapMetrics("GET /v1/links/{code}/health",
			http.HandlerFunc(handler.NewLinkHealthHandler(deps.HealthGetter).Get)))
	}

	// Liveness/readiness/metrics are scrape/probe targets, not user
	// traffic — deliberately left out of the request metrics themselves
	// to avoid every Kubernetes probe polluting the dashboards.
	mux.HandleFunc("GET /healthz", handler.Healthz)
	if deps.Pinger != nil {
		mux.HandleFunc("GET /readyz", handler.Readyz(deps.Pinger))
	}
	if deps.MetricsHandler != nil {
		mux.Handle("GET /metrics", deps.MetricsHandler)
	}

	// Order matters: RequestID must be outermost so Logging can read the
	// id, and Logging must wrap Recover (not the other way round) so it
	// still records the final status after a recovered panic.
	var h http.Handler = mux
	h = middleware.Recover(h)
	h = middleware.Logging(deps.Logger)(h)
	h = middleware.RequestID(h)

	return h
}

// wrapMetrics is a thin receiver-bound wrapper around middleware.WrapMetrics
// so route registration above doesn't have to keep repeating deps.HTTPMetrics.
func (deps Deps) wrapMetrics(route string, next http.Handler) http.Handler {
	return middleware.WrapMetrics(deps.HTTPMetrics, route, next)
}

// withAuth wraps next with API key auth and, if a limiter is configured,
// rate limiting keyed by keyFn — applied in that order so keyFn can read
// the Principal Auth just attached to the request context.
func (deps Deps) withAuth(next http.HandlerFunc, keyFn middleware.KeyFunc) http.Handler {
	var h http.Handler = next
	if deps.Limiter != nil {
		h = middleware.RateLimit(deps.Limiter, keyFn)(h)
	}
	h = middleware.Auth(deps.KeyStore)(h)
	return h
}

// withRateLimit wraps next with rate limiting if a limiter is configured,
// otherwise returns next unwrapped.
func (deps Deps) withRateLimit(next http.HandlerFunc, keyFn middleware.KeyFunc) http.Handler {
	if deps.Limiter == nil {
		return next
	}
	return middleware.RateLimit(deps.Limiter, keyFn)(next)
}
