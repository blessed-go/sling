### 1. Configuration Struct in Go (`config.go`)

This example demonstrates how to combine tags for various real-world scenarios:

```go
package main

import (
	"time"
)

// [1] Embedded struct: fields are promoted to the root level
type NetworkConfig struct {
	ReadTimeout  time.Duration `toml:"read_timeout" env-default:"5s" comment:"Global read timeout"`
	WriteTimeout time.Duration `toml:"write_timeout" env-default:"10s"`
}

// [2] Database configuration template for testing chains and merges
type DBConfig struct {
	Host     string `toml:"host" env-default:"localhost" comment:"Database host"`
	Port     int    `toml:"port" env-default:"5432" comment:"Database port"`
	Username string `toml:"username" env-default:"postgres"`
	PoolSize int    `toml:"pool_size" env-default:"10"`
}

// [3] Optional integration section
type TracingConfig struct {
	Endpoint   string  `toml:"endpoint" env-default:"http://jaeger:14268"`
	SampleRate float64 `toml:"sample_rate" env-default:"0.5" comment:"Sampling rate (0.0 to 1.0)"`
}

// [4] Root configuration with all tag combinations
type AppConfig struct {
	// [Option A] Anonymous embedding: read_timeout and write_timeout are promoted to root
	NetworkConfig

	// [Option B] Primitive types with defaults and comments
	AppName string `toml:"app_name" env-default:"super-service" comment:"Application display name"`
	Debug   bool   `toml:"debug" env-default:"false" comment:"Enable verbose logging"`

	// [Option C] Primitive slices (numeric strings remain strings, not converted to int64)
	AllowedRoles []string `toml:"allowed_roles" env-default:"admin, 12, editor" comment:"User roles allowed to login"`
	ListenPorts  []int    `toml:"listen_ports" env-default:"8080, 8081" comment:"Public and internal ports"`

	// [Option D] Required field (env-required):
	// Auto-repair will NOT generate a dummy value, allowing cleanenv to validate it
	SecretKey string `toml:"secret_key" env-required:"true" comment:"[REQUIRED] Application HMAC secret token"`

	// [Option E] Ignored field (excluded from TOML and .example)
	RuntimeCache string `toml:"-"`

	// [Option F] Scalar fallback to an embedded (promoted) top-level field:
	// If AdminTimeout is omitted, it falls back to read_timeout
	AdminTimeout time.Duration `toml:"admin_timeout" fallback:"read_timeout" comment:"Inherited from NetworkConfig.read_timeout"`

	// [Option G] Fallback chain (DAG): ReplicaDB -> PrimaryDB -> GlobalDB
	// Note: struct field declaration order does not matter!
	ReplicaDB DBConfig `toml:"replica_db" fallback:"primary_db" comment:"Replica (inherits from Primary)"`
	PrimaryDB DBConfig `toml:"primary_db" fallback:"global_db" comment:"Primary (inherits from Global)"`
	GlobalDB  DBConfig `toml:"global_db" comment:"Root baseline database parameters"`

	// [Option H] Optional section via nil pointer:
	// If this section is omitted from TOML, the field remains nil (tracing disabled)
	Tracing *TracingConfig `toml:"tracing" comment:"Optional distributed tracing integration"`
}
```

---

### 2. Developer's Initial File `config.toml` (Before First Run)

Suppose a developer creates a minimal local configuration overriding only what they need, along with custom comments:

```toml
# === MY LOCAL DEV ENVIRONMENT ===
# Keep this line, testing custom ports

app_name = "my-custom-dev-service" # local name

secret_key = "super-secret-local-jwt"

[global_db]
host = "postgres.internal.company"
pool_size = 50

[primary_db]
# Override host only; port, username, and pool_size should inherit from global_db!
host = "pg-master-01.internal"

[replica_db]
# Host is overridden, while the rest should cascade from primary_db (and global_db)!
host = "pg-replica-01.internal"
```

---

### 3. Application Entrypoint (`main.go`)

```go
package main

import (
	"fmt"
	"log"
	"os"

	"your_project/internal/config"
)

func main() {
	// Enable logging to os.Stdout to observe auto-repair actions
	cfg, err := config.Load[AppConfig]("config.toml", config.WithLogger(os.Stdout))
	if err != nil {
		log.Fatalf("Init failed: %v", err)
	}

	fmt.Printf("AppName: %s\n", cfg.AppName)
	fmt.Printf("ReadTimeout (Embedded): %v\n", cfg.ReadTimeout)
	fmt.Printf("AdminTimeout (Fallback): %v\n", cfg.AdminTimeout)
	fmt.Printf("Roles: %v\n", cfg.AllowedRoles)

	fmt.Printf("\n[GlobalDB]  Host: %s, Port: %d, Pool: %d\n", 
		cfg.GlobalDB.Host, cfg.GlobalDB.Port, cfg.GlobalDB.PoolSize)
	fmt.Printf("[PrimaryDB] Host: %s, Port: %d, Pool: %d\n", 
		cfg.PrimaryDB.Host, cfg.PrimaryDB.Port, cfg.PrimaryDB.PoolSize)
	fmt.Printf("[ReplicaDB] Host: %s, Port: %d, Pool: %d\n", 
		cfg.ReplicaDB.Host, cfg.ReplicaDB.Port, cfg.ReplicaDB.PoolSize)

	if cfg.Tracing == nil {
		fmt.Println("\nTracing: DISABLED (nil pointer preserved)")
	}
}
```

---

### 4. What Happens to `config.toml` on Disk

The AST editor safely injects missing fields:
* All manual developer comments and inline notes **remain intact**.
* The `[tracing]` section is **not force-created** (remains optional).
* The `[replica_db]` section receives missing keys.

```toml
# === MY LOCAL DEV ENVIRONMENT ===
# Keep this line, testing custom ports

app_name = "my-custom-dev-service" # local name

secret_key = "super-secret-local-jwt"
# Global read timeout
read_timeout = '5s'
write_timeout = '10s'
# Enable verbose logging
debug = false
# User roles allowed to login
allowed_roles = ['admin', '12', 'editor']
# Public and internal ports
listen_ports = [8080, 8081]
# Inherited from NetworkConfig.read_timeout
admin_timeout = '5s'

[global_db]
host = "postgres.internal.company"
pool_size = 50
# Database port
port = 5432
username = 'postgres'

[primary_db]
# Override host only; port, username, and pool_size should inherit from global_db!
host = "pg-master-01.internal"
# Database port
port = 5432
username = 'postgres'
pool_size = 10

[replica_db]
# Host is overridden, while the rest should cascade from primary_db (and global_db)!
host = "pg-replica-01.internal"
# Database port
port = 5432
username = 'postgres'
pool_size = 10
```

---

### 5. Generated `config.toml.example`

A fully commented-out configuration template for onboarding developers (with escaped strings and formatting):

```toml
# Automatically generated configuration template

# # Global read timeout
# read_timeout = 5s
# write_timeout = 10s
# # Application display name
# app_name = "super-service"
# # Enable verbose logging
# debug = false
# # User roles allowed to login
# allowed_roles = []
# # Public and internal ports
# listen_ports = []
# # [REQUIRED] Application HMAC secret token
# secret_key = ""
# # Inherited from NetworkConfig.read_timeout
# admin_timeout = 0s

# [global_db]
# # Database host
# host = "localhost"
# # Database port
# port = 5432
# username = "postgres"
# pool_size = 10

# [primary_db]
# # Database host
# host = "localhost"
# # Database port
# port = 5432
# username = "postgres"
# pool_size = 10

# [replica_db]
# # Database host
# host = "localhost"
# # Database port
# port = 5432
# username = "postgres"
# pool_size = 10

# [tracing]
# endpoint = "http://jaeger:14268"
# # Sampling rate (0.0 to 1.0)
# sample_rate = 0.5
```

---

### 6. Console Output at Startup (`stdout`)

```text
configuration has been automatically patched with missing keys.

--- IN-MEMORY EVALUATION RESULT ---
AppName: my-custom-dev-service
ReadTimeout (Embedded): 5s
AdminTimeout (Fallback): 5s
Roles: [admin 12 editor]

[GlobalDB]  Host: postgres.internal.company, Port: 5432, Pool: 50
[PrimaryDB] Host: pg-master-01.internal, Port: 5432, Pool: 50
[ReplicaDB] Host: pg-replica-01.internal, Port: 5432, Pool: 50

Tracing: DISABLED (nil pointer preserved)
```

### Key Takeaways:
1. **DAG Resolution:** `ReplicaDB` took `Host = pg-replica-01.internal` from its section, `PoolSize = 50` was resolved transitively from `GlobalDB` through `PrimaryDB`, and `Port = 5432` defaulted from `GlobalDB`.
2. **Slice Typing:** Roles `[admin, 12, editor]` preserved string typing (`'12'` as a string rather than an integer).
3. **Optional Pointer:** `cfg.Tracing` remained `nil`, consuming no memory and avoiding unwanted background integration initialization.