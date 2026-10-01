# JWT Authentication (`platform/jwt`)

HMAC-SHA256 (HS256) access token signing, validation, and automatic 3-way context enrichment across `context.Context`, structured `slog` logging, and OpenTelemetry distributed tracing.

---

## 1. Configuration (`conf.toml`)

```toml
[jwt]
secret = "your-secret-key-min-32-chars" # [REQUIRED] HMAC-SHA256 signing secret
expiration = "24h"                      # Optional, default: 24h
```

### Environment Variables

Config structs declare relative tags (`env:"SECRET"`). When embedded in a Sling service with the standard `env-prefix:"JWT_"`, they resolve to:

| Config Key | Relative Tag | Standard Service Env (with `env-prefix`) | Default |
| :--- | :--- | :--- | :--- |
| `secret` | `env:"SECRET"` | `JWT_SECRET` | *none (required)* |
| `expiration` | `env:"EXPIRATION"` | `JWT_EXPIRATION` | `24h` |
---

## 2. Usage

### A. Initialization & Route Protection (`cmd/<service>/main.go`)

```go
auth := jwt.NewHS256(cfg.JWT)

r.Route("/v1", func(r chi.Router) {
    // Public routes (no auth required)
    r.Post("/login", h.Login)

    // Protected routes (401 Unauthorized if token is missing or invalid)
    r.Group(func(r chi.Router) {
        r.Use(jwt.RequireAuth(auth))

        r.Get("/profile", h.GetProfile)
        r.Post("/orders", h.CreateOrder)
    })

    // Optional auth routes (proceeds anonymously if token absent; enriches context if present)
    r.Group(func(r chi.Router) {
        r.Use(jwt.OptionalAuth(auth))
        r.Get("/catalog", h.ListCatalog)
    })
})
```

### B. Extracting Identity in Handlers (`handler.go`)

```go
func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {
    // Panics if RequireAuth was not used; returns uuid.UUID
    userID := jwt.MustUserID(r.Context())

    // Or safe extraction:
    // userID, ok := jwt.UserID(r.Context())

    // Context logger and OpenTelemetry span are ALREADY enriched
    // Log will automatically include: user_id=<uuid>
    // Jaeger trace span will automatically include attribute: enduser.id = <uuid>
    logger.Info(r.Context(), "creating order", "amount", 100)

    httpx.JSON(w, http.StatusCreated, map[string]any{"user_id": userID})
}
```

### C. Issuing Access Tokens (`service.go` or `handler.go`)

```go
userID := uuid.NewV7()
tokenString, err := auth.SignAccess(userID)
if err != nil {
    // handle error
}
```

---

## 3. Features

* **Zero-Touch Context Enrichment:** When a valid token is verified, the middleware automatically injects `user_id` into the logger (`slog`) and labels the active OpenTelemetry span with `semconv.EnduserIDKey`.
* **Fail-Fast Error Handling:** Rejects invalid, tampered, or expired tokens immediately with standardized `401 Unauthorized` responses via `httpx.WriteError`.