package httpx

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/blessed-go/sling/platform/ctxerr"
	"github.com/blessed-go/sling/platform/logger"
	"github.com/blessed-go/sling/platform/telemetry"
	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

var (
	traceWarnOnce sync.Once
	meter         = telemetry.NewMeter("sling/platform/httpx")

	httpDuration = meter.MustHistogram(
		"http_server_request_duration_seconds",
		"Duration of HTTP requests in seconds",
		telemetry.WithBuckets([]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}),
		telemetry.WithUnit("s"),
	)

	httpRequestsTotal = meter.MustCounter(
		"http_server_requests_total",
		"Total number of HTTP requests processed",
	)
)

// Observability returns an HTTP middleware providing tracing, metrics, access logging, and error tracking.
func Observability(serviceName string, log *slog.Logger) func(http.Handler) http.Handler {
	tracer := otel.Tracer(serviceName)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))

			ctx, span := tracer.Start(ctx, r.Method+" "+r.URL.Path,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					semconv.HTTPRequestMethodKey.String(r.Method),
					semconv.URLPath(r.URL.Path),
				),
			)
			defer span.End()

			if !span.SpanContext().IsValid() {
				traceWarnOnce.Do(func() {
					slog.Warn("tracer provider is no-op; spans will not be collected")
				})
			}

			ctx = ctxerr.WithSlot(ctx)
			ctx = logger.WithContext(ctx, log)

			rw := &statusWriter{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rw, r.WithContext(ctx))

			duration := time.Since(start)
			durationSec := duration.Seconds()
			route := getRoutePattern(r)
			span.SetName(r.Method + " " + route)
			span.SetAttributes(
				semconv.HTTPRoute(route),
				semconv.HTTPResponseStatusCode(rw.status),
			)
			statusStr := strconv.Itoa(rw.status)

			httpDuration.ObserveKV(ctx, durationSec,
				"service", serviceName,
				"method", r.Method,
				"route", route,
				"status", statusStr,
			)
			httpRequestsTotal.IncKV(ctx,
				"service", serviceName,
				"method", r.Method,
				"route", route,
				"status", statusStr,
			)

			reqErr := ctxerr.Err(ctx)
			if rw.status >= 500 {
				span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", rw.status))
				if reqErr != nil {
					span.RecordError(reqErr)
				}
			}

			level := slog.LevelInfo
			switch {
			case rw.status >= 500:
				level = slog.LevelError
			case rw.status >= 400:
				level = slog.LevelWarn
			}

			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("route", route),
				slog.Int("status", rw.status),
				slog.Duration("duration", duration),
			}
			if reqErr != nil {
				attrs = append(attrs, slog.Any("error", reqErr))
			}

			logger.FromContext(ctx).LogAttrs(ctx, level, "request completed", attrs...)
		})
	}
}

func getRoutePattern(r *http.Request) string {
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		if pattern := rctx.RoutePattern(); pattern != "" {
			return pattern
		}
	}
	return "unmatched"
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.status = code
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
	}
	return hijacker.Hijack()
}

func (w *statusWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

