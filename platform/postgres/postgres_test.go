package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/blessed-go/sling/platform/postgres"
)

func TestPostgres_WithinTx_Lifecycle(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable"
	}
	ctx := context.Background()

	cfg := postgres.Config{
		DSN:          dsn,
		MaxOpenConns: 5,
	}

	client, err := postgres.New(ctx, cfg)
	if err != nil {
		t.Skipf("Postgres is not running on %s (%v), skipping integration test", dsn, err)
	}
	defer client.Close()

	_, err = client.Exec(ctx, `
		CREATE TEMP TABLE tx_test (
			id SERIAL PRIMARY KEY,
			val TEXT NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("failed to create temp table: %v", err)
	}

	if postgres.HasTx(ctx) {
		t.Fatal("expected HasTx to be false outside of WithinTx")
	}

	err = client.WithinTx(ctx, func(txCtx context.Context) error {
		if !postgres.HasTx(txCtx) {
			return errors.New("expected HasTx to be true inside WithinTx")
		}

		if _, e := client.Exec(txCtx, "INSERT INTO tx_test (val) VALUES ($1)", "commit_1"); e != nil {
			return e
		}
		if _, e := client.Exec(txCtx, "INSERT INTO tx_test (val) VALUES ($1)", "commit_2"); e != nil {
			return e
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithinTx failed: %v", err)
	}

	var count int
	_ = client.QueryRow(ctx, "SELECT count(*) FROM tx_test WHERE val LIKE 'commit_%'").Scan(&count)
	if count != 2 {
		t.Fatalf("expected 2 committed rows, got %d", count)
	}

	err = client.WithinTx(ctx, func(txCtx context.Context) error {
		if _, e := client.Exec(txCtx, "INSERT INTO tx_test (val) VALUES ($1)", "rollback_me"); e != nil {
			return e
		}
		return errors.New("business error: abort")
	})
	if err == nil {
		t.Fatal("expected WithinTx to return business error, got nil")
	}

	var rollbackCount int
	_ = client.QueryRow(ctx, "SELECT count(*) FROM tx_test WHERE val = 'rollback_me'").Scan(&rollbackCount)
	if rollbackCount != 0 {
		t.Fatalf("expected 0 rows after rollback, got %d (atomicity broken!)", rollbackCount)
	}

	err = client.WithinTx(ctx, func(outerCtx context.Context) error {
		if _, e := client.Exec(outerCtx, "INSERT INTO tx_test (val) VALUES ($1)", "nested_outer"); e != nil {
			return e
		}

		return client.WithinTx(outerCtx, func(innerCtx context.Context) error {
			if _, e := client.Exec(innerCtx, "INSERT INTO tx_test (val) VALUES ($1)", "nested_inner"); e != nil {
				return e
			}
			return nil
		})
	})
	if err != nil {
		t.Fatalf("nested WithinTx failed: %v", err)
	}

	var nestedCount int
	_ = client.QueryRow(ctx, "SELECT count(*) FROM tx_test WHERE val LIKE 'nested_%'").Scan(&nestedCount)
	if nestedCount != 2 {
		t.Fatalf("expected 2 nested rows committed, got %d", nestedCount)
	}

	func() {
		defer func() {
			_ = recover()
		}()
		_ = client.WithinTx(ctx, func(txCtx context.Context) error {
			_, _ = client.Exec(txCtx, "INSERT INTO tx_test (val) VALUES ($1)", "panic_row")
			panic("simulated crash")
		})
	}()

	var panicCount int
	_ = client.QueryRow(ctx, "SELECT count(*) FROM tx_test WHERE val = 'panic_row'").Scan(&panicCount)
	if panicCount != 0 {
		t.Fatalf("expected panic_row to be rolled back, got %d in DB", panicCount)
	}
}
