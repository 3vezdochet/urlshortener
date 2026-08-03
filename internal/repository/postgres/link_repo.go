package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"urlshortener/internal/domain"
)

// uniqueViolationCode is the PostgreSQL error code for a unique_violation,
// raised here when a code (custom alias or generated) already exists.
// See https://www.postgresql.org/docs/current/errcodes-appendix.html
const uniqueViolationCode = "23505"

// LinkRepo is a PostgreSQL-backed domain.LinkRepository built on pgx v5.
type LinkRepo struct {
	pool *pgxpool.Pool
}

// NewLinkRepo returns a LinkRepo backed by pool. The caller owns the pool's
// lifecycle (including closing it).
func NewLinkRepo(pool *pgxpool.Pool) *LinkRepo {
	return &LinkRepo{pool: pool}
}

func (r *LinkRepo) Create(ctx context.Context, link *domain.Link) error {
	const q = `
		INSERT INTO links (code, original_url, owner_id, created_at, expires_at, active)
		VALUES ($1, $2, $3, $4, $5, $6)`

	_, err := r.pool.Exec(ctx, q,
		link.Code, link.OriginalURL, link.OwnerID, link.CreatedAt, link.ExpiresAt, link.Active,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
			return domain.ErrLinkExists
		}
		return fmt.Errorf("insert link: %w", err)
	}

	return nil
}

func (r *LinkRepo) GetByCode(ctx context.Context, code string) (*domain.Link, error) {
	const q = `
		SELECT code, original_url, owner_id, created_at, expires_at, active
		FROM links
		WHERE code = $1`

	var link domain.Link
	err := r.pool.QueryRow(ctx, q, code).Scan(
		&link.Code, &link.OriginalURL, &link.OwnerID, &link.CreatedAt, &link.ExpiresAt, &link.Active,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrLinkNotFound
		}
		return nil, fmt.Errorf("select link: %w", err)
	}

	return &link, nil
}

func (r *LinkRepo) Deactivate(ctx context.Context, code string) error {
	const q = `UPDATE links SET active = false WHERE code = $1`

	tag, err := r.pool.Exec(ctx, q, code)
	if err != nil {
		return fmt.Errorf("deactivate link: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrLinkNotFound
	}

	return nil
}
