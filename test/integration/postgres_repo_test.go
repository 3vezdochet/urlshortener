//go:build integration

// Package integration contains tests that require a running Postgres
// instance, pointed to by TEST_DATABASE_URL. Run with:
//
//	TEST_DATABASE_URL=postgres://user:pass@localhost:5432/db?sslmode=disable \
//	  go test -tags=integration ./test/integration/... -v
package integration

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"urlshortener/internal/domain"
	"urlshortener/internal/repository/postgres"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping Postgres integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("postgres.Open() unexpected error: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	applyMigrations(t, db)
	t.Cleanup(func() { resetSchema(t, db) })

	return db
}

// applyMigrations runs every *.up.sql file in migrations/, in order. This
// is a minimal stand-in for a real migration tool (golang-migrate/goose),
// used here only to prepare the schema for tests without adding a heavy
// dependency to the module graph.
func applyMigrations(t *testing.T, db *sql.DB) {
	t.Helper()

	dir := filepath.Join("..", "..", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatalf("read migration %s: %v", f, err)
		}
		if _, err := db.Exec(string(b)); err != nil {
			t.Fatalf("apply migration %s: %v", f, err)
		}
	}
}

func resetSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`TRUNCATE links; ALTER SEQUENCE link_code_seq RESTART WITH 1`); err != nil {
		t.Logf("reset schema: %v", err)
	}
}

func TestLinkRepo_CreateAndGet(t *testing.T) {
	db := testDB(t)
	repo := postgres.NewLinkRepo(db)
	ctx := context.Background()

	link := &domain.Link{
		Code:        "int-test-1",
		OriginalURL: "https://example.com",
		OwnerID:     "owner-1",
		CreatedAt:   time.Now().UTC().Truncate(time.Microsecond),
		Active:      true,
	}

	if err := repo.Create(ctx, link); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	got, err := repo.GetByCode(ctx, link.Code)
	if err != nil {
		t.Fatalf("GetByCode() unexpected error: %v", err)
	}
	if got.OriginalURL != link.OriginalURL {
		t.Errorf("OriginalURL = %q, want %q", got.OriginalURL, link.OriginalURL)
	}
	if got.OwnerID != link.OwnerID {
		t.Errorf("OwnerID = %q, want %q", got.OwnerID, link.OwnerID)
	}
	if got.ExpiresAt != nil {
		t.Errorf("ExpiresAt = %v, want nil", got.ExpiresAt)
	}
}

func TestLinkRepo_CreateDuplicate(t *testing.T) {
	db := testDB(t)
	repo := postgres.NewLinkRepo(db)
	ctx := context.Background()

	link := &domain.Link{Code: "int-test-dup", OriginalURL: "https://example.com", CreatedAt: time.Now().UTC()}
	if err := repo.Create(ctx, link); err != nil {
		t.Fatalf("first Create() unexpected error: %v", err)
	}

	err := repo.Create(ctx, link)
	if !errors.Is(err, domain.ErrLinkExists) {
		t.Fatalf("second Create() error = %v, want %v", err, domain.ErrLinkExists)
	}
}

func TestLinkRepo_GetMissing(t *testing.T) {
	db := testDB(t)
	repo := postgres.NewLinkRepo(db)

	_, err := repo.GetByCode(context.Background(), "does-not-exist")
	if !errors.Is(err, domain.ErrLinkNotFound) {
		t.Fatalf("GetByCode() error = %v, want %v", err, domain.ErrLinkNotFound)
	}
}

func TestLinkRepo_Deactivate(t *testing.T) {
	db := testDB(t)
	repo := postgres.NewLinkRepo(db)
	ctx := context.Background()

	link := &domain.Link{
		Code: "int-test-deact", OriginalURL: "https://example.com",
		CreatedAt: time.Now().UTC(), Active: true,
	}
	if err := repo.Create(ctx, link); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	if err := repo.Deactivate(ctx, link.Code); err != nil {
		t.Fatalf("Deactivate() unexpected error: %v", err)
	}

	got, err := repo.GetByCode(ctx, link.Code)
	if err != nil {
		t.Fatalf("GetByCode() unexpected error: %v", err)
	}
	if got.Active {
		t.Error("Active = true, want false after Deactivate")
	}
}

func TestLinkRepo_Deactivate_Missing(t *testing.T) {
	db := testDB(t)
	repo := postgres.NewLinkRepo(db)

	err := repo.Deactivate(context.Background(), "does-not-exist")
	if !errors.Is(err, domain.ErrLinkNotFound) {
		t.Fatalf("Deactivate() error = %v, want %v", err, domain.ErrLinkNotFound)
	}
}

func TestLinkRepo_ExpiresAtRoundTrip(t *testing.T) {
	db := testDB(t)
	repo := postgres.NewLinkRepo(db)
	ctx := context.Background()

	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	link := &domain.Link{
		Code:        "int-test-ttl",
		OriginalURL: "https://example.com",
		CreatedAt:   time.Now().UTC().Truncate(time.Microsecond),
		ExpiresAt:   &expires,
		Active:      true,
	}
	if err := repo.Create(ctx, link); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	got, err := repo.GetByCode(ctx, link.Code)
	if err != nil {
		t.Fatalf("GetByCode() unexpected error: %v", err)
	}
	if got.ExpiresAt == nil {
		t.Fatal("ExpiresAt = nil, want non-nil")
	}
	if !got.ExpiresAt.Equal(expires) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, expires)
	}
}

func TestCodeGen_Next(t *testing.T) {
	db := testDB(t)
	gen := postgres.NewCodeGen(db)
	ctx := context.Background()

	first, err := gen.Next(ctx)
	if err != nil {
		t.Fatalf("Next() unexpected error: %v", err)
	}
	second, err := gen.Next(ctx)
	if err != nil {
		t.Fatalf("Next() unexpected error: %v", err)
	}
	if second <= first {
		t.Errorf("Next() not increasing: first=%d second=%d", first, second)
	}
}
