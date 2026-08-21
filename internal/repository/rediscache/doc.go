package rediscache

import "urlshortener/internal/domain"

// Compile-time check that Cache satisfies the domain port.
var _ domain.LinkCache = (*Cache)(nil)
