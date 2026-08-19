package handler

import (
	"context"
	"net/http"
	"time"
)

// Pinger is satisfied by *pgxpool.Pool and anything else that can check
// its own connectivity. Kept minimal so this package never has to import
// a specific database driver.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Healthz reports liveness: the process is up and serving requests. It
// never checks external dependencies — that's what Readyz is for.
func Healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// Readyz reports readiness: liveness plus reachability of dependencies
// (currently just the database, via pinger).
func Readyz(pinger Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		if err := pinger.Ping(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		w.WriteHeader(http.StatusOK)
	}
}
