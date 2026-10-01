// Package system aggregates platform-level configurations for services.
package system

import (
	"github.com/blessed-go/sling/platform/grpcx"
	"github.com/blessed-go/sling/platform/httpx"
	"github.com/blessed-go/sling/platform/jwt"
	"github.com/blessed-go/sling/platform/logger"
	"github.com/blessed-go/sling/platform/postgres"
	"github.com/blessed-go/sling/platform/redis"
)

// Config represents shared configuration options across platform components.
type Config struct {
	Postgres postgres.Config `toml:"postgres" env-prefix:"POSTGRES_" comment:"Global Postgres database config"`
	Redis    redis.Config    `toml:"redis"    env-prefix:"REDIS_"   comment:"Global Redis cache config"`
	Log      logger.Config   `toml:"log"      env-prefix:"LOG_"     comment:"Global Logger config"`
	HTTP     httpx.Config    `toml:"http"     env-prefix:"HTTP_"    comment:"Global HTTP server config"`
	JWT      jwt.Config      `toml:"jwt"      env-prefix:"JWT_"     comment:"Global JWT token config"`
	GRPC     grpcx.Config    `toml:"grpc"     env-prefix:"GRPC_"    comment:"Global gRPC server config"`
}
