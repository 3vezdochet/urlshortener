// Package auth provides API key authentication for the HTTP API: who a
// request is acting as (Principal), how a key resolves to one (KeyStore),
// and how that identity travels through a request's context.
package auth

import "context"

// Principal identifies who an authenticated request is acting as.
type Principal struct {
	// OwnerID becomes domain.Link.OwnerID for links this caller creates,
	// and is checked against it to authorize Deactivate.
	OwnerID string
	// Name is a human-readable label used only in logs/audit, never in
	// authorization decisions.
	Name string
}

// KeyStore resolves an API key to the Principal it authenticates.
// Implementations must be safe for concurrent use.
type KeyStore interface {
	Lookup(ctx context.Context, key string) (Principal, bool, error)
}

type contextKey int

const principalKey contextKey = iota

// WithPrincipal returns a copy of ctx carrying principal.
func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalKey, principal)
}

// PrincipalFromContext returns the Principal attached by WithPrincipal, or
// the zero value and false if none is present (e.g. an unauthenticated
// route, or middleware.Auth was skipped).
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}
