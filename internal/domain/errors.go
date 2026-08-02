package domain

import "errors"

var (
	// ErrLinkNotFound is returned when no link exists for a given code,
	// or it exists but is no longer available (deactivated).
	ErrLinkNotFound = errors.New("link not found")

	// ErrLinkExists is returned when creating a link whose code already
	// exists (typically a custom alias collision).
	ErrLinkExists = errors.New("link already exists")

	// ErrLinkExpired is returned when a link is resolved after its TTL
	// has passed.
	ErrLinkExpired = errors.New("link expired")

	// ErrInvalidURL is returned when the URL to shorten fails validation.
	ErrInvalidURL = errors.New("invalid url")
)
