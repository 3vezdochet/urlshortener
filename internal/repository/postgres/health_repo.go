package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"urlshortener/internal/domain"
)

// HealthRepo is a PostgreSQL-backed domain.HealthRepository.
type HealthRepo struct {
	pool *pgxpool.Pool
}

// NewHealthRepo returns a HealthRepo backed by pool.
func NewHealthRepo(pool *pgxpool.Pool) *HealthRepo {
	return &HealthRepo{pool: pool}
}

func (r *HealthRepo) EnsureScheduled(ctx context.Context, code string, dueAt time.Time) error {
	const q = `
		INSERT INTO link_health (code, next_check_at)
		VALUES ($1, $2)
		ON CONFLICT (code) DO NOTHING`

	if _, err := r.pool.Exec(ctx, q, code, dueAt); err != nil {
		return fmt.Errorf("insert link_health: %w", err)
	}
	return nil
}

// ClaimDue runs as two statements in one short transaction: SELECT ... FOR
// UPDATE SKIP LOCKED to find and lock due rows (so a concurrent checker
// replica skips straight past them), then UPDATE to push their
// next_check_at forward by leaseFor before committing. The transaction is
// only open for these two fast statements — never for the duration of the
// actual HTTP check, which happens later, unlocked, in the caller.
func (r *HealthRepo) ClaimDue(ctx context.Context, now time.Time, limit int, leaseFor time.Duration) ([]domain.DueCheck, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once committed below

	const selectQ = `
		SELECT h.code, l.original_url, h.consecutive_fails
		FROM link_health h
		JOIN links l ON l.code = h.code
		WHERE h.next_check_at <= $1
		ORDER BY h.next_check_at
		LIMIT $2
		FOR UPDATE OF h SKIP LOCKED`

	rows, err := tx.Query(ctx, selectQ, now, limit)
	if err != nil {
		return nil, fmt.Errorf("select due checks: %w", err)
	}
	defer rows.Close()

	var checks []domain.DueCheck
	var codes []string
	for rows.Next() {
		var c domain.DueCheck
		if err := rows.Scan(&c.Code, &c.OriginalURL, &c.ConsecutiveFails); err != nil {
			return nil, fmt.Errorf("scan due check: %w", err)
		}
		checks = append(checks, c)
		codes = append(codes, c.Code)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due checks: %w", err)
	}

	if len(codes) > 0 {
		const leaseQ = `UPDATE link_health SET next_check_at = $1 WHERE code = ANY($2)`
		if _, err := tx.Exec(ctx, leaseQ, now.Add(leaseFor), codes); err != nil {
			return nil, fmt.Errorf("lease claimed checks: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit claim: %w", err)
	}

	return checks, nil
}

func (r *HealthRepo) Record(ctx context.Context, result domain.HealthResult, nextCheckAt time.Time) error {
	status := domain.HealthStatusUp
	errMsg := ""
	if !result.Up {
		status = domain.HealthStatusDown
		if result.Err != nil {
			errMsg = result.Err.Error()
		}
	}

	const q = `
		UPDATE link_health
		SET status = $2,
		    last_checked_at = $3,
		    next_check_at = $4,
		    consecutive_fails = $5,
		    last_status_code = $6,
		    last_error = $7
		WHERE code = $1`

	_, err := r.pool.Exec(ctx, q,
		result.Code, status, result.CheckedAt, nextCheckAt, result.ConsecutiveFails, result.StatusCode, errMsg,
	)
	if err != nil {
		return fmt.Errorf("update link_health: %w", err)
	}
	return nil
}

func (r *HealthRepo) GetByCode(ctx context.Context, code string) (*domain.LinkHealth, error) {
	const q = `
		SELECT code, status, last_checked_at, next_check_at, consecutive_fails, last_status_code, last_error
		FROM link_health
		WHERE code = $1`

	var h domain.LinkHealth
	err := r.pool.QueryRow(ctx, q, code).Scan(
		&h.Code, &h.Status, &h.LastCheckedAt, &h.NextCheckAt,
		&h.ConsecutiveFails, &h.LastStatusCode, &h.LastError,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrLinkNotFound
		}
		return nil, fmt.Errorf("select link_health: %w", err)
	}

	return &h, nil
}

// Compile-time check that HealthRepo satisfies the domain port.
var _ domain.HealthRepository = (*HealthRepo)(nil)
