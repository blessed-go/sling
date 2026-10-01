// Package postgres provides PostgreSQL connection pool management with OpenTelemetry tracing,
// transaction orchestration (Unit of Work), and migration utilities.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/blessed-go/sling/platform/telemetry"
	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type txKey struct{}

// TxManager defines the standard platform contract for atomic transaction execution.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Config defines PostgreSQL connection pool parameters.
type Config struct {
	DSN             string        `toml:"dsn" env:"DSN" comment:"PostgreSQL database connection string"`
	MaxOpenConns    int           `toml:"max_open_conns" env:"MAX_OPEN_CONNS" env-default:"25" comment:"Maximum open connections"`
	MaxIdleConns    int           `toml:"max_idle_conns" env:"MAX_IDLE_CONNS" env-default:"5" comment:"Maximum idle connections"`
	ConnMaxLifetime time.Duration `toml:"conn_max_lifetime" env:"CONN_MAX_LIFETIME" env-default:"15m" comment:"Maximum connection lifetime"`
	ConnMaxIdleTime time.Duration `toml:"conn_max_idle_time" env:"CONN_MAX_IDLE_TIME" env-default:"5m" comment:"Maximum connection idle time"`
	TxTimeout       time.Duration `toml:"tx_timeout" env:"TX_TIMEOUT" env-default:"3s" comment:"Maximum transaction execution timeout"`
}

func (c *Config) SetDefaults() {
	if c.MaxOpenConns <= 0 {
		c.MaxOpenConns = 25
	}
	if c.MaxIdleConns <= 0 {
		c.MaxIdleConns = 5
	}
	if c.ConnMaxLifetime <= 0 {
		c.ConnMaxLifetime = 15 * time.Minute
	}
	if c.ConnMaxIdleTime <= 0 {
		c.ConnMaxIdleTime = 5 * time.Minute
	}
	if c.TxTimeout <= 0 {
		c.TxTimeout = 3 * time.Second
	}
}

// Validate проверяет инварианты конфигурации базы данных
func (c Config) Validate() error {
	if c.DSN == "" {
		return errors.New("dsn is required")
	}
	if c.TxTimeout <= 0 {
		return errors.New("tx_timeout must be greater than 0")
	}
	return nil
}

// Option configures underlying pgxpool settings.
type Option func(*pgxpool.Config)

// Client wraps a pgxpool.Pool, adding transaction lifecycle orchestration and metrics.
type Client struct {
	*pgxpool.Pool
	cfg Config
}

var _ TxManager = (*Client)(nil)

// New creates a PostgreSQL connection pool with OpenTelemetry tracing and startup ping.
func New(ctx context.Context, cfg Config, opts ...Option) (*Client, error) {
	cfg.SetDefaults() // just in case someone created config out of config.Load
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("postgres: invalid config: %w", err)
	}

	pgxCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("postgres: invalid dsn: %w", err)
	}

	pgxCfg.MaxConns = int32(cfg.MaxOpenConns)
	pgxCfg.MinConns = int32(cfg.MaxIdleConns)
	pgxCfg.MaxConnLifetime = cfg.ConnMaxLifetime
	pgxCfg.MaxConnIdleTime = cfg.ConnMaxIdleTime
	pgxCfg.ConnConfig.Tracer = otelpgx.NewTracer()

	for _, opt := range opts {
		opt(pgxCfg)
	}

	pool, err := pgxpool.NewWithConfig(ctx, pgxCfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: failed to connect to pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping failed: %w", err)
	}

	return &Client{Pool: pool, cfg: cfg}, nil
}

// Close closes all connections in the pool.
func (c *Client) Close() error {
	c.Pool.Close()
	return nil
}

// Ping checks database connectivity for readiness probes.
func (c *Client) Ping(ctx context.Context) error {
	return c.Pool.Ping(ctx)
}

// RegisterMetrics exports connection pool statistics to Prometheus.
func (c *Client) RegisterMetrics(m *telemetry.Meter) error {
	return m.Int64ObservableGauge("postgres_pool_connections", "Current state of pgx connection pool",
		func(_ context.Context, obs *telemetry.Int64Observer) error {
			stat := c.Pool.Stat()
			obs.ObserveKV(int64(stat.TotalConns()), "state", "total")
			obs.ObserveKV(int64(stat.AcquiredConns()), "state", "acquired")
			obs.ObserveKV(int64(stat.IdleConns()), "state", "idle")
			return nil
		},
	)
}

// QueryRow automatically executes within the active transaction in ctx if present, or falls back to the pool.
func (c *Client) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx.QueryRow(ctx, sql, args...)
	}
	return c.Pool.QueryRow(ctx, sql, args...)
}

// Exec automatically executes within the active transaction in ctx if present, or falls back to the pool.
func (c *Client) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx.Exec(ctx, sql, arguments...)
	}
	return c.Pool.Exec(ctx, sql, arguments...)
}

// Query automatically executes within the active transaction in ctx if present, or falls back to the pool.
func (c *Client) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx.Query(ctx, sql, args...)
	}
	return c.Pool.Query(ctx, sql, args...)
}

// SendBatch sends a batch within the active transaction in ctx if present, or falls back to the pool.
func (c *Client) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx.SendBatch(ctx, b)
	}
	return c.Pool.SendBatch(ctx, b)
}

// CopyFrom executes a copy operation within the active transaction in ctx if present, or falls back to the pool.
func (c *Client) CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error) {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx.CopyFrom(ctx, tableName, columnNames, rowSrc)
	}
	return c.Pool.CopyFrom(ctx, tableName, columnNames, rowSrc)
}

// WithinTx executes fn inside an atomic transaction.
// If fn returns an error or panics, the transaction is rolled back.
// If fn completes successfully (nil), the transaction is committed.
// Nested WithinTx calls safely reuse the parent transaction without deadlocks.
func (c *Client) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if HasTx(ctx) {
		return fn(ctx)
	}

	txCtx, cancel := context.WithTimeout(ctx, c.cfg.TxTimeout)
	defer cancel()

	tx, err := c.Pool.Begin(txCtx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}

	txCtx = context.WithValue(txCtx, txKey{}, tx)

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(txCtx)
			panic(p)
		}
	}()

	if err := fn(txCtx); err != nil {
		if rbErr := tx.Rollback(txCtx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			return errors.Join(err, fmt.Errorf("postgres: rollback: %w", rbErr))
		}
		return err
	}

	if err := tx.Commit(txCtx); err != nil {
		return fmt.Errorf("postgres: commit tx: %w", err)
	}

	return nil
}

// HasTx reports whether ctx carries an active database transaction.
func HasTx(ctx context.Context) bool {
	_, ok := ctx.Value(txKey{}).(pgx.Tx)
	return ok
}
