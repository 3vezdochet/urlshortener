package auth_test

import (
	"context"
	"testing"

	"urlshortener/internal/auth"
)

func TestParseStaticKeys(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    auth.StaticKeyStore
		wantErr bool
	}{
		{
			name: "empty",
			raw:  "",
			want: auth.StaticKeyStore{},
		},
		{
			name: "single key, name defaults to owner",
			raw:  "sk_abc:frontend",
			want: auth.StaticKeyStore{"sk_abc": {OwnerID: "frontend", Name: "frontend"}},
		},
		{
			name: "single key with explicit name",
			raw:  "sk_abc:frontend:Frontend Prod",
			want: auth.StaticKeyStore{"sk_abc": {OwnerID: "frontend", Name: "Frontend Prod"}},
		},
		{
			name: "multiple keys, whitespace tolerated",
			raw:  "sk_abc:frontend, sk_def:cli:CLI Tool ,sk_ghi:mobile",
			want: auth.StaticKeyStore{
				"sk_abc": {OwnerID: "frontend", Name: "frontend"},
				"sk_def": {OwnerID: "cli", Name: "CLI Tool"},
				"sk_ghi": {OwnerID: "mobile", Name: "mobile"},
			},
		},
		{
			name:    "missing owner",
			raw:     "sk_abc",
			wantErr: true,
		},
		{
			name:    "empty owner",
			raw:     "sk_abc:",
			wantErr: true,
		},
		{
			name:    "duplicate key",
			raw:     "sk_abc:frontend,sk_abc:mobile",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := auth.ParseStaticKeys(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseStaticKeys(%q) error = %v, wantErr %v", tt.raw, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			if len(got) != len(tt.want) {
				t.Fatalf("ParseStaticKeys(%q) = %d entries, want %d", tt.raw, len(got), len(tt.want))
			}
			for key, wantPrincipal := range tt.want {
				if got[key] != wantPrincipal {
					t.Errorf("entry %q = %+v, want %+v", key, got[key], wantPrincipal)
				}
			}
		})
	}
}

func TestStaticKeyStore_Lookup(t *testing.T) {
	store, err := auth.ParseStaticKeys("sk_abc:frontend")
	if err != nil {
		t.Fatalf("ParseStaticKeys() unexpected error: %v", err)
	}

	got, ok, err := store.Lookup(context.Background(), "sk_abc")
	if err != nil {
		t.Fatalf("Lookup() unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("Lookup() ok = false, want true")
	}
	if got.OwnerID != "frontend" {
		t.Errorf("OwnerID = %q, want %q", got.OwnerID, "frontend")
	}

	_, ok, err = store.Lookup(context.Background(), "unknown")
	if err != nil {
		t.Fatalf("Lookup() unexpected error: %v", err)
	}
	if ok {
		t.Error("Lookup() ok = true for an unknown key, want false")
	}
}
