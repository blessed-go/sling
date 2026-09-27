package config

import (
	"reflect"
	"strings"
	"testing"
)

type SimpleTestConfig struct {
	Host    string `toml:"host" env-default:"localhost" comment:"IP or domain"`
	Port    int    `toml:"port" envDefault:"8080"`
	Enabled bool   `toml:"enabled" env-default:"true"`
}

type DeepTestConfig struct {
	AppName string        `toml:"app_name" env-default:"my-app"`
	DB      *DBConfig     `toml:"database"`
	Metrics MetricsConfig `toml:"metrics"`
}

type DBConfig struct {
	DSN  string      `toml:"dsn" env-default:"postgres://localhost"`
	Pool *PoolConfig `toml:"pool"`
}

type PoolConfig struct {
	MaxConns int `toml:"max_conns" env-default:"10" comment:"Max open connections"`
}

type MetricsConfig struct {
	Interval string `toml:"interval" env-default:"10s"`
}

type IgnoredFieldsConfig struct {
	ExportedField   string `toml:"exported" env-default:"ok"`
	unexportedField string `toml:"private" env-default:"should_skip"`
	IgnoredField    string `toml:"-" env-default:"should_skip"`
}

type InvalidMutualConfig struct {
	Host string `toml:"host" env-default:"localhost" fallback:"global.host"`
}

type ZeroValuesConfig struct {
	IntField   int            `toml:"int_field"`
	BoolField  bool           `toml:"bool_field"`
	StrField   string         `toml:"str_field"`
	SliceField []int          `toml:"slice_field"`
	MapField   map[string]any `toml:"map_field"`
}

func TestBuildSchema_Simple(t *testing.T) {
	typ := reflect.TypeOf(SimpleTestConfig{})
	schema, err := BuildSchema(typ, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if schema == nil {
		t.Fatal("expected schema to be non-nil")
	}

	if len(schema.Fields) != 3 {
		t.Errorf("expected 3 fields, got %d", len(schema.Fields))
	}

	// Verify field mappings.
	fieldsMap := make(map[string]SchemaField)
	for _, f := range schema.Fields {
		fieldsMap[f.Key] = f
	}

	host, exists := fieldsMap["host"]
	if !exists {
		t.Fatal("missing field 'host' in schema")
	}
	if host.DefaultVal != "localhost" {
		t.Errorf("expected default 'localhost', got '%s'", host.DefaultVal)
	}
	if host.Comment != "IP or domain" {
		t.Errorf("expected comment 'IP or domain', got '%s'", host.Comment)
	}
	if host.Path != "host" {
		t.Errorf("expected path 'host', got '%s'", host.Path)
	}

	port, exists := fieldsMap["port"]
	if !exists {
		t.Fatal("missing field 'port' in schema")
	}
	if port.DefaultVal != "8080" {
		t.Errorf("expected default '8080' from alternative tag envDefault, got '%s'", port.DefaultVal)
	}
}

// TestBuildSchema_DeepNesting verifies recursive schema building for pointer fields and paths.
func TestBuildSchema_DeepNesting(t *testing.T) {
	typ := reflect.TypeOf(DeepTestConfig{})
	schema, err := BuildSchema(typ, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(schema.Fields) != 1 || schema.Fields[0].Key != "app_name" {
		t.Errorf("expected only 'app_name' at root level, got: %v", schema.Fields)
	}

	dbNode, exists := schema.Children["database"]
	if !exists {
		t.Fatal("missing nested child 'database'")
	}
	if dbNode.Section != "database" {
		t.Errorf("expected section path 'database', got '%s'", dbNode.Section)
	}

	if len(dbNode.Fields) != 1 || dbNode.Fields[0].Key != "dsn" {
		t.Errorf("expected 'dsn' field inside database node, got: %v", dbNode.Fields)
	}
	if dbNode.Fields[0].Path != "database.dsn" {
		t.Errorf("expected absolute path 'database.dsn', got '%s'", dbNode.Fields[0].Path)
	}

	poolNode, exists := dbNode.Children["pool"]
	if !exists {
		t.Fatal("missing nested child 'database.pool'")
	}
	if poolNode.Section != "database.pool" {
		t.Errorf("expected section path 'database.pool', got '%s'", poolNode.Section)
	}
	if len(poolNode.Fields) != 1 || poolNode.Fields[0].Key != "max_conns" {
		t.Errorf("expected 'max_conns' field inside pool, got: %v", poolNode.Fields)
	}
	if poolNode.Fields[0].Path != "database.pool.max_conns" {
		t.Errorf("expected absolute path 'database.pool.max_conns', got '%s'", poolNode.Fields[0].Path)
	}
	if poolNode.Fields[0].Comment != "Max open connections" {
		t.Errorf("expected comment 'Max open connections', got '%s'", poolNode.Fields[0].Comment)
	}

	metricsNode, exists := schema.Children["metrics"]
	if !exists {
		t.Fatal("missing nested child 'metrics'")
	}
	if metricsNode.Section != "metrics" {
		t.Errorf("expected section path 'metrics', got '%s'", metricsNode.Section)
	}
	if len(metricsNode.Fields) != 1 || metricsNode.Fields[0].Key != "interval" {
		t.Errorf("expected 'interval' field, got: %v", metricsNode.Fields)
	}
}

// TestBuildSchema_IgnoredFields verifies skipping unexported fields and "-" tagged fields.
func TestBuildSchema_IgnoredFields(t *testing.T) {
	typ := reflect.TypeOf(IgnoredFieldsConfig{})
	schema, err := BuildSchema(typ, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(schema.Fields) != 1 {
		t.Fatalf("expected exactly 1 field in schema, got %d", len(schema.Fields))
	}

	if schema.Fields[0].Key != "exported" {
		t.Errorf("expected field 'exported' to be parsed, but got '%s'", schema.Fields[0].Key)
	}
}

// TestBuildSchema_MutualExclusivity verifies error handling on env-default and fallback collisions.
func TestBuildSchema_MutualExclusivity(t *testing.T) {
	typ := reflect.TypeOf(InvalidMutualConfig{})
	_, err := BuildSchema(typ, "")
	if err == nil {
		t.Fatal("expected error due to mutual exclusivity of env-default and fallback, but got nil")
	}

	expectedSubStr := "mutual exclusivity violation"
	if !strings.Contains(err.Error(), expectedSubStr) {
		t.Errorf("expected error message to contain %q, got: %q", expectedSubStr, err.Error())
	}
}

// TestGetZeroValueString verifies default string placeholder generation for types without explicit defaults.
func TestGetZeroValueString(t *testing.T) {
	typ := reflect.TypeOf(ZeroValuesConfig{})
	schema, err := BuildSchema(typ, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fieldsMap := make(map[string]SchemaField)
	for _, f := range schema.Fields {
		fieldsMap[f.Key] = f
	}

	tests := []struct {
		key          string
		expectedZero string
	}{
		{"int_field", "0"},
		{"bool_field", "false"},
		{"str_field", "\"\""},
		{"slice_field", "[]"},
		{"map_field", "{}"},
	}

	for _, tc := range tests {
		field, exists := fieldsMap[tc.key]
		if !exists {
			t.Fatalf("missing field %s in schema", tc.key)
		}
		zeroStr := GetZeroValueString(field.Kind)
		if zeroStr != tc.expectedZero {
			t.Errorf("for field %s expected zero string %q, got %q", tc.key, tc.expectedZero, zeroStr)
		}
	}
}

func TestBuildSchema_NilType(t *testing.T) {
	_, err := BuildSchema(nil, "")
	if err == nil {
		t.Fatal("expected error when passing nil type to BuildSchema, got nil")
	}
	expectedMsg := "config: schema target type cannot be nil"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("expected error message %q, got %q", expectedMsg, err.Error())
	}
}

