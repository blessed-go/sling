// Package app provides the core runtime lifecycle harness for services.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/blessed-go/sling/platform/httpx"
	"github.com/blessed-go/sling/platform/logger"
	"github.com/blessed-go/sling/platform/run"
	"github.com/blessed-go/sling/platform/telemetry"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// Config defines runtime application settings.
type Config struct {
	ServiceName     string           `toml:"service_name" env:"SERVICE_NAME" env-default:"app"`
	Environment     string           `toml:"environment" env:"APP_ENV" env-default:"local"`
	ShutdownTimeout time.Duration    `toml:"shutdown_timeout" env:"SHUTDOWN_TIMEOUT" env-default:"10s" comment:"Graceful shutdown timeout"`
	Logger          logger.Config    `toml:"logger"`
	Telemetry       telemetry.Config `toml:"telemetry"`
	Profiling       ProfilingConfig  `toml:"profiling"`
}

// ProfilingConfig defines pprof profiling settings.
type ProfilingConfig struct {
	Enabled       bool `toml:"enabled" env:"PPROF_ENABLED" env-default:"false" comment:"Enable /debug/pprof"`
	BlockRate     int  `toml:"block_rate" env:"PPROF_BLOCK_RATE" env-default:"0" comment:"Block profiling rate (0: disabled, 1: all)"`
	MutexFraction int  `toml:"mutex_fraction" env:"PPROF_MUTEX_FRACTION" env-default:"0" comment:"Mutex profiling sample fraction (0: disabled)"`
}

// Pinger defines a component health check contract.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Metered defines a component that registers custom telemetry metrics.
type Metered interface {
	RegisterMetrics(meter *telemetry.Meter) error
}

// Runner defines a long-running background component.
type Runner interface {
	Run(ctx context.Context) error
}

type runnerComponent struct {
	name string
	fn   func(context.Context) error
}

// App is the central runtime coordinator for HTTP servers, background runners, and graceful shutdown.
type App struct {
	name  string
	cfg   Config
	log   *slog.Logger
	tel   *telemetry.Telemetry
	meter metric.Meter

	mu      sync.Mutex
	servers []*http.Server
	closers []io.Closer
	pingers map[string]func(context.Context) error
	runners []runnerComponent
}

// AttachPeriodic runs fn periodically at the given interval until the application context is canceled.
func (a *App) AttachPeriodic(name string, interval time.Duration, fn func(context.Context) error) {
	a.AttachRunner(name, func(ctx context.Context) error {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		a.log.Info("periodic runner started", "name", name, "interval", interval)

		for {
			select {
			case <-ctx.Done():
				a.log.Info("periodic runner stopping", "name", name)
				return nil
			case <-ticker.C:
				if err := fn(ctx); err != nil {
					a.log.Error("periodic task failed", "name", name, "err", err)
				}
			}
		}
	})
}

func safeRunner(name string, fn func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("runner %q panicked: %v\nstack:\n%s", name, r, debug.Stack())
			}
		}()
		return fn(ctx)
	}
}

// AttachRunner registers a named background runner.
func (a *App) AttachRunner(name string, fn func(context.Context) error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.runners = append(a.runners, runnerComponent{name: name, fn: safeRunner(name, fn)})
}

// New creates and initializes a new App instance.
func New(name string, cfg Config) (*App, error) {
	if name != "" {
		cfg.ServiceName = name
		cfg.Telemetry.ServiceName = name
	}
	if cfg.Environment != "" {
		cfg.Telemetry.Environment = cfg.Environment
	}

	ctx := context.Background()
	tel, err := telemetry.Setup(ctx, cfg.Telemetry)
	if err != nil {
		return nil, fmt.Errorf("app: failed to setup telemetry: %w", err)
	}

	log := logger.New(cfg.Logger, logger.WithTracing())

	if cfg.Profiling.BlockRate > 0 {
		runtime.SetBlockProfileRate(cfg.Profiling.BlockRate)
	}
	if cfg.Profiling.MutexFraction > 0 {
		runtime.SetMutexProfileFraction(cfg.Profiling.MutexFraction)
	}

	return &App{
		name:    cfg.ServiceName,
		cfg:     cfg,
		log:     log,
		tel:     tel,
		meter:   otel.GetMeterProvider().Meter(cfg.ServiceName),
		pingers: make(map[string]func(context.Context) error),
	}, nil
}

// Logger returns the application logger.
func (a *App) Logger() *slog.Logger { return a.log }

// Meter returns the application OpenTelemetry meter.
func (a *App) Meter() metric.Meter { return a.meter }

// Telemetry returns the application telemetry subsystem.
func (a *App) Telemetry() *telemetry.Telemetry { return a.tel }

// Attach registers a component implementing Pinger, Metered, Runner, or io.Closer.
func (a *App) Attach(name string, target any) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if p, ok := target.(Pinger); ok {
		a.pingers[name] = p.Ping
	} else if pFn, ok := target.(func(context.Context) error); ok {
		a.pingers[name] = pFn
	}

	if m, ok := target.(Metered); ok {
		meter := telemetry.NewMeter(name)
		if err := m.RegisterMetrics(meter); err != nil {
			a.log.Warn("failed to register metrics", "component", name, "err", err)
		}
	} else if m, ok := target.(interface{ RegisterMetrics(metric.Meter) error }); ok {
		if a.meter != nil {
			if err := m.RegisterMetrics(a.meter); err != nil {
				a.log.Warn("failed to register metrics", "component", name, "err", err)
			}
		}
	}

	if r, ok := target.(Runner); ok {
		a.runners = append(a.runners, runnerComponent{name: name, fn: safeRunner(name, r.Run)})
	}

	if c, ok := target.(io.Closer); ok {
		a.closers = append(a.closers, c)
	}
}

// DefaultRouter returns a Chi router preconfigured with observability middleware, recoverer, and system routes.
func (a *App) DefaultRouter() *chi.Mux {
	r := chi.NewRouter()

	r.Use(httpx.Observability(a.name, a.log))
	r.Use(middleware.Recoverer)

	r.Get("/healthz", a.Healthz)
	r.Get("/readyz", a.Readyz)
	if a.tel != nil && a.tel.Handler() != nil {
		r.Handle("/metrics", a.tel.Handler())
	}
	if a.cfg.Profiling.Enabled {
		r.Mount("/debug", middleware.Profiler())
	}
	return r
}

// Healthz handles liveness probes.
func (a *App) Healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

// Readyz handles readiness probes by checking all registered pingers.
func (a *App) Readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	a.mu.Lock()
	pingers := make(map[string]func(context.Context) error, len(a.pingers))
	for name, ping := range a.pingers {
		pingers[name] = ping
	}
	a.mu.Unlock()

	for name, ping := range pingers {
		if err := ping(ctx); err != nil {
			a.log.Warn("readiness probe failed", "component", name, "err", err)
			http.Error(w, fmt.Sprintf("%s is unavailable: %v", name, err), http.StatusServiceUnavailable)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready\n"))
}

// ServeHTTP registers an HTTP server to be managed by the application.
// target can be a port string (e.g. "8080", ":8080"), an httpx.Config, or an *httpx.Config.
func (a *App) ServeHTTP(target any, handler http.Handler) {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	switch v := target.(type) {
	case string:
		addr := v
		if !strings.Contains(addr, ":") {
			addr = ":" + addr
		}
		srv.Addr = addr
	case httpx.Config:
		a.applyHTTPConfig(srv, v)
	case *httpx.Config:
		if v != nil {
			a.applyHTTPConfig(srv, *v)
		}
	default:
		addr := fmt.Sprint(target)
		if !strings.Contains(addr, ":") {
			addr = ":" + addr
		}
		srv.Addr = addr
	}

	a.mu.Lock()
	a.servers = append(a.servers, srv)
	a.mu.Unlock()
}

func (a *App) applyHTTPConfig(srv *http.Server, cfg httpx.Config) {
	addr := cfg.Port
	if !strings.Contains(addr, ":") {
		addr = ":" + addr
	}
	srv.Addr = addr
	if cfg.ReadTimeout > 0 {
		srv.ReadTimeout = cfg.ReadTimeout
	}
	if cfg.WriteTimeout > 0 {
		srv.WriteTimeout = cfg.WriteTimeout
	}
	if cfg.IdleTimeout > 0 {
		srv.IdleTimeout = cfg.IdleTimeout
	}
}

// ServeHTTPConfig registers an HTTP server configured via httpx.Config.
func (a *App) ServeHTTPConfig(cfg httpx.Config, handler http.Handler) {
	a.ServeHTTP(cfg, handler)
}

// FatalIf logs an error and terminates the process if err is not nil.
func (a *App) FatalIf(err error, msg string) {
	if err == nil {
		return
	}
	a.log.Error("fatal error", "msg", msg, "err", err)
	_ = os.Stdout.Sync()
	_ = os.Stderr.Sync()
	os.Exit(1)
}

// Run starts all servers and runners, blocking until ctx is canceled or an error occurs.
func (a *App) Run(ctx context.Context) error {
	var g run.Group

	a.mu.Lock()
	for _, srv := range a.servers {
		s := srv
		g.Add("http:"+s.Addr, func(ctx context.Context) error {
			errChan := make(chan error, 1)
			go func() {
				a.log.Info("starting HTTP server", "addr", s.Addr)
				if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					errChan <- fmt.Errorf("http server on %s failed: %w", s.Addr, err)
				}
			}()

			select {
			case err := <-errChan:
				return err
			case <-ctx.Done():
				shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.ShutdownTimeout)
				defer cancel()
				return s.Shutdown(shutdownCtx)
			}
		})
	}

	for _, r := range a.runners {
		g.Add(r.name, r.fn)
	}
	a.mu.Unlock()

	a.log.Info("application is running", "service", a.name)

	err := g.Run(ctx)

	a.shutdown()

	if err != nil && !errors.Is(err, context.Canceled) {
		a.log.Error("application terminated with error", "err", err)
		return err
	}

	a.log.Info("application stopped cleanly")
	return nil
}

// shutdown performs staged graceful teardown: closes attached resources in reverse registration order (LIFO),
// and finally flushes telemetry. HTTP server ingress shutdown is handled during g.Run().
func (a *App) shutdown() {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.ShutdownTimeout)
	defer cancel()

	a.mu.Lock()
	closers := make([]io.Closer, len(a.closers))
	copy(closers, a.closers)
	a.mu.Unlock()

	a.log.Info("closing storage connections")
	for i := len(closers) - 1; i >= 0; i-- {
		if err := closers[i].Close(); err != nil {
			a.log.Error("error closing resource", "err", err)
		}
	}

	a.log.Info("flushing telemetry")
	if a.tel != nil {
		if err := a.tel.Shutdown(shutdownCtx); err != nil {
			a.log.Error("error flushing telemetry", "err", err)
		}
	}
}
