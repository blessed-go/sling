package domain

import (
	"embed"

	"github.com/blessed-go/sling/platform/postgres"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

var Migration = postgres.Migration{
	Name: "__SERVICE__",
	FS:   migrationsFS,
}
