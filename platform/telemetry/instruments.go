package telemetry

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const defaultScope = "sling/platform/telemetry"

// Attr is an alias for attribute.KeyValue.
type Attr = attribute.KeyValue

var (
	String  = attribute.String
	Int     = attribute.Int
	Int64   = attribute.Int64
	Float64 = attribute.Float64
	Bool    = attribute.Bool
)

// Meter provides scoped metric instrument creation.
type Meter struct {
	scope string
}

// NewMeter creates a Meter with the specified component scope.
func NewMeter(scopeName string) *Meter {
	if scopeName == "" {
		scopeName = defaultScope
	}
	return &Meter{scope: scopeName}
}

func (m *Meter) meter() metric.Meter {
	return otel.GetMeterProvider().Meter(m.scope)
}

// Counter wraps an OpenTelemetry Int64Counter with dynamic MeterProvider resolution.
type Counter struct {
	scope string
	name  string
	desc  string

	mu   sync.RWMutex
	mp   metric.MeterProvider
	inst metric.Int64Counter
}

// Counter creates a new Counter.
func (m *Meter) Counter(name, desc string) (*Counter, error) {
	c := &Counter{
		scope: m.scope,
		name:  name,
		desc:  desc,
	}
	if _, err := c.resolve(); err != nil {
		return nil, err
	}
	return c, nil
}

// MustCounter creates a new Counter or panics on failure.
func (m *Meter) MustCounter(name, desc string) *Counter {
	c, err := m.Counter(name, desc)
	if err != nil {
		panic(err)
	}
	return c
}

func (c *Counter) resolve() (metric.Int64Counter, error) {
	currentMP := otel.GetMeterProvider()

	c.mu.RLock()
	if c.mp == currentMP && c.inst != nil {
		inst := c.inst
		c.mu.RUnlock()
		return inst, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.mp == currentMP && c.inst != nil {
		return c.inst, nil
	}

	inst, err := currentMP.Meter(c.scope).Int64Counter(c.name, metric.WithDescription(c.desc))
	if err != nil {
		return nil, fmt.Errorf("telemetry: failed to create counter %q: %w", c.name, err)
	}
	c.mp = currentMP
	c.inst = inst
	return inst, nil
}

// Inc increments the counter by 1 with explicit attributes.
func (c *Counter) Inc(ctx context.Context, attrs ...attribute.KeyValue) {
	c.Add(ctx, 1, attrs...)
}

// Add adds value to the counter with explicit attributes.
func (c *Counter) Add(ctx context.Context, value int64, attrs ...attribute.KeyValue) {
	inst, err := c.resolve()
	if err != nil || inst == nil {
		return
	}
	inst.Add(ctx, value, metric.WithAttributes(attrs...))
}

// IncKV increments the counter by 1 with key-value pair attributes.
func (c *Counter) IncKV(ctx context.Context, kv ...any) {
	c.AddKV(ctx, 1, kv...)
}

// AddKV adds value to the counter with key-value pair attributes.
func (c *Counter) AddKV(ctx context.Context, value int64, kv ...any) {
	c.Add(ctx, value, toAttributes(kv...)...)
}

// Histogram wraps an OpenTelemetry Float64Histogram with dynamic MeterProvider resolution.
type Histogram struct {
	scope string
	name  string
	desc  string
	cfg   histogramConfig

	mu   sync.RWMutex
	mp   metric.MeterProvider
	inst metric.Float64Histogram
}

// HistogramOption configures histogram instrument parameters.
type HistogramOption func(*histogramConfig)

type histogramConfig struct {
	buckets []float64
	unit    string
}

// WithBuckets sets custom histogram bucket boundaries.
func WithBuckets(buckets []float64) HistogramOption {
	return func(c *histogramConfig) { c.buckets = buckets }
}

// WithUnit sets the histogram unit string.
func WithUnit(unit string) HistogramOption {
	return func(c *histogramConfig) { c.unit = unit }
}

// Histogram creates a new Histogram.
func (m *Meter) Histogram(name, desc string, opts ...HistogramOption) (*Histogram, error) {
	cfg := histogramConfig{unit: "s"}
	for _, opt := range opts {
		opt(&cfg)
	}

	h := &Histogram{
		scope: m.scope,
		name:  name,
		desc:  desc,
		cfg:   cfg,
	}
	if _, err := h.resolve(); err != nil {
		return nil, err
	}
	return h, nil
}

// MustHistogram creates a new Histogram or panics on failure.
func (m *Meter) MustHistogram(name, desc string, opts ...HistogramOption) *Histogram {
	h, err := m.Histogram(name, desc, opts...)
	if err != nil {
		panic(err)
	}
	return h
}

func (h *Histogram) resolve() (metric.Float64Histogram, error) {
	currentMP := otel.GetMeterProvider()

	h.mu.RLock()
	if h.mp == currentMP && h.inst != nil {
		inst := h.inst
		h.mu.RUnlock()
		return inst, nil
	}
	h.mu.RUnlock()

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.mp == currentMP && h.inst != nil {
		return h.inst, nil
	}

	metricOpts := []metric.Float64HistogramOption{
		metric.WithDescription(h.desc),
		metric.WithUnit(h.cfg.unit),
	}
	if len(h.cfg.buckets) > 0 {
		metricOpts = append(metricOpts, metric.WithExplicitBucketBoundaries(h.cfg.buckets...))
	}

	inst, err := currentMP.Meter(h.scope).Float64Histogram(h.name, metricOpts...)
	if err != nil {
		return nil, fmt.Errorf("telemetry: failed to create histogram %q: %w", h.name, err)
	}
	h.mp = currentMP
	h.inst = inst
	return inst, nil
}

// Observe records a value in the histogram with explicit attributes.
func (h *Histogram) Observe(ctx context.Context, value float64, attrs ...attribute.KeyValue) {
	inst, err := h.resolve()
	if err != nil || inst == nil {
		return
	}
	inst.Record(ctx, value, metric.WithAttributes(attrs...))
}

// ObserveKV records a value in the histogram with key-value pair attributes.
func (h *Histogram) ObserveKV(ctx context.Context, value float64, kv ...any) {
	h.Observe(ctx, value, toAttributes(kv...)...)
}

// Gauge wraps an OpenTelemetry Float64Gauge with dynamic MeterProvider resolution.
type Gauge struct {
	scope string
	name  string
	desc  string

	mu   sync.RWMutex
	mp   metric.MeterProvider
	inst metric.Float64Gauge
}

// Gauge creates a new Gauge.
func (m *Meter) Gauge(name, desc string) (*Gauge, error) {
	g := &Gauge{
		scope: m.scope,
		name:  name,
		desc:  desc,
	}
	if _, err := g.resolve(); err != nil {
		return nil, err
	}
	return g, nil
}

// MustGauge creates a new Gauge or panics on failure.
func (m *Meter) MustGauge(name, desc string) *Gauge {
	g, err := m.Gauge(name, desc)
	if err != nil {
		panic(err)
	}
	return g
}

func (g *Gauge) resolve() (metric.Float64Gauge, error) {
	currentMP := otel.GetMeterProvider()

	g.mu.RLock()
	if g.mp == currentMP && g.inst != nil {
		inst := g.inst
		g.mu.RUnlock()
		return inst, nil
	}
	g.mu.RUnlock()

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.mp == currentMP && g.inst != nil {
		return g.inst, nil
	}

	inst, err := currentMP.Meter(g.scope).Float64Gauge(g.name, metric.WithDescription(g.desc))
	if err != nil {
		return nil, fmt.Errorf("telemetry: failed to create gauge %q: %w", g.name, err)
	}
	g.mp = currentMP
	g.inst = inst
	return inst, nil
}

// Set sets the gauge value with explicit attributes.
func (g *Gauge) Set(ctx context.Context, value float64, attrs ...attribute.KeyValue) {
	inst, err := g.resolve()
	if err != nil || inst == nil {
		return
	}
	inst.Record(ctx, value, metric.WithAttributes(attrs...))
}

// SetKV sets the gauge value with key-value pair attributes.
func (g *Gauge) SetKV(ctx context.Context, value float64, kv ...any) {
	g.Set(ctx, value, toAttributes(kv...)...)
}

// UpDownCounter wraps an OpenTelemetry Int64UpDownCounter with dynamic MeterProvider resolution.
type UpDownCounter struct {
	scope string
	name  string
	desc  string

	mu   sync.RWMutex
	mp   metric.MeterProvider
	inst metric.Int64UpDownCounter
}

// UpDownCounter creates a new UpDownCounter.
func (m *Meter) UpDownCounter(name, desc string) (*UpDownCounter, error) {
	c := &UpDownCounter{
		scope: m.scope,
		name:  name,
		desc:  desc,
	}
	if _, err := c.resolve(); err != nil {
		return nil, err
	}
	return c, nil
}

// MustUpDownCounter creates a new UpDownCounter or panics on failure.
func (m *Meter) MustUpDownCounter(name, desc string) *UpDownCounter {
	c, err := m.UpDownCounter(name, desc)
	if err != nil {
		panic(err)
	}
	return c
}

func (g *UpDownCounter) resolve() (metric.Int64UpDownCounter, error) {
	currentMP := otel.GetMeterProvider()

	g.mu.RLock()
	if g.mp == currentMP && g.inst != nil {
		inst := g.inst
		g.mu.RUnlock()
		return inst, nil
	}
	g.mu.RUnlock()

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.mp == currentMP && g.inst != nil {
		return g.inst, nil
	}

	inst, err := currentMP.Meter(g.scope).Int64UpDownCounter(g.name, metric.WithDescription(g.desc))
	if err != nil {
		return nil, fmt.Errorf("telemetry: failed to create updown counter %q: %w", g.name, err)
	}
	g.mp = currentMP
	g.inst = inst
	return inst, nil
}

// Inc increments the counter by 1 with explicit attributes.
func (g *UpDownCounter) Inc(ctx context.Context, attrs ...attribute.KeyValue) {
	g.Add(ctx, 1, attrs...)
}

// Dec decrements the counter by 1 with explicit attributes.
func (g *UpDownCounter) Dec(ctx context.Context, attrs ...attribute.KeyValue) {
	g.Add(ctx, -1, attrs...)
}

// Add adds value to the counter with explicit attributes.
func (g *UpDownCounter) Add(ctx context.Context, value int64, attrs ...attribute.KeyValue) {
	inst, err := g.resolve()
	if err != nil || inst == nil {
		return
	}
	inst.Add(ctx, value, metric.WithAttributes(attrs...))
}

// IncKV increments the counter by 1 with key-value pair attributes.
func (g *UpDownCounter) IncKV(ctx context.Context, kv ...any) {
	g.AddKV(ctx, 1, kv...)
}

// DecKV decrements the counter by 1 with key-value pair attributes.
func (g *UpDownCounter) DecKV(ctx context.Context, kv ...any) {
	g.AddKV(ctx, -1, kv...)
}

// AddKV adds value to the counter with key-value pair attributes.
func (g *UpDownCounter) AddKV(ctx context.Context, value int64, kv ...any) {
	g.Add(ctx, value, toAttributes(kv...)...)
}

var defaultMeter = NewMeter(defaultScope)

// MustCounter creates a Counter on the default scope or panics on failure.
func MustCounter(name, desc string) *Counter {
	return defaultMeter.MustCounter(name, desc)
}

// MustHistogram creates a Histogram on the default scope or panics on failure.
func MustHistogram(name, desc string, opts ...HistogramOption) *Histogram {
	return defaultMeter.MustHistogram(name, desc, opts...)
}

// MustGauge creates a Gauge on the default scope or panics on failure.
func MustGauge(name, desc string) *Gauge {
	return defaultMeter.MustGauge(name, desc)
}

// MustUpDownCounter creates an UpDownCounter on the default scope or panics on failure.
func MustUpDownCounter(name, desc string) *UpDownCounter {
	return defaultMeter.MustUpDownCounter(name, desc)
}

func toAttributes(kv ...any) []attribute.KeyValue {
	n := len(kv)
	if n == 0 {
		return nil
	}

	attrs := make([]attribute.KeyValue, 0, n/2)
	for i := 0; i < n; i++ {
		if attr, ok := kv[i].(attribute.KeyValue); ok {
			attrs = append(attrs, attr)
			continue
		}

		if i+1 >= n {
			break
		}

		var key string
		switch k := kv[i].(type) {
		case string:
			key = k
		case fmt.Stringer:
			key = k.String()
		default:
			key = fmt.Sprint(k)
		}

		switch v := kv[i+1].(type) {
		case string:
			attrs = append(attrs, attribute.String(key, v))
		case int:
			attrs = append(attrs, attribute.Int(key, v))
		case int64:
			attrs = append(attrs, attribute.Int64(key, v))
		case float64:
			attrs = append(attrs, attribute.Float64(key, v))
		case bool:
			attrs = append(attrs, attribute.Bool(key, v))
		default:
			attrs = append(attrs, attribute.String(key, fmt.Sprint(v)))
		}
		i++
	}
	return attrs
}

// Int64Observer wraps an asynchronous OpenTelemetry Int64Observer.
type Int64Observer struct {
	raw metric.Int64Observer
}

// Observe records a value with explicit attributes.
func (o *Int64Observer) Observe(value int64, attrs ...attribute.KeyValue) {
	o.raw.Observe(value, metric.WithAttributes(attrs...))
}

// ObserveKV records a value with key-value pair attributes.
func (o *Int64Observer) ObserveKV(value int64, kv ...any) {
	o.raw.Observe(value, metric.WithAttributes(toAttributes(kv...)...))
}

// ObservableGaugeCallback is invoked during asynchronous metric collection.
type ObservableGaugeCallback func(ctx context.Context, obs *Int64Observer) error

// Int64ObservableGauge registers an asynchronous gauge metric callback.
func (m *Meter) Int64ObservableGauge(name, desc string, cb ObservableGaugeCallback) error {
	_, err := m.meter().Int64ObservableGauge(name,
		metric.WithDescription(desc),
		metric.WithInt64Callback(func(ctx context.Context, o metric.Int64Observer) error {
			return cb(ctx, &Int64Observer{raw: o})
		}),
	)
	if err != nil {
		return fmt.Errorf("telemetry: failed to register observable gauge %q: %w", name, err)
	}
	return nil
}
