//go:build integration

// Package integration contains tests that spin up real infrastructure
// (via testcontainers-go, which requires a local Docker daemon) rather than
// in-memory fakes. Run with:
//
//	go test -tags=integration ./test/integration/... -v
package integration

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"urlshortener/internal/domain"
	pgrepo "urlshortener/internal/repository/postgres"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("urlshortener_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		panic("start postgres container: " + err.Error())
	}
	defer func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			panic("terminate postgres container: " + err.Error())
		}
	}()

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		panic("connection string: " + err.Error())
	}

	if err := pgrepo.Migrate(dsn); err != nil {
		panic("migrate: " + err.Error())
	}

	pool, err := pgrepo.NewPool(ctx, dsn)
	if err != nil {
		panic("new pool: " + err.Error())
	}
	defer pool.Close()

	testPool = pool

	os.Exit(m.Run())
}

// resetDB truncates application tables and restarts the code sequence so
// each test starts from a clean, predictable state without paying for a
// fresh container per test.
func resetDB(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	if _, err := testPool.Exec(ctx, "TRUNCATE TABLE links"); err != nil {
		t.Fatalf("truncate links: %v", err)
	}
	if _, err := testPool.Exec(ctx, "ALTER SEQUENCE link_codes RESTART WITH 1"); err != nil {
		t.Fatalf("reset link_codes sequence: %v", err)
	}
}

func TestLinkRepo_CreateAndGet(t *testing.T) {
	resetDB(t)
	repo := pgrepo.NewLinkRepo(testPool)
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
	resetDB(t)
	repo := pgrepo.NewLinkRepo(testPool)
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
	resetDB(t)
	repo := pgrepo.NewLinkRepo(testPool)

	_, err := repo.GetByCode(context.Background(), "does-not-exist")
	if !errors.Is(err, domain.ErrLinkNotFound) {
		t.Fatalf("GetByCode() error = %v, want %v", err, domain.ErrLinkNotFound)
	}
}

func TestLinkRepo_Deactivate(t *testing.T) {
	resetDB(t)
	repo := pgrepo.NewLinkRepo(testPool)
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
	resetDB(t)
	repo := pgrepo.NewLinkRepo(testPool)

	err := repo.Deactivate(context.Background(), "does-not-exist")
	if !errors.Is(err, domain.ErrLinkNotFound) {
		t.Fatalf("Deactivate() error = %v, want %v", err, domain.ErrLinkNotFound)
	}
}

func TestLinkRepo_ExpiresAtRoundTrip(t *testing.T) {
	resetDB(t)
	repo := pgrepo.NewLinkRepo(testPool)
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
	resetDB(t)
	gen := pgrepo.NewCodeGen(testPool, "link_codes")
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
