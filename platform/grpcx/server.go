// Package grpcx provides helpers for configuring, serving, and intercepting gRPC servers.
package grpcx

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"google.golang.org/grpc"
)

// Config specifies gRPC server configuration.
type Config struct {
	Port int `env:"PORT" toml:"port" env-default:"50051"`
}

// NewListener binds a TCP listener using the configured gRPC port.
func NewListener(cfg Config) (net.Listener, error) {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		return nil, fmt.Errorf("failed to listen on gRPC port %d: %w", cfg.Port, err)
	}
	return lis, nil
}

// Serve runs the gRPC server and manages graceful shutdown on context cancellation.
func Serve(ctx context.Context, log *slog.Logger, srv *grpc.Server, lis net.Listener) error {
	serverErr := make(chan error, 1)
	go func() {
		log.Info("starting gRPC server", slog.String("addr", lis.Addr().String()))
		if err := srv.Serve(lis); err != nil {
			serverErr <- fmt.Errorf("grpc serve: %w", err)
		}
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		log.Info("gRPC shutdown signal received")
	}

	stopped := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
		log.Info("gRPC server gracefully stopped")
	case <-time.After(5 * time.Second):
		srv.Stop()
		log.Warn("gRPC server forced to stop due to timeout")
	}

	return nil
}
