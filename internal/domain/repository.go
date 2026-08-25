package domain

import (
	"context"
	"time"
)

// LinkRepository persists and retrieves links. Implementations must be
// safe for concurrent use.
type LinkRepository interface {
	Create(ctx context.Context, link *Link) error
	GetByCode(ctx context.Context, code string) (*Link, error)
	Deactivate(ctx context.Context, code string) error
}

// CodeGenerator produces unique numeric identifiers used to derive short
// codes. Implementations must be safe for concurrent use.
type CodeGenerator interface {
	Next(ctx context.Context) (uint64, error)
}

// LinkCache is a fast, possibly-lossy read-through cache for the redirect
// hot path. It is a performance optimization, never a source of truth:
// callers should treat any error from it as a cache miss and fall back to
// LinkRepository. Implementations must be safe for concurrent use.
type LinkCache interface {
	// Get returns the cached link for code. ok is false on a cache miss
	// (the code simply isn't cached — distinct from SetMissing's
	// "confirmed absent" below).
	Get(ctx context.Context, code string) (link *Link, ok bool, err error)

	// Set caches link for ttl.
	Set(ctx context.Context, link *Link, ttl time.Duration) error

	// SetMissing records that code does not exist in the repository, so
	// repeated lookups of a nonexistent code don't all reach the database
	// (cache penetration).
	SetMissing(ctx context.Context, code string, ttl time.Duration) error

	// IsMissing reports whether code was recorded by SetMissing and that
	// record hasn't expired yet.
	IsMissing(ctx context.Context, code string) (bool, error)

	// Invalidate removes any cached entry (positive or negative) for
	// code. Called when a link is created or deactivated, so callers
	// never observe stale cached state for longer than it takes this
	// call to complete.
	Invalidate(ctx context.Context, code string) error
}

// HealthRepository stores and retrieves link availability check state.
// Split from LinkRepository because the checker queries it in a
// completely different pattern (claim a due batch, work it, record
// results) than the API queries links (get by code). Implementations must
// be safe for concurrent use — ClaimDue in particular must guarantee two
// concurrent checker replicas never claim the same row.
type HealthRepository interface {
	// EnsureScheduled creates a health record for code if one doesn't
	// already exist yet, due at dueAt. Called when a link is created, so
	// every link eventually gets checked without a separate backfill
	// step. Safe to call more than once for the same code (no-op after
	// the first).
	EnsureScheduled(ctx context.Context, code string, dueAt time.Time) error

	// ClaimDue reserves up to limit records due at or before now by
	// pushing their next-check time forward by leaseFor — a short,
	// self-healing reservation: a worker that crashes mid-check simply
	// lets the lease expire, and the row becomes claimable again with no
	// separate cleanup job. Returns what was claimed.
	ClaimDue(ctx context.Context, now time.Time, limit int, leaseFor time.Duration) ([]DueCheck, error)

	// Record persists the outcome of a single check and schedules
	// nextCheckAt for the following one.
	Record(ctx context.Context, result HealthResult, nextCheckAt time.Time) error

	// GetByCode returns the current health record for code, or
	// ErrLinkNotFound if none exists yet (e.g. checking hasn't run for
	// this link).
	GetByCode(ctx context.Context, code string) (*LinkHealth, error)
}
