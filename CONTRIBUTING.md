# Contributing to Sling

Thank you for contributing to Sling! This guide details how to develop the Sling platform locally, test changes in real-time, and contribute back cleanly.

---

## Development Workflows

Sling development is organized into two primary workflows:

### Scenario A: Developing the Sling Framework (for me)

When developing features in `platform/` or templates in `template/`, you want fast feedback without repeatedly running `go install` or publishing dummy tags.

#### 1. Live Template Editing

The Sling CLI binary embeds templates via `//go:embed` by default. To edit template files and immediately see the effect in scaffolding commands:

```bash
export SLING_TEMPLATE_DIR=$(pwd)/template
```

When `SLING_TEMPLATE_DIR` is set, `sling` reads template files directly from your local filesystem using `os.DirFS` instead of the compiled-in `embed.FS`.

#### 2. Linking Sling to a Test Project (`sling link`)

To test changes in `platform/` against a real running microservice stack:

```bash
# 1. Initialize a test project in /tmp
cd /tmp
sling init testproject billing
cd testproject

# 2. Link the project to your local Sling checkout
task link-sling -- /path/to/your/sling
```

**What happens under the hood:**
- **On the host:** Generates a root `go.work` file referencing your local Sling directory. Your IDE (GoLand, VSCode) immediately indexes local Sling platform source files.
- **In Docker containers:** Generates `deployment/go.work.docker` and `deployment/docker-compose.override.yaml` (which are git-ignored). These mount your local Sling checkout into `/app/sling` inside each container and configure `GOWORK=/app/go.work`.
- **Live Reload:** Changes to `platform/` files immediately trigger live recompilation via `Air` inside the Docker containers!

#### 3. Unlinking After Testing

Once verification is complete:

```bash
task unlink-sling
```

This cleans up `go.work`, `deployment/go.work.docker`, and `deployment/docker-compose.override.yaml`, restoring the clean standalone project structure.

---

### Scenario B: Developing Services Inside a Generated Project

For developers building applications with Sling, all workflows are orchestrated through `Taskfile.yml`:

| Command | Action |
|---|---|
| `task up` | Boots PostgreSQL 18, Redis, dynamic Caddy gateway, Jaeger, Prometheus, Grafana, Loki, applies migrations, and runs services with Air hot reload. |
| `task down` | Stops and removes all containers for the project. |
| `task logs` | Streams real-time container logs. |
| `task add -- <service>` | Scaffolds a new microservice (`internal/<service>`, `cmd/<service>`, compose fragment, and tests). |
| `task add -- <service>/<subdomain>` | Adds a domain module to an existing service (modular monolith). |
| `task tidy` | Runs `go mod tidy` (and `go work sync` if linked to local Sling). |
| `task test:e2e` | Runs Bruno end-to-end integration tests inside the Docker network. |

---

## Testing and Quality Standards

Before submitting a Pull Request, verify that all automated checks pass:

```bash
# Run unit & integration tests with race detector
go test -v -race -count=1 ./...

# Verify code formatting and linting
go vet ./...

# Verify standalone compilation without go.work
GOWORK=off go test ./...
GOWORK=off go build ./cmd/sling

# Run CLI smoke tests
go test -v -run TestProjectLifecycleSmoke ./cmd/sling
```

---

## Architectural Conventions

1. **Zero Magic:** Keep dependencies explicit. Avoid hidden global state or reflection magic where plain Go interfaces suffice.
2. **Cardinality Protection:** Always parameterize metrics and span names (`span.SetName(r.Method + " " + route)`). Never inject raw IDs into metric dimensions or trace operation names.
3. **Staged Teardown (LIFO):** Always release ingress traffic before closing storage handles or flushing telemetry.
4. **Soft IDs Across Domains:** Never create cross-domain physical database foreign keys. Use scalar `UUID` references.
