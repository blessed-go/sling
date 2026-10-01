// Package redis provides Redis client initialization with OpenTelemetry tracing and connection pool metrics.
package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/blessed-go/sling/platform/telemetry"
	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
)

// Config defines Redis client connection parameters.
type Config struct {
	Addr         string        `toml:"addr" env:"ADDR" env-default:"redis:6379" comment:"Redis server address (host:port)"`
	Password     string        `toml:"password" env:"PASSWORD" env-default:"" comment:"Authentication password"`
	DB           int           `toml:"db" env:"DB" env-default:"0" comment:"Database index"`
	PoolSize     int           `toml:"pool_size" env:"POOL_SIZE" env-default:"10" comment:"Maximum pool connections"`
	MinIdleConns int           `toml:"min_idle_conns" env:"MIN_IDLE_CONNS" env-default:"2" comment:"Minimum idle connections"`
	DialTimeout  time.Duration `toml:"dial_timeout" env:"DIAL_TIMEOUT" env-default:"5s" comment:"Dial timeout"`
	ReadTimeout  time.Duration `toml:"read_timeout" env:"READ_TIMEOUT" env-default:"3s" comment:"Read timeout"`
	WriteTimeout time.Duration `toml:"write_timeout" env:"WRITE_TIMEOUT" env-default:"3s" comment:"Write timeout"`
}

// Option configures underlying redis options.
type Option func(*redis.Options)

// Client wraps a Redis client with health check and metrics registration support.
type Client struct {
	*redis.Client
}

// New initializes a Redis client with tracing and startup connection verification.
func New(ctx context.Context, cfg Config, opts ...Option) (*Client, error) {
	redisOpts := &redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     cfg.PoolSize,
		MinIdleConns: cfg.MinIdleConns,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}

	for _, opt := range opts {
		opt(redisOpts)
	}

	rdb := redis.NewClient(redisOpts)

	if err := redisotel.InstrumentTracing(rdb); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis: failed to instrument tracing: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis: connection failed to %s: %w", cfg.Addr, err)
	}

	return &Client{Client: rdb}, nil
}

// Ping checks Redis connectivity for readiness probes.
func (c *Client) Ping(ctx context.Context) error {
	return c.Client.Ping(ctx).Err()
}

// Close closes the underlying Redis client connection pool.
func (c *Client) Close() error {
	return c.Client.Close()
}

// RegisterMetrics exports connection pool metrics to Prometheus.
func (c *Client) RegisterMetrics(m *telemetry.Meter) error {
	return m.Int64ObservableGauge("redis_pool_connections", "Redis connection pool statistics",
		func(_ context.Context, obs *telemetry.Int64Observer) error {
			stats := c.PoolStats()
			obs.ObserveKV(int64(stats.TotalConns), "state", "total")
			obs.ObserveKV(int64(stats.IdleConns), "state", "idle")
			obs.ObserveKV(int64(stats.StaleConns), "state", "stale")
			obs.ObserveKV(int64(stats.Hits), "state", "hits")
			obs.ObserveKV(int64(stats.Misses), "state", "misses")
			obs.ObserveKV(int64(stats.Timeouts), "state", "timeouts")
			return nil
		},
	)
}
