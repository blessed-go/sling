# SLING PLATFORM: CAPABILITIES, BATTERIES & ROADMAP

> **Sling** is a lightweight platform chassis (SDK & Starter Kit) written in Go.  
> Philosophy: **"Zero magic, explicit Go-way dependencies, zero overhead, and full infrastructure plumbing out of the box."**

---

## 1. What Works Out of the Box (Day 0 Baseline)

Every project generated via `sling init <project> [service]` includes the following **by default without manual configuration**:

### Network and Routing (Gateway)
* **Dynamic Caddy Gateway:** No verbose reverse-proxy label sheets. Caddy binds port `:80`, matches any route `/api/<service>/*`, dynamically strips the prefix, and proxies traffic via Docker internal DNS to `<service>:8080`.
* **Zero-Touch Configuration:** The `Caddyfile` is identical across projects and does not require manual edits when adding services #2, #5, or #50.
* **Outbound HTTP Client (`httpx.NewClient`):** Preconfigured transport with persistent TCP connection pooling and automatic W3C `traceparent` header injection for distributed tracing across services.

### Database and Migrations (PostgreSQL 18)
* **Docker Label-Driven Provisioning (`postgres-init`):** No manual database provisioning scripts. A lightweight container monitors the Docker socket, discovers the `sling.postgres.init=<name>` label, and idempotently provisions the `<name>_db` database, user credentials, and permissions.  
  *(Note: Docker socket label-scanning is strictly intended for local developer velocity. In production environments, database provisioning is handled by Terraform/IaC or Kubernetes operators without socket mounts).*
* **Goose Migration Isolation (`postgres.Migration`):** Each domain module tracks its migration history in a dedicated table (e.g., `goose_booking`, `goose_billing`), preventing migration version collisions in modular monoliths. When adding subdomains via `sling add <service>/<subdomain>`, the CLI automatically wires the subdomain migration into the parent service's runner slice in `cmd/migrate/main.go`.
* **Startup Race Condition Prevention:** Migration runners depend strictly on database initialization completion (`condition: service_completed_successfully`).
* **Primary Key Standard:** Native `UUIDv7` (time-ordered, right-append-friendly for B-Tree indexes, preventing disk fragmentation).

### Complete Observability Triumvirate
* **Three-in-One Instrumentation (`httpx.Observability`):** A single middleware per request measures elapsed time once and emits an OpenTelemetry span, a structured `slog` record correlated with `trace_id` and `span_id`, and Prometheus latency/traffic metrics.
* **Prometheus Cardinality Protection:** Router metrics group requests strictly by parameterized route templates (`/items/{id}`), preventing high-cardinality metric explosion from raw URLs.
* **Docker Service Discovery in Prometheus:** Prometheus automatically discovers scrape targets through the Docker socket using `prometheus.scrape=true` labels without modifying `prometheus.yml`.
* **Grafana Cross-Navigation (Trace ⇄ Logs):** Clicking a `trace_id` in Loki logs directly opens the corresponding Jaeger trace waterfall. Inside Jaeger, the "Logs for this span" shortcut displays matching logs for that time window.

### Configuration and Lifecycle (`app` & `config`)
* **Self-Healing Configuration (TOML Auto-Repair):** Adding new fields to Go configuration structs causes the loader to automatically inject missing keys with descriptions into the local file on disk without corrupting existing formatting.
* **Docker Inode Preservation:** Local file updates execute via `truncate + write`, maintaining inode stability and preventing `EBUSY` errors when single files are mounted into Docker containers.
* **Staged Graceful Shutdown Waterfall:**
  * *Phase 1:* Stop HTTP and gRPC ingress traffic (`run.Group`) while allowing active requests to complete.
  * *Phase 2:* LIFO closing of databases, caches, and queues when writes have ceased.
  * *Phase 3:* Flush telemetry buffers using a fresh `context.Background()`.
* **Periodic Background Workers (`AttachPeriodic`):** Run recurring background tasks with built-in panic recovery and immediate responsiveness to shutdown signals.
* **Deterministic Error Mapping (`httpx.MapErrors`):** Traverses nested error chains from outermost to root; domain-specific errors take priority over generic transport wrappers.

---

## 2. Ready-to-Use Batteries (Opt-in Platform Modules)

These modules are **implemented, tested, and included in the platform repository**. They are decoupled from the default skeleton and can be enabled in seconds.

### Cache and State (`platform/redis`)
* **Under the Hood:** Built on `go-redis/v9` with method embedding, hardware command tracing via `redisotel`, pool metrics (`PoolStats`) exported to Prometheus, and connectivity verification on startup.
* **How to Enable in a Service:**
  1. In `docker-compose.yaml`, uncomment or enable `valkey`.
  2. In `internal/<service>/config.go`: add `Redis redis.Config toml:"redis"`.
  3. In `cmd/<service>/main.go`:
     ```go
     rdb, err := redis.New(ctx, cfg.Redis)
     core.FatalIf(err, "redis connection failed")
     core.Attach("redis", rdb) // Registers auto-readyz probe and LIFO teardown in Phase 2
     ```
* **Read-Through Decorator Pattern (`repo_cached.go`):** The service template includes a decorator that embeds `Repository`. Wrapping `repo = domain.NewCachedRepo(repo, rdb, 10*time.Minute)` directs read methods through Redis, invalidates keys on writes via `Del`, and delegates unhandled methods to Postgres.

### Security and Authentication (`platform/jwt`)
* **Under the Hood:** HMAC-SHA256 (HS256) token issuance and verification with expiration enforcement.
* **Triple Context Enrichment:**
  * `jwt.UserID(ctx)` attaches the user UUID to the context.
  * Contextual logger enrichment: all subsequent `logger.InfoContext` calls include `user_id=<UUID>`.
  * OpenTelemetry span enrichment: the active Jaeger span receives `enduser.id = <UUID>`.
* **How to Enable in a Service:**
  1. In `internal/<service>/config.go`: add `JWT jwt.Config toml:"jwt"`.
  2. In `cmd/<service>/main.go`:
     ```go
     auth := jwt.NewHS256(cfg.JWT)

     r.Group(func(r chi.Router) {
         r.Use(jwt.RequireAuth(auth)) // Enforces 401 Unauthorized when token is absent or invalid
         r.Post("/orders", h.CreateOrder)
     })
     ```
  3. In handlers: `userID := jwt.MustUserID(r.Context())`.

---

## 3. System Invariants Cheatsheet (Superpowers & Pitfalls)

### 5 Architectural Superpowers
1. **Never edit `conf.toml` templates manually:** Add fields directly to your Go struct; the loader writes missing keys and comments on startup in local environments.
2. **Leverage `fallback:"..."`:** If a microservice requires Redis or Postgres settings, specify `fallback:"redis"` to inherit configuration from root blocks without duplication.
3. **Trace correlation in logs:** In Grafana, `trace_id` values in Loki logs link directly to their corresponding Jaeger traces.
4. **Avoid container proliferation:** Use `task new-domain -- <name>` to add business domains into existing binaries and databases (modular monolith architecture).
5. **In-memory route testing in 50ms:** Use `app_test.go` patterns with `DefaultRouter()` to validate endpoints without spinning up Docker or binding external ports.

### 5 Critical Pitfalls to Avoid
1. **Gateway path prefixes:** Clients invoke `http://localhost/api/booking/v1/items`, but the Go router defines `/v1/items` because Caddy strips the `/api/booking` prefix.
2. **No cross-domain `FOREIGN KEY` constraints:** Cross-domain physical `REFERENCES` are prohibited. Use scalar `UUID` identifiers (Soft IDs) to decouple services.
3. **Never run one-shot tasks in `AttachRunner`:** Runners must be long-running supervisor loops. Returning `nil` while context is active signals an unexpected exit and triggers shutdown.
4. **Never close database handles manually:** Handlers and runners must not invoke `db.Close()`. Teardown is managed centrally during Phase 2 shutdown.
5. **Avoid raw `http.Get` calls:** Execute outbound network calls using `httpx.NewClient()` with context propagation to maintain trace continuity.

---

## 4. Platform Architecture Roadmap (v0.1.x -> v0.2.0)

This section outlines the architectural backlog:

```text
       Sprint 3: Transactions & Consistency
       ├── pg.WithinTx (Unit of Work without leaking driver types to domain)
       └── Hardware Cache Bypass (postgres.HasTx protection in cache decorator)
              │
              ▼
       Sprint 4: Asynchronous Events & Bus
       ├── platform/nats (JetStream + W3C traceparent header propagation)
       └── Transactional Outbox + Inbox (SKIP LOCKED + ON CONFLICT idempotency)
              │
              ▼
       Sprint 5: Network Reliability
       ├── HTTP Idempotency-Key middleware on Redis (Stripe pattern)
       └── platform/grpcx (Symmetric gRPC client/server with interceptors)
              │
              ▼
       Sprint 6: Testing Framework
       └── platform/testx (Log assertion utilities, ephemeral test DBs)
```

### Module Specifications:

#### 1. Database Transaction Manager (`pg.WithinTx`)
* **Purpose:** Execute multi-repository operations inside a single atomic transaction without leaking `pgx.Tx` into business logic.
* **Contract:** Domain defines `TxManager { WithinTx(ctx, fn) error }`. The platform passes down a transactional context `txCtx`.
* **Guardrail:** The `postgres.HasTx(ctx)` utility allows caching decorators to bypass Redis reads inside active transactions to ensure read-your-own-writes consistency.

#### 2. Asynchronous Event Bus (`platform/nats`)
* **Purpose:** Loosely coupled asynchronous communication across microservices.
* **Contract:** JetStream Streams and Durable Consumers.
* **Invariant:** Automatic serialization and deserialization of W3C `traceparent` headers into `nats.Msg.Header`.
* **Lifecycle:** Invocations of `sub.Drain()` during Phase 1 shutdown to finish processing buffered messages.

#### 3. Transactional Outbox + Inbox
* **Purpose:** Guarantee reliable at-least-once message delivery without dual-write issues and deduplicate incoming messages.
* **Outbox:** Write domain events to `outbox_events` inside the business transaction and poll via a background worker using `SELECT ... FOR UPDATE SKIP LOCKED`.
* **Inbox:** Deduplicate received consumer events through `inbox_events` using `ON CONFLICT DO NOTHING`.

#### 4. HTTP Idempotency Middleware (`Idempotency-Key`)
* **Purpose:** Protect mutating and financial endpoints from repeated submissions and mobile network retries.
* **Contract:** Middleware intercepts `Idempotency-Key`, acquires a distributed lock in Redis, and on duplicate requests returns the cached response status (`200/201`) with header `Idempotent-Replayed: true` without querying backend databases.

#### 5. Binary RPC Transport (`platform/grpcx`)
* **Purpose:** High-performance inter-service communication.
* **Contract:** Full symmetry with `httpx`: interceptor chain (OpenTelemetry, latency logging via `slog`, metrics, panic recovery, domain error mapping via `ctxerr` to gRPC status codes). Implements `app.Runner`.

---

## 5. FAQ & Core Mechanics

### 1. "Why does the API mask database errors?" (5xx Sanitization)
* **Context:** When Postgres returns an error (such as a unique constraint violation) and the handler passes it to `httpx.WriteError(w, r, err)`, the HTTP client receives:  
  `{"error": "internal error"}`.  
* **Architecture Rationale:**  
  > **Security Policy:** Any unmapped error or error resulting in status $\ge 500$ is **intentionally masked** to `"internal error"` to prevent database DSNs, internal table names, and stack traces from leaking to public networks.  
  > The underlying error is **never lost**—it is recorded in the OpenTelemetry Jaeger span and structured Loki logs. To expose a friendly error message to users, map the error to a 4xx status using `httpx.MapErrors`.

---

### 2. "How do I apply a new migration?"
* **Goose Migration Procedure:**
  1. Add the migration file strictly under `internal/<domain>/migrations/`.
  2. Name the file with a timestamp prefix: `YYYYMMDDHHMMSS_add_column.sql`.
  3. Include Goose annotations:
     ```sql
     -- +goose Up
     ALTER TABLE items ADD COLUMN description TEXT;
     -- +goose Down
     ALTER TABLE items DROP COLUMN description;
     ```
  4. No manual Go registry modifications are needed—the `//go:embed migrations/*.sql` directive embeds and applies new migrations automatically.

---

### 3. Struct Tag Reference
| Struct Tag | Platform Behavior |
| :--- | :--- |
| `comment:"text"` | Writes an inline comment next to the key in `conf.toml`. |
| `env-required:"true"` | Appends `[REQUIRED]` and ensures the field is validated. |
| `env-default:"value"` | Populates default value in memory and generates `.example` template files. |
| `fallback:"section"` | Cascades missing values from a parent/global block (e.g., `fallback:"redis"`). |

---

### 4. Contextual Logging Rule: "No Context, No Traces"
* **Rule:** Always log using a context:
  ```go
  logger.Info(ctx, "user registered", "email", email)
  // or
  log.InfoContext(ctx, "user registered", "email", email)
  ```
  Only by passing `ctx` will the log record automatically attach `trace_id`, `span_id`, and `user_id`. Uncontextualized logging is considered an anti-pattern in Sling.

---

### 5. Port Allocation Matrix
| Component | Host Port | Docker Internal Port | Purpose |
| :--- | :--- | :--- | :--- |
| **Caddy Gateway** | `:80` | `:80` | Ingress gateway (`http://localhost/api/<service>/...`) |
| **Go Services** | *unexposed* | `:8080` | Accessible externally exclusively through Caddy |
| **PostgreSQL** | `:5432` | `:5432` | Database access for DataGrip / DBeaver |
| **Redis** | `:6379` | `:6379` | Cache access for RedisInsight / redis-cli |
| **Prometheus** | `:9090` | `:9090` | Prometheus UI and metrics scrape endpoints |
| **Jaeger UI** | `:16686` | `:16686` | Distributed tracing web interface |
| **Grafana** | `:3000` | `:3000` | Observability dashboards (default credentials: `admin/admin`) |