# Sling

[![Go Version](https://img.shields.io/badge/Go-1.27%2B-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

> **Sling** is a lightweight, production-ready Go platform chassis and starter kit for microservices and modular monoliths.  
> **Philosophy:** Zero magic, explicit Go-way dependencies, zero overhead, and full infrastructure plumbing out of the box.

---

## Key Highlights

- **Dynamic Caddy Gateway:** Zero-touch reverse proxy. Matches `/api/<service>/*`, automatically strips the prefix, and routes internally to `<service>:8080` without label sprawl.
- **PostgreSQL 18 & Isolated Migrations:** Automatic database and credential provisioning via Docker labels (`sling.postgres.init`). Goose migrations isolated per domain table (e.g. `goose_billing`).
- **3-in-1 Observability:** A single lightweight middleware emits OpenTelemetry traces, route-parameterized Prometheus metrics (cardinality protected), and structured `slog` entries with correlated `trace_id`.
- **Self-Healing Config:** Struct-driven TOML configuration with auto-repair on startup in local environments. Preserves file inodes for seamless Docker mounts.
- **Graceful Teardown Waterfall:** Staged 3-phase shutdown: stops ingress HTTP traffic (`run.Group`), closes databases/caches in reverse registration order (LIFO), and flushes telemetry buffers.

> [!NOTE]
> **Security Notice (Local Dev vs Production):** The label-based PostgreSQL provisioner (`postgres-init`) and Docker Socket discovery in Prometheus/Promtail are developer conveniences designed specifically for zero-touch local development. In production, databases and credentials should be provisioned via Infrastructure-as-Code (Terraform, Kubernetes Operators, managed DBs) without mounting the Docker socket.

---

## 🚀 Quick Start

### 1. Install Sling CLI

```bash
go install github.com/blessed-go/sling/cmd/sling@latest
```

### 2. Initialize a New Project

```bash
# Initialize a project with an initial 'billing' microservice
sling init myproject billing
cd myproject
```

### 3. Add Services or Domains

```bash
# Add another standalone microservice
sling add orders

# Add a domain module inside an existing service (modular monolith)
sling add billing/analytics
```

### 4. Run Locally

```bash
# Boot the local stack (Postgres, Caddy, Prometheus, Grafana, Jaeger, Loki, Migrations, and Air hot reload)
task up

# Run end-to-end Bruno tests
task test:e2e
```

---

## CLI Commands

| Command | Description |
|---|---|
| `sling init <project> [service]` | Scaffolds a new project root with docker compose, observability stack, and optional first service |
| `sling add <service>` | Creates a new deployable microservice (`internal/<service>`, `cmd/<service>`, compose fragment, and tests) |
| `sling add <service>/<domain>` | Adds a domain module to an existing service without spinning up new containers |
| `sling link <path-to-sling>` | Links local Sling checkout via `go.work` and docker override for platform development |
| `sling unlink` | Removes local development overrides, restoring standalone project config |

---

## Ready-to-Use

Sling includes pre-tested platform components:
- **`platform/app`**: Central runtime coordinator for HTTP/gRPC servers, background runners, and graceful teardown.
- **`platform/config`**: TOML configuration loader with `fallback:"..."` tags and disk auto-repair.
- **`platform/httpx`**: HTTP helpers, error mapping, observability middleware, and persistent outbound HTTP client.
- **`platform/postgres`**: `pgx/v5` connection pool with OTel tracing, Prometheus metrics, and Goose migrations.
- **`platform/redis`**: `go-redis/v9` client with OTel hooks, Prometheus pool stats, and read-through caching decorator.
- **`platform/jwt`**: HMAC-SHA256 token issuance and middleware with automatic logger and trace context enrichment.

For deep architectural patterns, design principles, and recipes, see [PLATFORM.md](PLATFORM.md).

---

## 📄 License

Sling is open-source software licensed under the [MIT License](LICENSE).
