package domain

import "time"

// HealthStatus is the latest known availability state of a link's target URL.
type HealthStatus string

const (
	HealthStatusUnknown HealthStatus = "unknown" // never checked yet
	HealthStatusUp      HealthStatus = "up"
	HealthStatusDown    HealthStatus = "down"
)

// LinkHealth is the current availability state of a link, as shown to API
// callers via GET /v1/links/{code}/health.
type LinkHealth struct {
	Code             string
	Status           HealthStatus
	LastCheckedAt    *time.Time
	NextCheckAt      time.Time
	ConsecutiveFails int
	LastStatusCode   int    // 0 if the last check never got a response at all
	LastError        string // empty on success, or before the first check
}

// DueCheck is a link claimed by HealthRepository.ClaimDue: enough to
// actually perform the check (OriginalURL) plus enough history
// (ConsecutiveFails) for the checker to decide the next backoff interval.
type DueCheck struct {
	Code             string
	OriginalURL      string
	ConsecutiveFails int
}

// HealthResult is the outcome of a single check, persisted via
// HealthRepository.Record. ConsecutiveFails is the count *after* this
// check (0 if Up) — computed by the checker usecase, not the repository,
// same reasoning as cache TTLs being computed by the shortener usecase.
type HealthResult struct {
	Code             string
	CheckedAt        time.Time
	Up               bool
	StatusCode       int   // 0 if no response was received at all
	Err              error // nil on success
	ConsecutiveFails int
}
