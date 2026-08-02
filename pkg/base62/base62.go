// Package base62 encodes non-negative integers into short, URL-safe
// strings using the alphabet [0-9A-Za-z], and decodes them back.
package base62

import (
	"fmt"
	"strings"
)

const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

const base = uint64(len(alphabet))

// Encode converts n into a base62 string. Encode(0) is "0".
func Encode(n uint64) string {
	if n == 0 {
		return string(alphabet[0])
	}

	var b strings.Builder
	b.Grow(11) // a uint64 needs at most 11 base62 digits

	for n > 0 {
		b.WriteByte(alphabet[n%base])
		n /= base
	}

	return reverse(b.String())
}

// Decode parses a base62 string produced by Encode back into a uint64.
// It returns an error if s is empty or contains characters outside the
// base62 alphabet.
func Decode(s string) (uint64, error) {
	if s == "" {
		return 0, fmt.Errorf("base62: empty string")
	}

	var n uint64
	for i := 0; i < len(s); i++ {
		idx := strings.IndexByte(alphabet, s[i])
		if idx < 0 {
			return 0, fmt.Errorf("base62: invalid character %q at position %d", s[i], i)
		}
		n = n*base + uint64(idx)
	}

	return n, nil
}

func reverse(s string) string {
	b := []byte(s)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}
