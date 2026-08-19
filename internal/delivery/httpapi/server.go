package httpapi

import (
	"net/http"
	"time"
)

// NewServer builds an *http.Server around handler with production-sane
// timeouts. It does not start listening — call ListenAndServe (or
// Shutdown) on the result.
func NewServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}
