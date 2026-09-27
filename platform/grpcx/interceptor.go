package grpcx

import (
	"context"
	"log/slog"
	"time"

	"github.com/blessed-go/sling/platform/ctxerr"
	"github.com/blessed-go/sling/platform/logger"

	"google.golang.org/grpc"
)

// ServerLoggingInterceptor returns a gRPC unary server interceptor that logs method execution and duration.
func ServerLoggingInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		ctx = ctxerr.WithSlot(logger.WithContext(ctx, log))

		reply, err := handler(ctx, req)
		duration := time.Since(start)

		if err != nil {
			logErr := err
			if internalErr := ctxerr.Err(ctx); internalErr != nil {
				logErr = internalErr
			}
			logger.FromContext(ctx).Error("RPC failed", slog.String("method", info.FullMethod), slog.Duration("duration", duration), slog.Any("error", logErr))
		} else {
			logger.FromContext(ctx).Info("RPC succeeded", slog.String("method", info.FullMethod), slog.Duration("duration", duration))
		}
		return reply, err
	}
}

// ClientLoggingInterceptor returns a gRPC unary client interceptor that logs method execution and duration.
func ClientLoggingInterceptor(logger *slog.Logger) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		start := time.Now()

		err := invoker(ctx, method, req, reply, cc, opts...)
		duration := time.Since(start)

		if err != nil {
			logger.Error("RPC failed", slog.String("method", method), slog.Duration("duration", duration), slog.Any("error", err))
		} else {
			logger.Info("RPC succeeded", slog.String("method", method), slog.Duration("duration", duration))
		}
		return err
	}
}
