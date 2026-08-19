// Package httpapi assembles the HTTP transport for the API service: route
// registration, the middleware chain, and server construction. It has no
// PostgreSQL/pgx import of its own — see handler.Pinger.
package httpapi

import (
	"log/slog"
	"net/http"

	"urlshortener/internal/delivery/httpapi/handler"
	"urlshortener/internal/delivery/httpapi/middleware"
	"urlshortener/internal/usecase/shortener"
)

// NewRouter wires up all routes and the middleware chain. pinger backs
// GET /readyz; pass nil to omit that route (e.g. in tests with no real
// dependency to check).
func NewRouter(svc *shortener.Service, pinger handler.Pinger, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	linkHandler := handler.NewLinkHandler(svc)
	redirectHandler := handler.NewRedirectHandler(svc)

	mux.HandleFunc("POST /v1/links", linkHandler.Create)
	mux.HandleFunc("GET /v1/links/{code}", linkHandler.Get)
	mux.HandleFunc("DELETE /v1/links/{code}", linkHandler.Deactivate)
	mux.HandleFunc("GET /r/{code}", redirectHandler.Redirect)

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
