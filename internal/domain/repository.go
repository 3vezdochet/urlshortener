package domain

import "context"

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
