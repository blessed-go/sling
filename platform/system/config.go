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
	Postgres postgres.Config `toml:"postgres" comment:"Global Postgres database config"`
	Redis    redis.Config    `toml:"redis"    comment:"Global Redis cache config"`
	Log      logger.Config   `toml:"log"      comment:"Global Logger config"`
	HTTP     httpx.Config    `toml:"http"     comment:"Global HTTP server config"`
	JWT      jwt.Config      `toml:"jwt"      comment:"Global JWT token config"`
	GRPC     grpcx.Config    `toml:"grpc"     comment:"Global gRPC server config"`
}
