// Command migrate applies all pending PostgreSQL migrations embedded in the
// postgres package. Run it as a separate step before starting the
// api/checker/analytics services — e.g. in CI/CD or as a Kubernetes init
// container — rather than migrating from inside a running service.
package main

import (
	"log"
	"os"

	"urlshortener/internal/repository/postgres"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}

	if err := postgres.Migrate(dsn); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	log.Println("migrations applied")
}
