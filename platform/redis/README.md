# Redis & Valkey Client (`platform/redis`)

Valkey/Redis client wrapper based on `go-redis/v9`. 
Features: automatic OpenTelemetry command tracing (`redisotel`), connection pool metrics for Prometheus, readiness checks for `/readyz`, and adapters for caching and idempotency.

---

## 1. Configuration (`conf.toml`)

```toml
[redis]
addr = "localhost:6379"     # Redis/Valkey server host:port (default: redis:6379)
password = ""               # Optional authentication password
db = 0                      # Database index (default: 0)
pool_size = 10              # Maximum socket pool connections (default: 10)
min_idle_conns = 2          # Minimum idle connections kept alive (default: 2)
dial_timeout = "5s"         # Connection establishment timeout (default: 5s)
read_timeout = "3s"         # Socket read timeout (default: 3s)
write_timeout = "3s"        # Socket write timeout (default: 3s)
```

### Environment Variables

Config structs declare relative tags (`env:"ADDR"`). When embedded in a Sling service with the standard `env-prefix:"REDIS_"`, they resolve to:

| Config Key | Relative Tag | Standard Service Env (with `env-prefix`) | Default |
| :--- | :--- | :--- | :--- |
| `addr` | `env:"ADDR"` | `REDIS_ADDR` | `redis:6379` |
| `password` | `env:"PASSWORD"` | `REDIS_PASSWORD` | `""` |
| `db` | `env:"DB"` | `REDIS_DB` | `0` |
| `pool_size` | `env:"POOL_SIZE"` | `REDIS_POOL_SIZE` | `10` |
| `min_idle_conns` | `env:"MIN_IDLE_CONNS"` | `REDIS_MIN_IDLE_CONNS` | `2` |
| `dial_timeout` | `env:"DIAL_TIMEOUT"` | `REDIS_DIAL_TIMEOUT` | `5s` |
| `read_timeout` | `env:"READ_TIMEOUT"` | `REDIS_READ_TIMEOUT` | `3s` |
| `write_timeout` | `env:"WRITE_TIMEOUT"` | `REDIS_WRITE_TIMEOUT` | `3s` |

*Multi-Instance Note: If a service uses multiple Redis instances (e.g. caching vs. queues), mount them with distinct prefixes (`CACHE_REDIS_`, `QUEUE_REDIS_`).*
---

## 2. Usage

### A. Initialization & Lifecycle Attachment (`cmd/<service>/main.go`)

```go
rdb, err := redis.New(ctx, cfg.Redis)
core.FatalIf(err, "failed to connect to redis")

// Attach to application supervisor:
// 1. Registers readiness ping for /readyz
// 2. Registers connection pool metrics with Prometheus
// 3. Registers LIFO closer for Phase 2 graceful shutdown
core.Attach("redis", rdb)
```

### B. Direct Command Execution

`redis.Client` embeds `*redis.Client` directly, so all Redis commands are available as native methods:

```go
// Set a key with 10-minute expiration
err := rdb.Set(ctx, "session:123", userDataBytes, 10*time.Minute).Err()

// Get a key
val, err := rdb.Get(ctx, "session:123").Result()
if errors.Is(err, redis.Nil) {
    // Key does not exist
}
```

### C. Read-Through Caching Decorator (`repo_cached.go` in template)

To cache repository reads without polluting your business layer, wrap your database repository using the decorator included in the service template:

```go
// 1. Instantiate pure Postgres repository
pgRepo := booking.NewRepo(pg)

// 2. Wrap with Redis read-through caching decorator (10-minute TTL)
cachedRepo := booking.NewCachedRepo(pgRepo, rdb, 10*time.Minute)

// 3. Pass cached repo to service layer (satisfies same Repository interface)
svc := booking.NewService(core.Logger(), cachedRepo)
```

*Reads check Redis first, falling back to Postgres on cache misses. Mutations write to Postgres and invalidate or update the cached key. If called inside an active `postgres.WithinTx` transaction, it automatically bypasses Redis to ensure Read-Your-Own-Writes consistency.*

### D. Distributed Idempotency Adapter

Use `rdb.IdempotencyStore()` to back the `httpx.Idempotency` middleware with Valkey/Redis across a multi-replica cluster:

```go
r.Group(func(r chi.Router) {
    // Protect mutating routes from duplicate clicks across all pods:
    r.Use(httpx.Idempotency(rdb.IdempotencyStore(), 24*time.Hour))

    r.Post("/orders", h.CreateOrder)
    r.Post("/payments", h.Charge)
})
```

---

## 3. Observability & Metrics

When attached to `app.App`, the client automatically exports connection pool statistics to Prometheus via `telemetry.Meter`:

| Metric Name | Type | Labels | Description |
| :--- | :--- | :--- | :--- |
| `redis_pool_connections` | Gauge | `state="total"` | Total connections in the pool |
| `redis_pool_connections` | Gauge | `state="idle"` | Idle connections ready for use |
| `redis_pool_connections` | Gauge | `state="stale"` | Stale connections waiting to be closed |
| `redis_pool_connections` | Gauge | `state="hits"` | Successful connection pool acquisitions |
| `redis_pool_connections` | Gauge | `state="misses"` | New connections created due to empty pool |
| `redis_pool_connections` | Gauge | `state="timeouts"` | Requests that timed out waiting for a connection |

Every command automatically creates an OpenTelemetry span child to the active HTTP request via `redisotel`.