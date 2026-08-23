package middleware

import (
	"log/slog"
	"net/http"

	"urlshortener/internal/auth"
)

// Auth checks the X-API-Key header against store and rejects the request
// with 401 if it's missing or unknown. On success, the resolved
// auth.Principal is attached to the request context (auth.WithPrincipal)
// for handlers to read via auth.PrincipalFromContext.
func Auth(store auth.KeyStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("X-API-Key")
			if key == "" {
				writeAuthError(w, http.StatusUnauthorized, "missing X-API-Key header")
				return
			}

			principal, ok, err := store.Lookup(r.Context(), key)
			if err != nil {
				slog.Error("api key lookup failed", "error", err, "request_id", RequestIDFromContext(r.Context()))
				writeAuthError(w, http.StatusInternalServerError, "internal server error")
				return
			}
			if !ok {
				writeAuthError(w, http.StatusUnauthorized, "invalid API key")
				return
			}

			ctx := auth.WithPrincipal(r.Context(), principal)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// writeAuthError writes a minimal JSON error body without depending on
// the dto package — keeps middleware's only real dependency on internal
// packages limited to auth/ratelimit, not the transport DTOs.
func writeAuthError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + message + `"}`))
}
