# Configuration Loader (`platform/config`)

An autonomous, self-healing, and thread-safe configuration loader for Go based on `cleanenv` and `go-toml/v2`.

It is specifically designed for local developer-friendly workflows (Docker volume mounts, automatic templates) while remaining safe and read-only in production environments.

## Features

- **Auto-Healing (Auto-Repair):** Automatically detects missing keys on startup and injects them into the existing file on disk *without* losing user formatting or comments.
- **Auto-Generated Templates:** Automatically writes an up-to-date `config.toml.example` file on every local startup, ensuring your Git-tracked config templates never fall out of sync.
- **Deep Fallback Merging:** Allows sub-configurations (e.g., modular configurations) to inherit missing settings from global/parent blocks on the fly using the `fallback` tag.
- **Docker-Friendly (Inode Preserving):** Rewrites files via direct truncation instead of `os.Rename` when updating files locally, preserving host permissions (no accidental `root:root` chowns) and preventing "device or resource busy" errors in mounted single-file Docker volumes.
- **Required Fields Warning:** Highlights fields marked with `env-required:"true"` with a distinct `[REQUIRED]` comment on disk, forcing developer visibility.
- **Thread-Safe:** Uses strict mutex synchronization, preventing race conditions during parallel test executions.

## Struct Tags

- `toml` — maps structure fields to TOML keys.
- `env` — binds the field to an environment variable name. Module configs declare relative names (`PORT`, `DSN`, `ADDR`).
- `env-prefix` — prepends a namespace prefix to all environment variables inside a nested struct (e.g. `env-prefix:"HTTP_"` turns `env:"PORT"` into `HTTP_PORT`). Prevents collisions when multiple modules or instances (e.g. primary vs. replica DB) are mounted together.
- `env-default` — defines the default value in memory if not present on disk.
- `env-required="true"` — marks a field as mandatory.
- `comment` — writes an inline comment next to the field in the config file.
- `fallback` — references a root-level struct path to pull missing/zero values from if the local block is empty.
## Usage

### 1. Define your Config Structs

```go
type Config struct {
	Redis redis.Config `toml:"redis" env-prefix:"REDIS_"`
	Auth  AuthConfig   `toml:"auth"  env-prefix:"AUTH_"`
}

type AuthConfig struct {
	JWTSecret string       `toml:"jwt_secret" env-required:"true" comment:"JWT signing key"`
	Redis     redis.Config `toml:"redis" fallback:"redis" comment:"Local auth cache (commented out by default, falls back to root redis)"`
}
```

### 2. Load the Configuration

```go
package main

import (
	"fmt"
	"log"

	"github.com/blessed-go/sling/platform/config"
)

func main() {
	// Auto-repair only triggers locally (where APP_ENV is "local" or empty)
	cfg, err := config.Load[Config]("config.toml")
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	fmt.Printf("Database loaded: %s\n", cfg.Auth.JWTSecret)
}
```

### 3. Generated `config.toml` (Initial on-disk state)

```toml
[redis]
db = 0
addr = "localhost:6379"

[auth]
jwt_secret = "" # [REQUIRED] JWT signing key

# [auth.redis] # Optional section
# db = 0 # Defaults to [redis]
# addr = "localhost:6379" # Defaults to [redis]
```

## How It Works Locally

When `Load` is called in a local environment:
1. It parses defaults using reflection.
2. It reads the current TOML file on disk (if present).
3. It performs a recursive diff between the struct defaults and the on-disk file.
4. It injects any missing keys or sections right under their logical parent/sibling headers using smart spacing.
5. Slices of structs or nested configs with a `fallback` tag are safely commented out by default to keep the working config clean while remaining fully documented.