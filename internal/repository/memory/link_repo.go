// Package memory provides in-memory implementations of the domain ports.
// They are used for local development and unit tests before the
// Postgres/Redis-backed adapters land.
package memory

import (
	"context"
	"sync"

	"urlshortener/internal/domain"
)

// LinkRepo is a concurrency-safe, in-memory domain.LinkRepository.
type LinkRepo struct {
	mu    sync.RWMutex
	links map[string]domain.Link
}

// NewLinkRepo returns an empty LinkRepo.
func NewLinkRepo() *LinkRepo {
	return &LinkRepo{links: make(map[string]domain.Link)}
}

func (r *LinkRepo) Create(_ context.Context, link *domain.Link) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.links[link.Code]; exists {
		return domain.ErrLinkExists
	}

	r.links[link.Code] = *link
	return nil
}

func (r *LinkRepo) GetByCode(_ context.Context, code string) (*domain.Link, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	link, ok := r.links[code]
	if !ok {
		return nil, domain.ErrLinkNotFound
	}

	return &link, nil
}

func (r *LinkRepo) Deactivate(_ context.Context, code string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	link, ok := r.links[code]
	if !ok {
		return domain.ErrLinkNotFound
	}

	link.Active = false
	r.links[code] = link
	return nil
}
