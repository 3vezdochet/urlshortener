package auth_test

import (
	"context"
	"testing"

	"urlshortener/internal/auth"
)

func TestWithPrincipal_RoundTrip(t *testing.T) {
	principal := auth.Principal{OwnerID: "owner-1", Name: "Test Client"}
	ctx := auth.WithPrincipal(context.Background(), principal)

	got, ok := auth.PrincipalFromContext(ctx)
	if !ok {
		t.Fatal("PrincipalFromContext() ok = false, want true")
	}
	if got != principal {
		t.Errorf("PrincipalFromContext() = %+v, want %+v", got, principal)
	}
}

func TestPrincipalFromContext_Absent(t *testing.T) {
	_, ok := auth.PrincipalFromContext(context.Background())
	if ok {
		t.Error("PrincipalFromContext() ok = true on a bare context, want false")
	}
}
