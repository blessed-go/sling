// Package telemetry provides OpenTelemetry tracing, metrics, and Prometheus exporter setup.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Telemetry manages lifecycle for tracer and meter providers.
type Telemetry struct {
	promHandler http.Handler
	shutdown    func(context.Context) error

	serverMu sync.Mutex
	server   *http.Server
}

// Handler returns the HTTP handler exposing Prometheus metrics.
func (t *Telemetry) Handler() http.Handler {
	return t.promHandler
}

// Shutdown flushes and stops all telemetry exporters and servers.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	var errs []error

	t.serverMu.Lock()
	if t.server != nil {
		if err := t.server.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	t.serverMu.Unlock()

	if t.shutdown != nil {
		if err := t.shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// StartPrometheusServer starts a dedicated HTTP server for Prometheus scraping.
func (t *Telemetry) StartPrometheusServer(addr string) (*http.Server, error) {
	if t.promHandler == nil {
		return nil, errors.New("telemetry: prometheus handler is not enabled")
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", t.promHandler)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
	}

	t.serverMu.Lock()
	t.server = srv
	t.serverMu.Unlock()

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			otel.Handle(fmt.Errorf("telemetry: prometheus server error: %w", err))
		}
	}()

	return srv, nil
}

// Setup initializes global TracerProvider and MeterProvider instances based on configuration.
func Setup(ctx context.Context, cfg Config) (*Telemetry, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("telemetry: invalid config: %w", err)
	}

	// Service resource metadata.
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
			semconv.DeploymentEnvironment(cfg.Environment),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: failed to create resource: %w", err)
	}

	var shutdownFuncs []func(context.Context) error

	// Configure TracerProvider.
	var tracerProvider *sdktrace.TracerProvider
	var traceProviderOpts []sdktrace.TracerProviderOption
	traceProviderOpts = append(traceProviderOpts,
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	if cfg.OTLPEndpoint != "" {
		traceOpts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
		}
		if cfg.OTLPInsecure || cfg.Environment == "local" || cfg.Environment == "test" || !strings.Contains(cfg.OTLPEndpoint, ":443") {
			traceOpts = append(traceOpts, otlptracegrpc.WithInsecure())
		}

		traceExporter, err := otlptracegrpc.New(ctx, traceOpts...)
		if err != nil {
			return nil, fmt.Errorf("telemetry: failed to create trace exporter: %w", err)
		}

		var bspOpts []sdktrace.BatchSpanProcessorOption
		if cfg.Environment == "local" || cfg.Environment == "test" {
			bspOpts = append(bspOpts, sdktrace.WithBatchTimeout(500*time.Millisecond))
		}
		traceProviderOpts = append(traceProviderOpts, sdktrace.WithSpanProcessor(
			sdktrace.NewBatchSpanProcessor(traceExporter, bspOpts...),
		))
	}

	// Always register standard W3C propagators for cross-service context propagation
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	tracerProvider = sdktrace.NewTracerProvider(
		traceProviderOpts...,
	)

	otel.SetTracerProvider(tracerProvider)
	shutdownFuncs = append(shutdownFuncs, tracerProvider.Shutdown)

	// Configure MeterProvider.
	var (
		readers     []sdkmetric.Reader
		promHandler http.Handler
	)

	if cfg.OTLPEndpoint != "" {
		metricOpts := []otlpmetricgrpc.Option{
			otlpmetricgrpc.WithEndpoint(cfg.OTLPEndpoint),
		}
		if cfg.OTLPInsecure || cfg.Environment == "local" || cfg.Environment == "test" || !strings.Contains(cfg.OTLPEndpoint, ":443") {
			metricOpts = append(metricOpts, otlpmetricgrpc.WithInsecure())
		}

		otlpMetricExporter, err := otlpmetricgrpc.New(ctx, metricOpts...)
		if err != nil {
			_ = tracerProvider.Shutdown(ctx)
			return nil, fmt.Errorf("telemetry: failed to create OTLP metric exporter: %w", err)
		}

		readers = append(readers, sdkmetric.NewPeriodicReader(
			otlpMetricExporter,
			sdkmetric.WithInterval(cfg.Interval),
		))
	}

	if cfg.PrometheusEnabled {
		reg := prometheus.NewRegistry()
		promExporter, err := otelprom.New(otelprom.WithRegisterer(reg))
		if err != nil {
			_ = tracerProvider.Shutdown(ctx)
			return nil, fmt.Errorf("telemetry: failed to create Prometheus exporter: %w", err)
		}
		readers = append(readers, promExporter)
		promHandler = promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
	}

	metricProviderOpts := []sdkmetric.Option{
		sdkmetric.WithResource(res),
	}
	for _, r := range readers {
		metricProviderOpts = append(metricProviderOpts, sdkmetric.WithReader(r))
	}

	mp := sdkmetric.NewMeterProvider(metricProviderOpts...)
	otel.SetMeterProvider(mp)
	shutdownFuncs = append(shutdownFuncs, mp.Shutdown)

	if len(readers) > 0 {
		if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
			_ = mp.Shutdown(ctx)
			_ = tracerProvider.Shutdown(ctx)
			return nil, fmt.Errorf("telemetry: failed to start runtime metrics: %w", err)
		}
	}

	return &Telemetry{
		promHandler: promHandler,
		shutdown: func(ctx context.Context) error {
			var errs []error
			for _, fn := range shutdownFuncs {
				if err := fn(ctx); err != nil {
					errs = append(errs, err)
				}
			}
			return errors.Join(errs...)
		},
	}, nil
}
