# HTTP Toolkit: Server, Client, Errors & Idempotency (`platform/httpx`)

Out-of-the-box HTTP plumbing for services: server bootstrap with graceful shutdown, one-pass observability middleware (OpenTelemetry + Prometheus + `slog`), deterministic error-to-status mapping with 5xx sanitization, a preconfigured outbound client with trace propagation, and an `Idempotency-Key` middleware (Stripe pattern) with pluggable stores.

---

## 1. Configuration (`conf.toml`)

```toml
[http]
port = "8080"      # Listen port (default: 8080)
read_timeout = "5s"   # Maximum duration to read the full request (default: 5s)
write_timeout = "10s" # Maximum duration before timing out writes of the response (default: 10s)
idle_timeout = "15s"  # Maximum keep-alive idle connection time (default: 15s)
```

### Environment Variables

Config structs declare relative tags (`env:"PORT"`). When embedded in a Sling service with the standard `env-prefix:"HTTP_"`, they resolve to:

| Config Key | Relative Tag | Standard Service Env (with `env-prefix`) | Default |
| :--- | :--- | :--- | :--- |
| `port` | `env:"PORT"` | `HTTP_PORT` | `8080` |
| `read_timeout` | `env:"READ_TIMEOUT"` | `HTTP_READ_TIMEOUT` | `5s` |
| `write_timeout` | `env:"WRITE_TIMEOUT"` | `HTTP_WRITE_TIMEOUT` | `10s` |
| `idle_timeout` | `env:"IDLE_TIMEOUT"` | `HTTP_IDLE_TIMEOUT` | `15s` |
---

## 2. Usage

### A. Server Bootstrap & Lifecycle (`cmd/<service>/main.go`)

`httpx.Config` is embedded into the service config; `core.ServeHTTP` registers the server with the application supervisor, and `core.Run` handles Phase 1 ingress shutdown (active requests drain with a 5s grace window before a forced close):

```go
type Config struct {
    Postgres postgres.Config `toml:"postgres" env-prefix:"POSTGRES_"`
    HTTP     httpx.Config    `toml:"http"     env-prefix:"HTTP_"`
    App      app.Config      `toml:"app"`
}

// In cmd/<service>/main.go:
r := core.DefaultRouter()
r.Route("/v1", func(r chi.Router) {
    r.Use(httpx.MapErrors(__SERVICE__.ErrorMapping))
    r.Get("/items/{id}", h.GetItem)
    r.Post("/items", h.CreateItem)
})

core.ServeHTTP(cfg.HTTP, r) // Accepts a port string, httpx.Config, or *httpx.Config
```

`core.DefaultRouter()` already wires `httpx.Observability`, a chi `Recoverer`, and the system routes (`/healthz`, `/readyz`, `/metrics`). For a standalone server without `app.App`, `httpx.Serve(ctx, log, cfg, handler)` starts a blocking `http.Server` with the same graceful shutdown semantics.

### B. Handlers: JSON Responses & Deterministic Error Mapping (`handler.go`)

* `httpx.JSON(w, status, data)` — serializes `data` with `Content-Type: application/json`.
* `httpx.Error{Status, Msg}` — a transport error that always wins over any mapping.
* `httpx.BadRequest(w, r, msg)` / `httpx.NotFound(w, r, msg)` — status shortcuts.
* `httpx.WriteError(w, r, err)` — the standard exit for handler failures:

```go
// Domain mapping (internal/<service>/errors.go):
var ErrorMapping = map[error]int{
    ErrItemNotFound:  http.StatusNotFound,
    ErrTitleRequired: http.StatusBadRequest,
}

// Handler (internal/<service>/handler.go):
func (h *Handler) GetItem(w http.ResponseWriter, r *http.Request) {
    item, err := h.svc.GetItem(r.Context(), id)
    if err != nil {
        httpx.WriteError(w, r, err) // Maps to 404 {"error": "item not found"}
        return
    }
    httpx.JSON(w, http.StatusOK, ItemResponse{...})
}
```

* **Chain Traversal:** `WriteError` walks the entire error chain (outermost to root, including `errors.Join` multi-errors and `Is` matchers), so a deep `domain.ErrItemNotFound` wrapped in generic/context errors (e.g. `fmt.Errorf("failed to load item: %w", err)`) still resolves to 404.
* **5xx Sanitization:** Any unmapped or 5xx-status error is masked to `{"error": "internal error"}`. The underlying error is never lost—it is recorded in the OpenTelemetry span and in the structured access log (see Section 3).
* **Span Enrichment:** 5xx responses set the active span status to `Error` and attach the root cause via `RecordError`.

### C. Outbound HTTP Client (Inter-Service Calls)

`httpx.NewClient` returns an `*http.Client` with persistent connection pooling, HTTP/2, and automatic W3C `traceparent` propagation—outbound calls continue the distributed trace into the downstream service:

```go
// Instantiate once at startup to share the persistent TCP pool across handlers:
client := httpx.NewClient() // Default 10s timeout; options: WithClientTimeout(d), WithCustomTransport(rt)

// GET
resp, err := client.Get(ctx, "http://billing:8080/v1/invoices")
core.FatalIf(err, "billing call failed")
defer resp.Body.Close()

// POST JSON
resp, err = client.PostJSON(ctx, "http://billing:8080/v1/invoices", billing.CreateInvoiceRequest{...})
```

* **Transport Tuning:** Default transport uses 100 max idle connections (10 per host), 90s idle-connection timeout, 30s TCP keep-alive, and 5s dial/TLS-handshake timeouts.
* **Anti-pattern:** Never use raw `http.Get` in handlers—context propagation and trace continuity are only preserved through `httpx.NewClient`.

### D. Idempotency-Key Middleware (Mutating Endpoints)

Protects `POST`/mutating endpoints from duplicate submissions and client retries (the Stripe pattern). Requests carrying the `Idempotency-Key` header are deduplicated; requests without it pass through untouched:

```go
// Store selection:
mem := httpx.NewMemoryStore()        // Single instance / tests only
store := rdb.IdempotencyStore()      // Redis/Valkey-backed, shared across instances (platform/redis)

// Middleware (wired on the route group that owns mutating endpoints):
r.Route("/v1", func(r chi.Router) {
    r.Use(httpx.Idempotency(store, 24*time.Hour,
        httpx.WithKeyPrefix("billing:"),  // Namespaces keys per service (default: "idempotency:")
        httpx.WithLockTTL(30*time.Second), // In-flight lock TTL (default: 30s)
    ))
    r.Post("/orders", h.CreateOrder)
})
```

* **First Request:** Acquires an in-flight lock, executes the handler through a buffered writer, and stores the completed response (status + headers + body) for `responseTTL` (default 24h).
* **Duplicate While In-Flight:** Concurrent requests with the same key receive `409 Conflict` `{"error": "request in progress"}`—never a second execution.
* **Replay After Completion:** Cached responses are returned verbatim with the `Idempotent-Replayed: true` response header, without touching backend databases.
* **Failure Safety:** 5xx responses and panics release the key, so a retry re-executes instead of replaying a failure.

*Note: `NewMemoryStore` is process-local—adequate for tests and single-replica deployments, but multi-replica services must use the Redis adapter or duplicates will execute once per instance.*

---

## 3. Observability & Metrics

`httpx.Observability` (preconfigured in `core.DefaultRouter()`) measures each request **once** and emits three correlated artifacts: an OpenTelemetry server span, Prometheus metrics, and a structured `slog` record with `trace_id`/`span_id` attached.

| Metric Name | Type | Labels | Description |
| :--- | :--- | :--- | :--- |
| `http_server_request_duration_seconds` | Histogram | `service`, `method`, `route`, `status` | Request latency distribution (buckets: 5ms…10s) |
| `http_server_requests_total` | Counter | `service`, `method`, `route`, `status` | Total requests processed |

* **Cardinality Protection:** The `route` label uses the chi route template (e.g. `/items/{id}`), so metrics group by parameterized pattern instead of raw URL.
* **Span Naming:** The server span is renamed to `METHOD /items/{id}` and carries `http.route`, `http.response.status_code`, and `url.path` attributes.
* **Access Log:** Every request logs `request completed` with `method`, `path`, `route`, `status`, and `duration`. Log level scales with status: `4xx` → WARN, `5xx` → ERROR (plus the root-cause error from the `ctxerr` slot).
* **Trace Propagation:** Incoming `traceparent` headers are extracted; the no-op tracer is warned about once at startup if no exporter is configured.
