package config

import (
	"errors"
	"strings"
	"testing"
)

type TestTelemetryConfig struct {
	Protocol string `toml:"protocol"`
	Port     int    `toml:"port"`
	Exporter string `toml:"exporter"`
}

func (t *TestTelemetryConfig) SetDefaults() {
	if t.Port == 0 {
		if t.Protocol == "https" {
			t.Port = 8443
		} else {
			t.Port = 8080
		}
	}
}

func (t *TestTelemetryConfig) Validate() error {
	if t.Port < 1024 {
		return errors.New("privileged ports not allowed")
	}
	if t.Exporter == "" {
		return errors.New("exporter must be configured")
	}
	return nil
}

type RootLifecycleConfig struct {
	Telemetry TestTelemetryConfig  `toml:"telemetry"`
	Optional  *TestTelemetryConfig `toml:"optional"`
}

func TestLifecycle_SetDefaultsAndValidationSuccess(t *testing.T) {
	cfg := RootLifecycleConfig{
		Telemetry: TestTelemetryConfig{
			Protocol: "https",
			Port:     0,
			Exporter: "jaeger:14268",
		},
		Optional: nil,
	}

	if err := SetDefaultsRecursive(&cfg); err != nil {
		t.Fatalf("SetDefaults failed: %v", err)
	}

	if cfg.Telemetry.Port != 8443 {
		t.Errorf("expected port 8443 after SetDefaults, got %d", cfg.Telemetry.Port)
	}

	if err := ValidateRecursive(&cfg); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
}

func TestLifecycle_ValidationFailureWithPath(t *testing.T) {
	cfg := RootLifecycleConfig{
		Telemetry: TestTelemetryConfig{
			Protocol: "http",
			Port:     8080,
			Exporter: "", // Empty exporter must trigger a validation error.
		},
	}

	err := ValidateRecursive(&cfg)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}

	// Verify that the error message contains the exact path of the "telemetry" field.
	if !strings.Contains(err.Error(), `"telemetry"`) {
		t.Errorf("expected error to mention path 'telemetry', got: %v", err)
	}
	if !strings.Contains(err.Error(), "exporter must be configured") {
		t.Errorf("expected custom error message, got: %v", err)
	}
}
