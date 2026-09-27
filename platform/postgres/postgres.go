// Package postgres provides PostgreSQL connection pool management with OpenTelemetry tracing and migration utilities.
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/blessed-go/sling/platform/telemetry"
	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config defines PostgreSQL connection pool parameters.
type Config struct {
	DSN             string        `toml:"dsn" env:"POSTGRES_DSN" comment:"PostgreSQL database connection string"`
	MaxOpenConns    int           `toml:"max_open_conns" env:"POSTGRES_MAX_OPEN_CONNS" env-default:"25" comment:"Maximum open connections"`
	MaxIdleConns    int           `toml:"max_idle_conns" env:"POSTGRES_MAX_IDLE_CONNS" env-default:"5" comment:"Maximum idle connections"`
	ConnMaxLifetime time.Duration `toml:"conn_max_lifetime" env:"POSTGRES_CONN_MAX_LIFETIME" env-default:"15m" comment:"Maximum connection lifetime"`
	ConnMaxIdleTime time.Duration `toml:"conn_max_idle_time" env:"POSTGRES_CONN_MAX_IDLE_TIME" env-default:"5m" comment:"Maximum connection idle time"`
}

// Option configures underlying pgxpool settings.
type Option func(*pgxpool.Config)

// Client wraps a pgxpool.Pool with health check and metrics registration support.
type Client struct {
	*pgxpool.Pool
}

// New creates a PostgreSQL connection pool with OpenTelemetry tracing.
func New(ctx context.Context, cfg Config, opts ...Option) (*Client, error) {
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

	return &Client{Pool: pool}, nil
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
