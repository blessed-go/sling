package app_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blessed-go/sling/platform/app"
	"github.com/blessed-go/sling/platform/httpx"
	"github.com/blessed-go/sling/platform/logger"
	"github.com/blessed-go/sling/platform/telemetry"
	"github.com/go-chi/chi/v5"
)

func TestAppRuntimeInProcessSmoke(t *testing.T) {
	port := getFreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	baseURL := fmt.Sprintf("http://%s", addr)

	cfg := app.Config{
		ServiceName:     "smoke-app",
		ShutdownTimeout: 2 * time.Second,
		Logger: logger.Config{
			Format: "text",
			Level:  "error", // quiet logs during tests
		},
		Telemetry: telemetry.Config{
			ServiceName:       "smoke-app",
			ServiceVersion:    "0.0.1",
			Environment:       "test",
			PrometheusEnabled: true,
			Interval:          1 * time.Second,
			// OTLPEndpoint is empty -> tracer operates in memory without network calls
		},
	}

	a, err := app.New(cfg.ServiceName, cfg)
	if err != nil {
		t.Fatalf("failed to create app: %v", err)
	}

	closerCalled := atomic.Bool{}
	mockCloser := &testCloser{onClose: func() error {
		closerCalled.Store(true)
		return nil
	}}
	a.Attach("mock-db", mockCloser)

	meteredCalled := atomic.Bool{}
	mockMetered := &testMetered{onRegister: func(m *telemetry.Meter) error {
		meteredCalled.Store(true)
		return nil
	}}
	a.Attach("mock-metered", mockMetered)

	// Mock readiness pinger.
	isReady := atomic.Bool{}
	isReady.Store(true)
	a.Attach("mock-pinger", func(ctx context.Context) error {
		if !isReady.Load() {
			return errors.New("db disconnected")
		}
		return nil
	})

	// Periodic background runner.
	var tickCount atomic.Int32
	a.AttachPeriodic("test-worker", 20*time.Millisecond, func(ctx context.Context) error {
		tickCount.Add(1)
		return nil
	})

	r := a.DefaultRouter()
	r.Route("/v1", func(r chi.Router) {
		r.Get("/items/{id}", func(w http.ResponseWriter, r *http.Request) {
			httpx.JSON(w, http.StatusOK, map[string]string{"id": chi.URLParam(r, "id")})
		})
	})

	a.ServeHTTP(addr, r)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrChan := make(chan error, 1)
	go func() {
		runErrChan <- a.Run(ctx)
	}()

	waitForServer(t, baseURL+"/healthz", 2*time.Second)

	client := &http.Client{Timeout: 1 * time.Second}

	// Verify /healthz
	resp, err := client.Get(baseURL + "/healthz")
	assertNoError(t, err)
	assertEqual(t, resp.StatusCode, http.StatusOK)
	body := readBody(t, resp)
	if !strings.Contains(body, "ok") {
		t.Fatalf("expected /healthz to contain 'ok', got %q", body)
	}

	// Verify /readyz transitions
	resp, err = client.Get(baseURL + "/readyz")
	assertNoError(t, err)
	assertEqual(t, resp.StatusCode, http.StatusOK)

	isReady.Store(false)
	resp, err = client.Get(baseURL + "/readyz")
	assertNoError(t, err)
	assertEqual(t, resp.StatusCode, http.StatusServiceUnavailable)

	isReady.Store(true)
	resp, err = client.Get(baseURL + "/readyz")
	assertNoError(t, err)
	assertEqual(t, resp.StatusCode, http.StatusOK)

	// Verify application endpoint and route metric grouping
	resp, err = client.Get(baseURL + "/v1/items/42")
	assertNoError(t, err)
	assertEqual(t, resp.StatusCode, http.StatusOK)

	var metricsBody string
	for attempt := 0; attempt < 20; attempt++ {
		resp, err = client.Get(baseURL + "/metrics")
		assertNoError(t, err)
		assertEqual(t, resp.StatusCode, http.StatusOK)
		metricsBody = readBody(t, resp)
		if strings.Contains(metricsBody, "http_server_requests_total") && strings.Contains(metricsBody, `route="/v1/items/{id}"`) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !strings.Contains(metricsBody, "http_server_requests_total") {
		t.Fatalf("expected metrics to contain 'http_server_requests_total', but got:\n%s", metricsBody)
	}
	if !strings.Contains(metricsBody, `route="/v1/items/{id}"`) {
		t.Fatalf("expected metrics to group route by pattern '/v1/items/{id}', but got:\n%s", metricsBody)
	}

	time.Sleep(50 * time.Millisecond)
	if tickCount.Load() == 0 {
		t.Fatalf("expected periodic worker to tick at least once, got 0")
	}

	// Verify graceful shutdown
	cancel()

	select {
	case err := <-runErrChan:
		assertNoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("app failed to shutdown within timeout")
	}

	if !closerCalled.Load() {
		t.Fatal("expected io.Closer to be called during shutdown phase 2, but it was not")
	}

	if !meteredCalled.Load() {
		t.Fatal("expected app.Metered to have RegisterMetrics called during Attach, but it was not")
	}
}

func TestServeHTTP_WithConfig(t *testing.T) {
	port := getFreePort(t)
	cfg := app.Config{
		ServiceName:     "cfg-test-app",
		ShutdownTimeout: 2 * time.Second,
		Logger: logger.Config{
			Format: "text",
			Level:  "error",
		},
		Telemetry: telemetry.Config{
			ServiceName:    "cfg-test-app",
			ServiceVersion: "0.0.1",
			Environment:    "test",
			Interval:       1 * time.Second,
		},
	}

	a, err := app.New(cfg.ServiceName, cfg)
	if err != nil {
		t.Fatalf("failed to create app: %v", err)
	}

	httpCfg := httpx.Config{
		Port:         fmt.Sprintf("127.0.0.1:%d", port),
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 4 * time.Second,
		IdleTimeout:  5 * time.Second,
	}

	r := chi.NewRouter()
	r.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	})

	a.ServeHTTP(httpCfg, r)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErr := make(chan error, 1)
	go func() {
		runErr <- a.Run(ctx)
	}()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForServer(t, baseURL+"/ping", 2*time.Second)

	client := &http.Client{
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}
	resp, err := client.Get(baseURL + "/ping")
	assertNoError(t, err)
	assertEqual(t, resp.StatusCode, http.StatusOK)
	assertEqual(t, readBody(t, resp), "pong")

	cancel()
	select {
	case err := <-runErr:
		assertNoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("app failed to shutdown within timeout")
	}
}


type testCloser struct {
	onClose func() error
}

func (t *testCloser) Close() error {
	if t.onClose != nil {
		return t.onClose()
	}
	return nil
}

type testMetered struct {
	onRegister func(m *telemetry.Meter) error
}

var _ app.Metered = (*testMetered)(nil)

func (t *testMetered) RegisterMetrics(m *telemetry.Meter) error {
	if t.onRegister != nil {
		return t.onRegister(m)
	}
	return nil
}

func getFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitForServer(t *testing.T, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	client := &http.Client{
		Timeout: 100 * time.Millisecond,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s did not become ready within %v", url, timeout)
}

func assertNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	return string(bytes)
}
