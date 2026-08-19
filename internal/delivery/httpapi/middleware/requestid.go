// Package middleware provides the standard HTTP middleware chain: request
// ID propagation, panic recovery, and structured request logging.
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

type contextKey int

const requestIDKey contextKey = iota

// RequestID assigns a short random identifier to each request — or reuses
// an inbound X-Request-Id header, e.g. from a trusted upstream proxy —
// stores it in the request context, and echoes it back in the response
// header so clients and logs can correlate.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = newRequestID()
		}

		w.Header().Set("X-Request-Id", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFromContext returns the request ID stored by RequestID, or ""
// if none is present.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}
