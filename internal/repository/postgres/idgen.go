package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CodeGen is a domain.CodeGenerator backed by a PostgreSQL sequence
// (link_codes, created in migration 0002). A DB sequence guarantees unique,
// monotonically increasing values even with many API replicas writing
// concurrently, without any application-level locking.
type CodeGen struct {
	pool         *pgxpool.Pool
	sequenceName string
}

// NewCodeGen returns a CodeGen backed by pool, drawing values from
// sequenceName (e.g. "link_codes").
func NewCodeGen(pool *pgxpool.Pool, sequenceName string) *CodeGen {
	return &CodeGen{pool: pool, sequenceName: sequenceName}
}

func (g *CodeGen) Next(ctx context.Context) (uint64, error) {
	const q = `SELECT nextval($1::regclass)`

	var raw int64
	if err := g.pool.QueryRow(ctx, q, g.sequenceName).Scan(&raw); err != nil {
		return 0, fmt.Errorf("next sequence value: %w", err)
	}

	return uint64(raw), nil
}
