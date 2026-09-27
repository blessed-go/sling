package telemetry_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blessed-go/sling/platform/telemetry"
	"go.opentelemetry.io/otel"
)

func TestTelemetry_FullPipeline(t *testing.T) {
	ctx := context.Background()

	cfg := telemetry.Config{
		ServiceName:       "test-service",
		ServiceVersion:    "1.0.0",
		Environment:       "test",
		PrometheusEnabled: true,
		Interval:          time.Second,
	}

	tel, err := telemetry.Setup(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}
	defer func() {
		if err := tel.Shutdown(ctx); err != nil {
			t.Errorf("shutdown error: %v", err)
		}
	}()

	meter := telemetry.NewMeter("test-scope")
	cnt := meter.MustCounter("test_orders_total", "Test counter")

	cnt.Inc(ctx, telemetry.String("status", "success"))
	cnt.IncKV(ctx, "status", "failed", "retry", true)

	hist := meter.MustHistogram("test_duration_seconds", "Duration")
	hist.Observe(ctx, 0.42, telemetry.String("route", "/pay"))
	hist.ObserveKV(ctx, 1.25, "route", "/login")

	handler := tel.Handler()
	if handler == nil {
		t.Fatal("prometheus handler must not be nil")
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	body := rec.Body.String()

	expectedSubstrings := []string{
		"test_orders_total",
		`status="success"`,
		`status="failed"`,
		"test_duration_seconds",
		`route="/pay"`,
		"go_goroutine_count",
	}

	for _, sub := range expectedSubstrings {
		if !strings.Contains(body, sub) {
			t.Errorf("expected /metrics to contain %q, but got:\n%s", sub, body)
		}
	}
}

func TestTelemetry_PropagatorConfiguredWithoutOTLP(t *testing.T) {
	ctx := context.Background()
	cfg := telemetry.Config{
		ServiceName:    "no-otlp-service",
		ServiceVersion: "1.0.0",
		Environment:    "test",
		Interval:       time.Second,
	}

	tel, err := telemetry.Setup(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}
	defer func() {
		_ = tel.Shutdown(ctx)
	}()

	propagator := otel.GetTextMapPropagator()
	fields := propagator.Fields()

	hasTraceparent := false
	hasBaggage := false
	for _, f := range fields {
		if f == "traceparent" {
			hasTraceparent = true
		}
		if f == "baggage" {
			hasBaggage = true
		}
	}

	if !hasTraceparent {
		t.Errorf("expected text map propagator to contain 'traceparent', got fields: %v", fields)
	}
	if !hasBaggage {
		t.Errorf("expected text map propagator to contain 'baggage', got fields: %v", fields)
	}
}

var preDeclaredMeter = telemetry.NewMeter("predeclared-scope")
var preDeclaredCounter = preDeclaredMeter.MustCounter("predeclared_counter_total", "Test")

func TestTelemetry_MultipleSetup_PreDeclaredInstruments(t *testing.T) {
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		cfg := telemetry.Config{
			ServiceName:       "iter-service",
			ServiceVersion:    "1.0.0",
			Environment:       "test",
			PrometheusEnabled: true,
			Interval:          time.Second,
		}

		tel, err := telemetry.Setup(ctx, cfg)
		if err != nil {
			t.Fatalf("iter %d: setup error: %v", i, err)
		}

		preDeclaredCounter.Inc(ctx, telemetry.String("iter", "true"))

		handler := tel.Handler()
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		body := rec.Body.String()
		if !strings.Contains(body, "predeclared_counter_total") {
			t.Fatalf("iter %d: expected metrics to contain predeclared_counter_total, got:\n%s", i, body)
		}

		_ = tel.Shutdown(ctx)
	}
}


