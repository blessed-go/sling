// Package config provides configuration loading, schema validation, fallback resolution, and automatic repair.
package config

import (
	"fmt"
	"io"
	"os"
	"reflect"

	"github.com/ilyakaznacheev/cleanenv"
)

// Options holds configuration loader parameters.
type Options struct {
	AutoRepair bool
	Logger     io.Writer
}

// Option configures loader behavior.
type Option func(*Options)

// WithAutoRepair configures whether missing configuration fields are automatically written to disk.
func WithAutoRepair(enable bool) Option {
	return func(o *Options) {
		o.AutoRepair = enable
	}
}

// WithLogger configures a custom writer for loader diagnostic messages.
func WithLogger(w io.Writer) Option {
	return func(o *Options) {
		o.Logger = w
	}
}

// Load reads and parses a configuration struct from TOML and environment variables,
// applying defaults, fallbacks, and validation rules.
func Load[T any](path string, opts ...Option) (*T, error) {
	cfgOpts := Options{
		AutoRepair: os.Getenv("APP_ENV") == "local",
		Logger:     io.Discard,
	}

	for _, opt := range opts {
		opt(&cfgOpts)
	}

	var schemaTarget T
	schema, err := BuildSchema(reflect.TypeOf(schemaTarget), "")
	if err != nil {
		return nil, fmt.Errorf("failed to build configuration schema: %w", err)
	}

	if cfgOpts.AutoRepair {
		patched, err := RepairTOMLFile(path, schema)
		if err != nil {
			if _, statErr := os.Stat(path); statErr == nil {
				fmt.Fprintln(cfgOpts.Logger, "auto-repair skipped (write access denied, likely read-only filesystem), continuing startup:", err)
			} else {
				return nil, fmt.Errorf("failed to auto-repair configuration file: %w", err)
			}
		} else if patched {
			fmt.Fprintln(cfgOpts.Logger, "configuration has been automatically patched with missing keys")
		}

		examplePath := path + ".example"
		exampleContent := GenerateExample(schema)
		_ = os.WriteFile(examplePath, []byte(exampleContent), 0644)
	}

	var cfg T
	if err := cleanenv.ReadConfig(path, &cfg); err != nil {
		return nil, fmt.Errorf("failed to decode configuration via cleanenv: %w", err)
	}

	if err := SetDefaultsRecursive(&cfg); err != nil {
		return nil, err
	}

	resolver, err := NewFallbackResolver(reflect.ValueOf(&cfg))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize fallback resolver: %w", err)
	}
	if err := resolver.Resolve(); err != nil {
		return nil, fmt.Errorf("failed to resolve configuration fallbacks: %w", err)
	}

	if err := ValidateRecursive(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}
