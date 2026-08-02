package shortener_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"urlshortener/internal/domain"
	"urlshortener/internal/repository/memory"
	"urlshortener/internal/usecase/shortener"
)

func newService(t *testing.T, opts ...shortener.Option) *shortener.Service {
	t.Helper()
	repo := memory.NewLinkRepo()
	codeGen := memory.NewCodeGen(0)
	return shortener.New(repo, codeGen, opts...)
}

func TestService_Create(t *testing.T) {
	tests := []struct {
		name    string
		req     shortener.CreateRequest
		wantErr error
	}{
		{
			name: "valid url",
			req:  shortener.CreateRequest{OriginalURL: "https://example.com/path"},
		},
		{
			name: "custom alias",
			req:  shortener.CreateRequest{OriginalURL: "https://example.com", CustomAlias: "promo"},
		},
		{
			name:    "empty url",
			req:     shortener.CreateRequest{OriginalURL: ""},
			wantErr: domain.ErrInvalidURL,
		},
		{
			name:    "missing scheme",
			req:     shortener.CreateRequest{OriginalURL: "example.com"},
			wantErr: domain.ErrInvalidURL,
		},
		{
			name:    "unsupported scheme",
			req:     shortener.CreateRequest{OriginalURL: "ftp://example.com"},
			wantErr: domain.ErrInvalidURL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newService(t)

			link, err := svc.Create(context.Background(), tt.req)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Create() error = %v, want %v", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("Create() unexpected error: %v", err)
			}
			if link.Code == "" {
				t.Error("Create() returned link with empty code")
			}
			if tt.req.CustomAlias != "" && link.Code != tt.req.CustomAlias {
				t.Errorf("Create() code = %q, want custom alias %q", link.Code, tt.req.CustomAlias)
			}
		})
	}
}

func TestService_Create_DuplicateAlias(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	req := shortener.CreateRequest{OriginalURL: "https://example.com", CustomAlias: "promo"}

	if _, err := svc.Create(ctx, req); err != nil {
		t.Fatalf("first Create() unexpected error: %v", err)
	}

	_, err := svc.Create(ctx, req)
	if !errors.Is(err, domain.ErrLinkExists) {
		t.Fatalf("second Create() error = %v, want %v", err, domain.ErrLinkExists)
	}
}

func TestService_Resolve(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	svc := newService(t, shortener.WithClock(clock))
	ctx := context.Background()

	link, err := svc.Create(ctx, shortener.CreateRequest{
		OriginalURL: "https://example.com",
		CustomAlias: "promo",
	})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	got, err := svc.Resolve(ctx, link.Code)
	if err != nil {
		t.Fatalf("Resolve() unexpected error: %v", err)
	}
	if got.OriginalURL != link.OriginalURL {
		t.Errorf("Resolve() OriginalURL = %q, want %q", got.OriginalURL, link.OriginalURL)
	}
}

func TestService_Resolve_NotFound(t *testing.T) {
	svc := newService(t)

	_, err := svc.Resolve(context.Background(), "missing")
	if !errors.Is(err, domain.ErrLinkNotFound) {
		t.Fatalf("Resolve() error = %v, want %v", err, domain.ErrLinkNotFound)
	}
}

func TestService_Resolve_Expired(t *testing.T) {
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	current := start
	clock := func() time.Time { return current }

	svc := newService(t, shortener.WithClock(clock))
	ctx := context.Background()

	link, err := svc.Create(ctx, shortener.CreateRequest{
		OriginalURL: "https://example.com",
		TTL:         time.Hour,
	})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	current = start.Add(2 * time.Hour)

	_, err = svc.Resolve(ctx, link.Code)
	if !errors.Is(err, domain.ErrLinkExpired) {
		t.Fatalf("Resolve() error = %v, want %v", err, domain.ErrLinkExpired)
	}
}

func TestService_Deactivate(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()

	link, err := svc.Create(ctx, shortener.CreateRequest{OriginalURL: "https://example.com"})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	if err := svc.Deactivate(ctx, link.Code); err != nil {
		t.Fatalf("Deactivate() unexpected error: %v", err)
	}

	_, err = svc.Resolve(ctx, link.Code)
	if !errors.Is(err, domain.ErrLinkNotFound) {
		t.Fatalf("Resolve() after deactivate error = %v, want %v", err, domain.ErrLinkNotFound)
	}
}
