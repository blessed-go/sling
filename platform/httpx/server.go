package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Config holds HTTP server configuration parameters.
type Config struct {
	Port         string        `env:"PORT" toml:"port" env-default:"8080"`
	ReadTimeout  time.Duration `env:"READ_TIMEOUT" toml:"read_timeout" env-default:"5s"`
	WriteTimeout time.Duration `env:"WRITE_TIMEOUT" toml:"write_timeout" env-default:"10s"`
	IdleTimeout  time.Duration `env:"IDLE_TIMEOUT" toml:"idle_timeout" env-default:"15s"`
}

// Serve starts an HTTP server and gracefully shuts it down when ctx is cancelled.
func Serve(ctx context.Context, log *slog.Logger, cfg Config, handler http.Handler) error {
	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      handler,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Info("starting HTTP server", slog.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- fmt.Errorf("listen and serve: %w", err)
		}
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		log.Info("HTTP shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		if err := srv.Close(); err != nil {
			return fmt.Errorf("forced server close: %w", err)
		}
	}

	log.Info("HTTP server gracefully stopped")
	return nil
}
