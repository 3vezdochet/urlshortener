package base62_test

import (
	"testing"

	"urlshortener/pkg/base62"
)

func TestEncode(t *testing.T) {
	tests := []struct {
		name string
		in   uint64
		want string
	}{
		{"zero", 0, "0"},
		{"single digit", 9, "9"},
		{"first letter", 10, "A"},
		{"base value", 62, "10"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := base62.Encode(tt.in)
			if got != tt.want {
				t.Errorf("Encode(%d) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDecode(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    uint64
		wantErr bool
	}{
		{"zero", "0", 0, false},
		{"letter", "A", 10, false},
		{"round base", "10", 62, false},
		{"empty", "", 0, true},
		{"invalid char", "a b", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := base62.Decode(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Decode(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("Decode(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	values := []uint64{0, 1, 61, 62, 12345, 987654321, ^uint64(0)}

	for _, v := range values {
		encoded := base62.Encode(v)
		decoded, err := base62.Decode(encoded)
		if err != nil {
			t.Fatalf("Decode(%q) unexpected error: %v", encoded, err)
		}
		if decoded != v {
			t.Errorf("round trip failed: Encode(%d) = %q, Decode gave %d", v, encoded, decoded)
		}
	}
}
