//go:build ignore

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"__MODULE__/internal/__SERVICE__"

	"github.com/blessed-go/sling/platform/app"
	"github.com/blessed-go/sling/platform/config"
	"github.com/blessed-go/sling/platform/httpx"
	"github.com/blessed-go/sling/platform/postgres"
	"github.com/go-chi/chi/v5"
)

func main() {
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		if _, err := os.Stat("conf/__SERVICE__.toml"); err == nil {
			cfgPath = "conf/__SERVICE__.toml"
		} else {
			cfgPath = "conf/conf.toml"
		}
	}
	isLocal := os.Getenv("APP_ENV") == "local" || os.Getenv("APP_ENV") == ""

	cfg, err := config.Load[__SERVICE__.Config](cfgPath, config.WithAutoRepair(isLocal))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to load config: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	core, err := app.New("__SERVICE__", cfg.App)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to create app: %v\n", err)
		os.Exit(1)
	}

	pg, err := postgres.New(ctx, cfg.Postgres)
	if err != nil {
		core.Logger().Error("failed to connect to postgres", "err", err)
		os.Exit(1)
	}
	core.Attach("postgres", pg)

	repo := __SERVICE__.NewRepo(pg.Pool)
	svc := __SERVICE__.NewService(core.Logger(), repo)
	h := __SERVICE__.NewHandler(svc)

	r := core.DefaultRouter()
	r.Route("/v1", func(r chi.Router) {
		r.Use(httpx.MapErrors(__SERVICE__.ErrorMapping))

		r.Get("/items/{id}", h.GetItem)
		r.Get("/items", h.ListItems)
		r.Post("/items", h.CreateItem)
	})

	core.ServeHTTP(cfg.HTTP, r)

	if err := core.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: service terminated: %v\n", err)
		os.Exit(1)
	}
}
