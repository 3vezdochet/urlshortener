package domain

import "time"

// Link represents a shortened URL and its metadata.
type Link struct {
	Code        string
	OriginalURL string
	OwnerID     string
	CreatedAt   time.Time
	ExpiresAt   *time.Time
	Active      bool
}

// IsExpired reports whether the link has an expiration set and it has
// passed as of now.
func (l *Link) IsExpired(now time.Time) bool {
	return l.ExpiresAt != nil && now.After(*l.ExpiresAt)
}
