# PostgreSQL Client & Migrations (`platform/postgres`)

PostgreSQL 18 connection pool wrapper based on `jackc/pgx/v5`. Features automatic OpenTelemetry SQL tracing (`otelpgx`), connection pool metrics for Prometheus, transparent context-aware transactions (Unit of Work), and domain-isolated Goose migrations.

---

## 1. Configuration (`conf.toml`)

```toml
[postgres]
dsn = "postgres://user:pass@localhost:5432/my_db?sslmode=disable" # [REQUIRED] Connection string
max_open_conns = 25                                                # Maximum open pool connections (default: 25)
max_idle_conns = 5                                                 # Maximum idle connections (default: 5)
conn_max_lifetime = "15m"                                          # Maximum connection lifetime (default: 15m)
conn_max_idle_time = "5m"                                          # Maximum connection idle time (default: 5m)
tx_timeout = "3s"                                                  # Hard ceiling on transaction duration (default: 3s)
```

### Environment Variables

Config structs declare relative tags (`env:"DSN"`). When embedded in a Sling service with the standard `env-prefix:"POSTGRES_"`, they resolve to:

| Config Key | Relative Tag | Standard Service Env (with `env-prefix`) | Default |
| :--- | :--- | :--- | :--- |
| `dsn` | `env:"DSN"` | `POSTGRES_DSN` | *none (required)* |
| `max_open_conns` | `env:"MAX_OPEN_CONNS"` | `POSTGRES_MAX_OPEN_CONNS` | `25` |
| `max_idle_conns` | `env:"MAX_IDLE_CONNS"` | `POSTGRES_MAX_IDLE_CONNS` | `5` |
| `conn_max_lifetime` | `env:"CONN_MAX_LIFETIME"` | `POSTGRES_CONN_MAX_LIFETIME` | `15m` |
| `conn_max_idle_time` | `env:"CONN_MAX_IDLE_TIME"` | `POSTGRES_CONN_MAX_IDLE_TIME` | `5m` |
| `tx_timeout` | `env:"TX_TIMEOUT"` | `POSTGRES_TX_TIMEOUT` | `3s` |

*Multi-Instance Note: If a service requires multiple databases (primary + replica), mount them with distinct prefixes (`PRIMARY_DB_`, `REPLICA_DB_`) for conflict-free configuration.*
*All fields are validated on startup via `SetDefaults()` and `Validate()`. Invalid DSNs or non-positive timeouts fail fast during application bootstrap.*

---

## 2. Usage

### A. Initialization & Lifecycle Attachment (`cmd/<service>/main.go`)

```go
pg, err := postgres.New(ctx, cfg.Postgres)
core.FatalIf(err, "failed to connect to postgres")

// Attach to application supervisor:
// 1. Registers readiness ping for /readyz
// 2. Exports connection pool metrics to Prometheus
// 3. Registers LIFO closer for Phase 2 graceful shutdown
core.Attach("postgres", pg)

// Pass client directly to repository and service constructors
repo := booking.NewRepo(pg)
svc := booking.NewService(core.Logger(), pg, repo)
```

### B. Standard Repositories with Method Shadowing (`repo_postgres.go`)

Repositories hold `*postgres.Client`. Through Go method shadowing, calls like `r.pg.QueryRow`, `r.pg.Exec`, and `r.pg.Query` **automatically route queries to the active transaction if present in `ctx`, or fall back to the pool**:

```go
type Repo struct {
    pg *postgres.Client
}

func NewRepo(pg *postgres.Client) *Repo {
    return &Repo{pg: pg}
}

func (r *Repo) Get(ctx context.Context, id uuid.UUID) (*Item, error) {
    query := `SELECT id, title, created_at FROM items WHERE id = $1`
    var item Item

    // Executes in active transaction if called inside WithinTx; otherwise executes in pool.
    // Zero helpers or ceremony required. Impossible to bypass the transaction accidentally!
    err := r.pg.QueryRow(ctx, query, id).Scan(&item.ID, &item.Title, &item.CreatedAt)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return nil, ErrItemNotFound
        }
        return nil, err
    }
    return &item, nil
}
```

### C. Atomic Transactions (`service.go`)

Use `postgres.TxManager` to coordinate multiple repository updates inside an atomic database transaction:

```go
type Service struct {
    log          *slog.Logger
    tx           postgres.TxManager
    bookingRepo  BookingRepository
    customerRepo CustomerRepository
}

func NewService(log *slog.Logger, tx postgres.TxManager, b BookingRepository, c CustomerRepository) *Service {
    return &Service{log: log, tx: tx, bookingRepo: b, customerRepo: c}
}

func (s *Service) ConfirmBooking(ctx context.Context, bookingID, customerID uuid.UUID) error {
    return s.tx.WithinTx(ctx, func(ctx context.Context) error {
        // 1. Update booking state (executes inside transaction via shadowed pg.Exec)
        if err := s.bookingRepo.SetStatus(ctx, bookingID, "CONFIRMED"); err != nil {
            return err // Triggers automatic ROLLBACK
        }

        // 2. Deduct customer balance (executes inside same transaction)
        if err := s.customerRepo.Deduct(ctx, customerID, 100); err != nil {
            return err // Triggers automatic ROLLBACK
        }

        return nil // Triggers automatic COMMIT
    })
}
```

* **Automatic Rollback:** Any returned error or unhandled panic inside `WithinTx` immediately triggers a `ROLLBACK` (panics are safely re-thrown after rollback).
* **Timeout Ceiling:** Every transaction is bound by `tx_timeout` (default 3s). Transactions taking longer are aborted to prevent pool starvation.
* **Safe Nesting:** Nested `WithinTx` calls safely reuse the parent transaction without deadlocks or taking additional connections from the pool.
* **Cache Bypass:** Decorators can check `postgres.HasTx(ctx)` to bypass caches and ensure Read-Your-Own-Writes consistency inside transactions.

### D. Multi-Domain Isolated Migrations (`cmd/migrate/main.go`)

In modular monoliths sharing a database, domains avoid version collisions by tracking migrations in dedicated tables (`goose_<domain>`):

```go
// Embed domain migrations in internal/<service>/embed.go:
//
// //go:embed migrations/*.sql
// var migrationsFS embed.FS
// var Migration = postgres.Migration{Name: "booking", FS: migrationsFS}

// Apply multi-domain migrations:
err := postgres.Migrate(dsn,
    booking.Migration,           // Tracks in table: goose_booking
    booking_analytics.Migration, // Tracks in table: goose_booking_analytics
)
```

---

## 3. Observability & Connection Pool Metrics

When attached via `core.Attach("postgres", pg)`, the client exports live connection pool stats to Prometheus:

| Metric Name | Type | Labels | Description |
| :--- | :--- | :--- | :--- |
| `postgres_pool_connections` | Gauge | `state="total"` | Total connections currently established in the pool |
| `postgres_pool_connections` | Gauge | `state="acquired"` | Connections currently checked out and executing queries |
| `postgres_pool_connections` | Gauge | `state="idle"` | Connections currently idle and waiting in the pool |

All SQL queries automatically create child spans in OpenTelemetry with the query text, sanitized arguments, and error tracking via `otelpgx`.