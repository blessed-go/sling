package postgres

import (
	"database/sql"
	"fmt"
	"io/fs"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Migration defines a domain-isolated database migration unit.
type Migration struct {
	Name string
	FS   fs.FS
	Dir  string
}

// Migrate applies database migrations for each specified migration unit.
func Migrate(dsn string, migrations ...Migration) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("postgres migrate: %w", err)
	}
	defer db.Close()

	goose.SetDialect("postgres")
	goose.SetLogger(goose.NopLogger())

	for _, m := range migrations {
		table := "goose_" + strings.ReplaceAll(m.Name, "-", "_")
		dir := m.Dir
		if dir == "" {
			dir = "migrations"
		}

		goose.SetTableName(table)
		goose.SetBaseFS(m.FS)

		if err := goose.Up(db, dir); err != nil {
			return fmt.Errorf("migrate domain %q (table %s): %w", m.Name, table, err)
		}
	}
	return nil
}
