package observability_test

import (
	"testing"

	"urlshortener/internal/observability"
)

func TestNewRegistry_Gatherable(t *testing.T) {
	reg := observability.NewRegistry()

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() unexpected error: %v", err)
	}
	if len(families) == 0 {
		t.Fatal("Gather() returned no metric families, want the Go/process collectors' output")
	}
}

func TestNewRegistry_ReturnsFreshRegistryEachCall(t *testing.T) {
	// Two independent registries must not panic on "duplicate collector
	// registration" — that's the whole reason this isn't
	// prometheus.DefaultRegisterer.
	reg1 := observability.NewRegistry()
	reg2 := observability.NewRegistry()

	if _, err := reg1.Gather(); err != nil {
		t.Errorf("reg1.Gather() unexpected error: %v", err)
	}
	if _, err := reg2.Gather(); err != nil {
		t.Errorf("reg2.Gather() unexpected error: %v", err)
	}
}
