package domain

import (
	"github.com/blessed-go/sling/platform/app"
	"github.com/blessed-go/sling/platform/httpx"
	"github.com/blessed-go/sling/platform/postgres"
	// "github.com/blessed-go/sling/platform/redis"
)

type Config struct {
	Postgres postgres.Config `toml:"postgres"`
	HTTP     httpx.Config    `toml:"http"`
	App      app.Config      `toml:"app"`

	// Redis redis.Config `toml:"redis"`
}
