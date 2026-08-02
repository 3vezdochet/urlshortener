package memory_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"urlshortener/internal/domain"
	"urlshortener/internal/repository/memory"
)

func TestLinkRepo_CreateAndGet(t *testing.T) {
	repo := memory.NewLinkRepo()
	ctx := context.Background()

	link := &domain.Link{Code: "abc", OriginalURL: "https://example.com", Active: true}
	if err := repo.Create(ctx, link); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	got, err := repo.GetByCode(ctx, "abc")
	if err != nil {
		t.Fatalf("GetByCode() unexpected error: %v", err)
	}
	if got.OriginalURL != link.OriginalURL {
		t.Errorf("GetByCode() OriginalURL = %q, want %q", got.OriginalURL, link.OriginalURL)
	}
}

func TestLinkRepo_CreateDuplicate(t *testing.T) {
	repo := memory.NewLinkRepo()
	ctx := context.Background()
	link := &domain.Link{Code: "abc", OriginalURL: "https://example.com"}

	if err := repo.Create(ctx, link); err != nil {
		t.Fatalf("first Create() unexpected error: %v", err)
	}

	err := repo.Create(ctx, link)
	if !errors.Is(err, domain.ErrLinkExists) {
		t.Fatalf("second Create() error = %v, want %v", err, domain.ErrLinkExists)
	}
}

func TestLinkRepo_GetMissing(t *testing.T) {
	repo := memory.NewLinkRepo()

	_, err := repo.GetByCode(context.Background(), "missing")
	if !errors.Is(err, domain.ErrLinkNotFound) {
		t.Fatalf("GetByCode() error = %v, want %v", err, domain.ErrLinkNotFound)
	}
}

func TestLinkRepo_Deactivate(t *testing.T) {
	repo := memory.NewLinkRepo()
	ctx := context.Background()
	link := &domain.Link{Code: "abc", OriginalURL: "https://example.com", Active: true}

	if err := repo.Create(ctx, link); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if err := repo.Deactivate(ctx, "abc"); err != nil {
		t.Fatalf("Deactivate() unexpected error: %v", err)
	}

	got, err := repo.GetByCode(ctx, "abc")
	if err != nil {
		t.Fatalf("GetByCode() unexpected error: %v", err)
	}
	if got.Active {
		t.Error("GetByCode() Active = true, want false after Deactivate")
	}
}

func TestLinkRepo_ConcurrentAccess(t *testing.T) {
	repo := memory.NewLinkRepo()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			code := string(rune('a' + n%26))
			_ = repo.Create(ctx, &domain.Link{Code: code, OriginalURL: "https://example.com"})
			_, _ = repo.GetByCode(ctx, code)
		}(i)
	}
	wg.Wait()
}
