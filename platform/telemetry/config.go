package telemetry

import (
	"errors"
	"time"
)

var (
	// ErrServiceNameRequired indicates service name is missing.
	ErrServiceNameRequired = errors.New("service_name is required")
	// ErrServiceVersionRequired indicates service version is missing.
	ErrServiceVersionRequired = errors.New("service_version is required")
	// ErrEnvironmentRequired indicates environment is missing.
	ErrEnvironmentRequired = errors.New("environment is required")
	// ErrIntervalRequired indicates metrics interval must be positive.
	ErrIntervalRequired = errors.New("interval must be greater than 0")
	// ErrNoExportersEnabled indicates no telemetry exporters are enabled.
	ErrNoExportersEnabled = errors.New("at least one exporter (OTLP or Prometheus) must be enabled")
)

// Config defines OpenTelemetry and Prometheus configuration parameters.
type Config struct {
	ServiceName       string        `toml:"service_name" env:"OTEL_SERVICE_NAME" env-default:"unknown_service"`
	ServiceVersion    string        `toml:"service_version" env:"OTEL_SERVICE_VERSION" env-default:"0.0.1"`
	Environment       string        `toml:"environment" env:"ENV" env-default:"production"`
	OTLPEndpoint      string        `toml:"otel_endpoint" env:"OTEL_EXPORTER_OTLP_ENDPOINT" env-default:"localhost:4317"`
	OTLPInsecure      bool          `toml:"otel_insecure" env:"OTEL_EXPORTER_OTLP_INSECURE" env-default:"true"`
	PrometheusEnabled bool          `toml:"prometheus_enabled" env:"METRICS_PROMETHEUS_ENABLED" env-default:"true"`
	Interval          time.Duration `toml:"interval" env:"OTEL_INTERVAL" env-default:"15s"`
}

func (c *Config) Validate() error {
	if c.ServiceName == "" {
		return ErrServiceNameRequired
	}
	if c.ServiceVersion == "" {
		return ErrServiceVersionRequired
	}
	if c.Environment == "" {
		return ErrEnvironmentRequired
	}
	if c.Interval <= 0 {
		return ErrIntervalRequired
	}
	return nil
}
