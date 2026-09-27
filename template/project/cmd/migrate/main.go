package main

import (
	"log"
	"os"

	// migrations:imports

	"github.com/blessed-go/sling/platform/postgres"
)

// Project migration registry for all services.
var registry = map[string][]postgres.Migration{
	// migrations:registry
}

func main() {
	service := os.Getenv("SERVICE")
	dsn := os.Getenv("POSTGRES_DSN")

	if service == "" || dsn == "" {
		log.Fatal("SERVICE and POSTGRES_DSN are required")
	}

	migrations, ok := registry[service]
	if !ok {
		log.Fatalf("unknown service: %s", service)
	}

	log.Printf("applying migrations for [%s]...", service)
	if err := postgres.Migrate(dsn, migrations...); err != nil {
		log.Fatalf("migration failed for [%s]: %v", service, err)
	}
	log.Printf("migrations for [%s] successfully applied!", service)
}
