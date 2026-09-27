// Package logger provides structured logging with slog, tint-based human-readable
// output, and OpenTelemetry trace correlation.
package logger

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/lmittmann/tint"
	"go.opentelemetry.io/otel/trace"
)

// Config defines logger configuration settings.
type Config struct {
	Format string `toml:"format" env:"FORMAT" env-default:"text" comment:"Log format: text or json"`
	Level  string `toml:"level" env:"LEVEL" env-default:"info" comment:"Log level: debug, info, warn, error"`
}

type ctxKey struct{}

// Option decorates the base slog.Handler.
type Option func(slog.Handler) slog.Handler

// WithTracing returns an Option that injects trace_id and span_id into log records.
func WithTracing() Option {
	return func(next slog.Handler) slog.Handler {
		return &traceHandler{next: next}
	}
}

// New creates a configured slog.Logger instance based on Config and Options.
func New(cfg Config, opts ...Option) *slog.Logger {
	var handler slog.Handler
	level := parseLogLevel(cfg.Level)

	switch strings.ToLower(cfg.Format) {
	case "text":
		handler = tint.NewTextHandler(os.Stdout, &tint.Options{
			Level:      level,
			TimeFormat: time.TimeOnly,
		})
	case "json":
		fallthrough
	default:
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: level,
		})
	}

	for _, opt := range opts {
		handler = opt(handler)
	}

	return slog.New(handler)
}

// WithContext returns a new context carrying the given logger.
func WithContext(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, logger)
}

// FromContext extracts the logger from context or returns the default logger.
func FromContext(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return logger
	}
	return slog.Default()
}

// Debug logs at LevelDebug using the logger from context.
func Debug(ctx context.Context, msg string, args ...any) {
	FromContext(ctx).DebugContext(ctx, msg, args...)
}

// Info logs at LevelInfo using the logger from context.
func Info(ctx context.Context, msg string, args ...any) {
	FromContext(ctx).InfoContext(ctx, msg, args...)
}

// Warn logs at LevelWarn using the logger from context.
func Warn(ctx context.Context, msg string, args ...any) {
	FromContext(ctx).WarnContext(ctx, msg, args...)
}

// Error logs at LevelError using the logger from context.
func Error(ctx context.Context, msg string, args ...any) {
	FromContext(ctx).ErrorContext(ctx, msg, args...)
}

func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// traceHandler injects active trace and span IDs into log records.
type traceHandler struct {
	next slog.Handler
}

func (h *traceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	span := trace.SpanFromContext(ctx)
	if sc := span.SpanContext(); sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.next.Handle(ctx, r)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{next: h.next.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{next: h.next.WithGroup(name)}
}
