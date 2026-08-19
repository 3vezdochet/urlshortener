// Package postgres provides Postgres-backed implementations of the domain
// ports, built on database/sql and the lib/pq driver.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"

	"urlshortener/internal/domain"
)

// Compile-time checks that the adapters satisfy the domain ports.
var (
	_ domain.LinkRepository = (*LinkRepo)(nil)
	_ domain.CodeGenerator  = (*CodeGen)(nil)
)

// Open opens a connection pool to Postgres and verifies connectivity with a
// ping. The caller is responsible for closing the returned *sql.DB.
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(20)
	db.SetConnMaxLifetime(30 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return db, nil
}
