// Package postgres provides PostgreSQL-backed implementations of the
// domain ports, built on pgx v5. Migrations are embedded in the binary and
// applied via Migrate.
package postgres

import "urlshortener/internal/domain"

// Compile-time checks that the adapters satisfy the domain ports.
var (
	_ domain.LinkRepository = (*LinkRepo)(nil)
	_ domain.CodeGenerator  = (*CodeGen)(nil)
)
