# SLING PLATFORM: ARCHITECTURE & MODULE DIRECTORY

> **Sling** is a lightweight platform chassis (SDK & Starter Kit) written in Go.  
> Philosophy: **"Zero magic, explicit Go-way dependencies, zero overhead, and full infrastructure plumbing out of the box."**

---

## 1. Module Directory (TOC)

Every platform package is self-contained under `platform/`. Detailed usage, configuration parameters, and API examples live in each module's own `README.md`:

| Module | Docs | Role | Description |
| :--- | :--- | :--- | :--- |
| **`platform/config`** | [README](platform/config/README.md) | Baseline | TOML loader, auto-repair, fallback DAG, cleanenv validation |
| **`platform/httpx`** | [README](platform/httpx/README.md) | Baseline | HTTP server, graceful shutdown, error mapping, client, idempotency |
| **`platform/postgres`** | [README](platform/postgres/README.md) | Baseline | PostgreSQL 18 pool, tx manager (Unit of Work), isolated Goose migrations |
| **`platform/redis`** | [README](platform/redis/README.md) | Opt-in | Valkey/Redis client, command tracing, cache decorator, idempotency store |
| **`platform/jwt`** | [README](platform/jwt/README.md) | Opt-in | HMAC-SHA256 auth, context identity (User ID → logger + tracer) |
| **`platform/grpcx`** | [README](platform/grpcx/README.md) | Opt-in | Binary RPC transport, interceptors, `app.Runner` lifecycle integration |
| **`platform/htmx`** | [README](platform/htmx/README.md) | Opt-in | HTMX frontend helpers, safe buffered rendering, client events |
| **`platform/app`** | [README](platform/app/README.md) | Baseline | Central runtime coordinator, staged graceful shutdown (LIFO), runners |
| **`platform/logger`** | [README](platform/logger/README.md) | Baseline | Structured `slog` wrapper, tint formatting, OTel trace/span injection |
| **`platform/telemetry`** | [README](platform/telemetry/README.md) | Baseline | OTel tracer/meter initialization, Prometheus metrics exporter |
| **`platform/ctxerr`** | [README](platform/ctxerr/README.md) | Internal | Request context error slot for access log enrichment |
| **`platform/run`** | [README](platform/run/README.md) | Internal | Concurrency group supervisor with signal management |
| **`platform/system`** | [README](platform/system/README.md) | Internal | Aggregate platform configuration schema |

---

## 2. What Works Out of the Box (Day 0 Baseline)

Every project generated via `sling init <project> [service]` includes the following **by default without manual configuration**:

### Network and Routing (Gateway)
* **Dynamic Caddy Gateway:** Caddy binds port `:80`, matches any route `/api/<service>/*`, dynamically strips the prefix, and proxies traffic via Docker internal DNS to `<service>:8080`.
* **Zero-Touch Configuration:** The `Caddyfile` is identical across projects and does not require manual edits when adding services.
* **Outbound HTTP Client (`httpx.NewClient`):** Preconfigured transport with persistent TCP connection pooling and automatic W3C `traceparent` header injection for distributed tracing across services.

### Database and Migrations (PostgreSQL 18)
* **Docker Label-Driven Provisioning (`postgres-init`):** A lightweight container monitors the Docker socket, discovers the `sling.postgres.init=<name>` label, and idempotently provisions the `<name>_db` database, user credentials, and permissions.  
  *(Note: Docker socket label-scanning is strictly intended for local developer velocity. In production environments, database provisioning is handled by Terraform/IaC or Kubernetes operators without socket mounts).*
* **Goose Migration Isolation (`postgres.Migration`):** Each domain module tracks its migration history in a dedicated table (e.g., `goose_booking`), preventing migration version collisions in modular monoliths.
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

## 4. FAQ & Core Mechanics

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
| `toml:"key"` | Maps struct field to TOML key. |
| `env:"VAR"` | Relative environment variable name (e.g. `PORT`, `DSN`, `ADDR`). |
| `env-prefix:"PREFIX_"` | Namespaces all child env vars when embedding a module config (e.g. `env-prefix:"HTTP_"` + `env:"PORT"` → `HTTP_PORT`). |
| `comment:"text"` | Writes an inline comment next to the key in `conf.toml`. |
| `env-required:"true"` | Appends `[REQUIRED]` and ensures the field is validated. |
| `env-default:"value"` | Populates default value in memory and generates `.example` template files. |
| `fallback:"section"` | Cascades missing values from a parent/global block (e.g., `fallback:"redis"`). |

---

### 4. Environment Variable Namespacing Convention
Modules are independent Lego bricks:
* **Inside modules:** Config structs declare *relative* env tags (`env:"PORT"`, `env:"DSN"`, `env:"ADDR"`). Modules never hardcode their mount prefix.
* **In the service config:** The owning struct explicitly namespaces embedded modules using `env-prefix`:
  ```go
  type Config struct {
      HTTP     httpx.Config    `toml:"http"     env-prefix:"HTTP_"`
      GRPC     grpcx.Config    `toml:"grpc"     env-prefix:"GRPC_"`
      Postgres postgres.Config `toml:"postgres" env-prefix:"POSTGRES_"`
      Redis    redis.Config    `toml:"redis"    env-prefix:"REDIS_"`
      App      app.Config      `toml:"app"`     // Process-level globals: APP_ENV, SERVICE_NAME
  }
  ```
* **Multi-Instance Support:** Running two databases or caches requires zero hacks:
  ```go
  PrimaryDB postgres.Config `toml:"primary_db" env-prefix:"PRIMARY_DB_"`
  ReplicaDB postgres.Config `toml:"replica_db" env-prefix:"REPLICA_DB_"`
  ```
  Generates `PRIMARY_DB_DSN` and `REPLICA_DB_DSN` cleanly without port/variable collisions.

---

### 5. Contextual Logging Rule: "No Context, No Traces"
* **Rule:** Always log using a context:
  ```go
  logger.Info(ctx, "user registered", "email", email)
  // or
  log.InfoContext(ctx, "user registered", "email", email)
  ```
  Only by passing `ctx` will the log record automatically attach `trace_id`, `span_id`, and `user_id`. Uncontextualized logging is considered an anti-pattern in Sling.

---

### 6. Port Allocation Matrix
| Component | Host Port | Docker Internal Port | Purpose |
| :--- | :--- | :--- | :--- |
| **Caddy Gateway** | `:80` | `:80` | Ingress gateway (`http://localhost/api/<service>/...`) |
| **Go Services** | *unexposed* | `:8080` | Accessible externally exclusively through Caddy |
| **PostgreSQL** | `:5432` | `:5432` | Database access for DataGrip / DBeaver |
| **Redis** | `:6379` | `:6379` | Cache access for RedisInsight / redis-cli |
| **Prometheus** | `:9090` | `:9090` | Prometheus UI and metrics scrape endpoints |
| **Jaeger UI** | `:16686` | `:16686` | Distributed tracing web interface |
| **Grafana** | `:3000` | `:3000` | Observability dashboards (default credentials: `admin/admin`) |
