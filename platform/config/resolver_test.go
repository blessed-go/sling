package config_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/blessed-go/sling/platform/config"
)

// Test helper structures for resolver tests.

type DBConfigTest struct {
	Host string `toml:"host"`
	Port int    `toml:"port"`
}

type SimpleFallbackConfig struct {
	Primary   DBConfigTest `toml:"primary"`
	Secondary DBConfigTest `toml:"secondary" fallback:"primary"`
}

type ChainedFallbackConfig struct {
	Replica DBConfigTest `toml:"replica" fallback:"primary"`
	Primary DBConfigTest `toml:"primary" fallback:"global"`
	Global  DBConfigTest `toml:"global"`
}

type CircularA struct {
	Value string `toml:"val" fallback:"b.val"`
}
type CircularB struct {
	Value string `toml:"val" fallback:"a.val"`
}
type CircularConfig struct {
	A CircularA `toml:"a"`
	B CircularB `toml:"b"`
}

type DeepCircularConfig struct {
	A string `toml:"a" fallback:"b"`
	B string `toml:"b" fallback:"c"`
	C string `toml:"c" fallback:"a"`
}

type OptionalPointerConfig struct {
	GlobalHost string             `toml:"global_host"`
	Optional   *NestedWithDefault `toml:"optional"`
}

type NestedWithDefault struct {
	Host string `toml:"host" fallback:"global_host"`
}

type PointerToPointerConfig struct {
	Primary   *DBConfigTest `toml:"primary"`
	Secondary *DBConfigTest `toml:"secondary" fallback:"primary"`
}

type CustomString string

type CustomTypeConfig struct {
	Source string       `toml:"source"`
	Dest   CustomString `toml:"dest" fallback:"source"`
}

// TestFallbackResolver_Simple verifies value copying and deep struct merging.
func TestFallbackResolver_Simple(t *testing.T) {
	cfg := SimpleFallbackConfig{
		Primary: DBConfigTest{
			Host: "prod-db-primary",
			Port: 5432,
		},
		Secondary: DBConfigTest{
			Host: "",   // Should be populated from Primary.Host via deep struct merge.
			Port: 9999, // Should NOT be overwritten since value is non-zero.
		},
	}

	resolver, err := config.NewFallbackResolver(reflect.ValueOf(&cfg))
	if err != nil {
		t.Fatalf("failed to create resolver: %v", err)
	}

	if err := resolver.Resolve(); err != nil {
		t.Fatalf("unexpected resolve error: %v", err)
	}

	// Verify empty Host field was copied in nested merge.
	if cfg.Secondary.Host != "prod-db-primary" {
		t.Errorf("expected Secondary.Host to be 'prod-db-primary', got '%s'", cfg.Secondary.Host)
	}

	// Verify non-empty Port preserved user value.
	if cfg.Secondary.Port != 9999 {
		t.Errorf("expected Secondary.Port to preserve user value 9999, got %d", cfg.Secondary.Port)
	}
}

// TestFallbackResolver_Chained verifies evaluation of chained DAG dependencies.
func TestFallbackResolver_Chained(t *testing.T) {
	cfg := ChainedFallbackConfig{
		Global: DBConfigTest{
			Host: "global-domain-controller",
			Port: 3306,
		},
		Primary: DBConfigTest{
			Host: "",
			Port: 0,
		},
		Replica: DBConfigTest{
			Host: "",
			Port: 0,
		},
	}

	resolver, err := config.NewFallbackResolver(reflect.ValueOf(&cfg))
	if err != nil {
		t.Fatalf("failed to create resolver: %v", err)
	}

	if err := resolver.Resolve(); err != nil {
		t.Fatalf("unexpected resolve error: %v", err)
	}

	if cfg.Primary.Host != "global-domain-controller" || cfg.Primary.Port != 3306 {
		t.Errorf("Primary was not resolved correctly: %+v", cfg.Primary)
	}

	if cfg.Replica.Host != "global-domain-controller" || cfg.Replica.Port != 3306 {
		t.Errorf("Replica was not resolved correctly. Chained evaluation failed: %+v", cfg.Replica)
	}
}

// TestFallbackResolver_Circular verifies detection of simple cyclic dependencies A -> B -> A.
func TestFallbackResolver_Circular(t *testing.T) {
	cfg := CircularConfig{
		A: CircularA{Value: ""},
		B: CircularB{Value: ""},
	}

	resolver, err := config.NewFallbackResolver(reflect.ValueOf(&cfg))
	if err != nil {
		t.Fatalf("failed to create resolver: %v", err)
	}

	err = resolver.Resolve()
	if err == nil {
		t.Fatal("expected circular dependency error, but resolution completed successfully")
	}

	expectedErrSubStr := "circular fallback dependency"
	if !strings.Contains(err.Error(), expectedErrSubStr) {
		t.Errorf("expected error to contain %q, got: %q", expectedErrSubStr, err.Error())
	}
}

// TestFallbackResolver_DeepCircular verifies detection of deep circular dependencies A -> B -> C -> A.
func TestFallbackResolver_DeepCircular(t *testing.T) {
	cfg := DeepCircularConfig{
		A: "",
		B: "",
		C: "",
	}

	resolver, err := config.NewFallbackResolver(reflect.ValueOf(&cfg))
	if err != nil {
		t.Fatalf("failed to create resolver: %v", err)
	}

	err = resolver.Resolve()
	if err == nil {
		t.Fatal("expected circular dependency error, but resolution completed successfully")
	}

	expectedErrSubStr := "circular fallback dependency"
	if !strings.Contains(err.Error(), expectedErrSubStr) {
		t.Errorf("expected error to contain %q, got: %q", expectedErrSubStr, err.Error())
	}
}

// TestFallbackResolver_NilPointerSafety verifies that uninitialized nil pointers are safely ignored.
func TestFallbackResolver_NilPointerSafety(t *testing.T) {
	cfg := OptionalPointerConfig{
		GlobalHost: "cloud-lb-address",
		Optional:   nil, // Disabled section; recursive resolver must not panic.
	}

	resolver, err := config.NewFallbackResolver(reflect.ValueOf(&cfg))
	if err != nil {
		t.Fatalf("failed to create resolver: %v", err)
	}

	// Optional nil pointer -> resolution must succeed without forced allocation.
	if err := resolver.Resolve(); err != nil {
		t.Fatalf("unexpected resolve error: %v", err)
	}

	if cfg.Optional != nil {
		t.Fatal("expected Optional pointer to remain nil, but it was force-allocated")
	}
}

// TestFallbackResolver_PointerToPointer verifies pointer-to-pointer fallbacks.
func TestFallbackResolver_PointerToPointer(t *testing.T) {
	primary := &DBConfigTest{
		Host: "primary-ptr-host",
		Port: 8888,
	}

	cfg := PointerToPointerConfig{
		Primary:   primary,
		Secondary: nil, // Should copy pointer from Primary.
	}

	resolver, err := config.NewFallbackResolver(reflect.ValueOf(&cfg))
	if err != nil {
		t.Fatalf("failed to create resolver: %v", err)
	}

	if err := resolver.Resolve(); err != nil {
		t.Fatalf("unexpected resolve error: %v", err)
	}

	if cfg.Secondary == nil {
		t.Fatal("expected Secondary pointer to be resolved from Primary, but got nil")
	}

	if cfg.Secondary.Host != "primary-ptr-host" || cfg.Secondary.Port != 8888 {
		t.Errorf("Secondary pointer resolved with incorrect values: %+v", cfg.Secondary)
	}
}

// TestFallbackResolver_TypeConvertibility verifies fallbacks between compatible named types.
func TestFallbackResolver_TypeConvertibility(t *testing.T) {
	cfg := CustomTypeConfig{
		Source: "service-mesh-value",
		Dest:   "",
	}

	resolver, err := config.NewFallbackResolver(reflect.ValueOf(&cfg))
	if err != nil {
		t.Fatalf("failed to create resolver: %v", err)
	}

	if err := resolver.Resolve(); err != nil {
		t.Fatalf("unexpected resolve error: %v", err)
	}

	if cfg.Dest != "service-mesh-value" {
		t.Errorf("expected Dest (CustomString) to receive converted value, got '%v'", cfg.Dest)
	}
}
