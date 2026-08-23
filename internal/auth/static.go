package auth

import (
	"context"
	"fmt"
	"strings"
)

// StaticKeyStore is a fixed, in-memory KeyStore loaded once at startup —
// enough for service-to-service API keys where the whole set changes
// rarely and a redeploy to rotate one is acceptable. Swap in a
// database-backed KeyStore (same interface, no caller changes) if that
// stops being true.
type StaticKeyStore map[string]Principal

// Lookup implements KeyStore.
func (s StaticKeyStore) Lookup(_ context.Context, key string) (Principal, bool, error) {
	p, ok := s[key]
	return p, ok, nil
}

// ParseStaticKeys parses the API_KEYS environment variable: comma-separated
// "key:owner[:name]" entries, e.g. "sk_abc:frontend:Frontend Prod,sk_def:cli".
// When name is omitted, owner is reused as the name.
func ParseStaticKeys(raw string) (StaticKeyStore, error) {
	store := make(StaticKeyStore)
	if raw == "" {
		return store, nil
	}

	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		parts := strings.SplitN(entry, ":", 3)
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid API_KEYS entry %q: want key:owner[:name]", entry)
		}

		key, owner := parts[0], parts[1]
		if key == "" || owner == "" {
			return nil, fmt.Errorf("invalid API_KEYS entry %q: key and owner must not be empty", entry)
		}

		name := owner
		if len(parts) == 3 && parts[2] != "" {
			name = parts[2]
		}

		if _, exists := store[key]; exists {
			return nil, fmt.Errorf("invalid API_KEYS: duplicate key %q", key)
		}

		store[key] = Principal{OwnerID: owner, Name: name}
	}

	return store, nil
}
