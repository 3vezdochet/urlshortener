// Package shortener implements the core use case: turning URLs into short
// codes and resolving codes back to URLs.
package shortener

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"urlshortener/internal/domain"
	"urlshortener/pkg/base62"
)

// Clock abstracts time.Now so tests can control the current time.
type Clock func() time.Time

// Service implements link shortening and resolution.
type Service struct {
	repo    domain.LinkRepository
	codeGen domain.CodeGenerator
	now     Clock
	ttl     time.Duration
}

// Option configures a Service.
type Option func(*Service)

// WithClock overrides the default time source (time.Now). Useful in tests.
func WithClock(c Clock) Option {
	return func(s *Service) { s.now = c }
}

// WithDefaultTTL sets the TTL applied to links created without an explicit
// expiration. The zero value disables the default (links never expire
// unless a TTL is requested explicitly).
func WithDefaultTTL(ttl time.Duration) Option {
	return func(s *Service) { s.ttl = ttl }
}

// New creates a Service backed by repo, using codeGen to derive short codes.
func New(repo domain.LinkRepository, codeGen domain.CodeGenerator, opts ...Option) *Service {
	s := &Service{
		repo:    repo,
		codeGen: codeGen,
		now:     time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// CreateRequest describes a request to shorten a URL.
type CreateRequest struct {
	OriginalURL string
	OwnerID     string
	CustomAlias string        // optional; if empty, a code is generated
	TTL         time.Duration // optional; overrides the service default
}

// Create validates req and stores a new short link, returning it.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*domain.Link, error) {
	if err := validateURL(req.OriginalURL); err != nil {
		return nil, err
	}

	code := req.CustomAlias
	if code == "" {
		id, err := s.codeGen.Next(ctx)
		if err != nil {
			return nil, fmt.Errorf("generate code: %w", err)
		}
		code = base62.Encode(id)
	}

	link := &domain.Link{
		Code:        code,
		OriginalURL: req.OriginalURL,
		OwnerID:     req.OwnerID,
		CreatedAt:   s.now(),
		Active:      true,
	}

	if ttl := effectiveTTL(req.TTL, s.ttl); ttl > 0 {
		expiresAt := link.CreatedAt.Add(ttl)
		link.ExpiresAt = &expiresAt
	}

	if err := s.repo.Create(ctx, link); err != nil {
		return nil, fmt.Errorf("store link: %w", err)
	}

	return link, nil
}

// Resolve returns the active, non-expired link for code.
func (s *Service) Resolve(ctx context.Context, code string) (*domain.Link, error) {
	link, err := s.repo.GetByCode(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("lookup link: %w", err)
	}

	if !link.Active {
		return nil, domain.ErrLinkNotFound
	}

	if link.IsExpired(s.now()) {
		return nil, domain.ErrLinkExpired
	}

	return link, nil
}

// Deactivate marks the link identified by code as inactive.
func (s *Service) Deactivate(ctx context.Context, code string) error {
	if err := s.repo.Deactivate(ctx, code); err != nil {
		return fmt.Errorf("deactivate link: %w", err)
	}
	return nil
}

func effectiveTTL(requested, fallback time.Duration) time.Duration {
	if requested > 0 {
		return requested
	}
	return fallback
}

func validateURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("%w: empty url", domain.ErrInvalidURL)
	}

	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrInvalidURL, err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: unsupported scheme %q", domain.ErrInvalidURL, u.Scheme)
	}

	if u.Host == "" {
		return fmt.Errorf("%w: missing host", domain.ErrInvalidURL)
	}

	return nil
}
