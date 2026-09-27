package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Test data structures for repair verification.

type RepairSSLConfig struct {
	Enabled bool   `toml:"enabled" env-default:"true" comment:"Enable SSL/TLS connection"`
	Cert    string `toml:"cert" env-default:"/etc/ssl/cert.pem"`
}

type RepairAppConfig struct {
	AppName string          `toml:"app_name" env-default:"demo-app"`
	Port    int             `toml:"port" env-default:"8080" comment:"Server listening port"`
	SSL     RepairSSLConfig `toml:"ssl"`
}

// TestRepairTOMLFile_NewFile verifies auto-generation of a config file when absent on disk.
func TestRepairTOMLFile_NewFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "config_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "subdir", "config.toml")

	schema, err := BuildSchema(reflect.TypeOf(RepairAppConfig{}), "")
	if err != nil {
		t.Fatalf("failed to build schema: %v", err)
	}

	patched, err := RepairTOMLFile(configPath, schema)
	if err != nil {
		t.Fatalf("failed to repair TOML file: %v", err)
	}

	if !patched {
		t.Error("expected patched to be true when generating a new file")
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatalf("config file was not created on disk: %s", configPath)
	}

	contentBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read generated config: %v", err)
	}
	content := string(contentBytes)

	// Accept double or single quotes for string values depending on parser output.
	hasAppName := strings.Contains(content, "app_name = \"demo-app\"") || strings.Contains(content, "app_name = 'demo-app'")
	if !hasAppName {
		t.Errorf("missing app_name field, got content:\n%s", content)
	}

	// Port must be written strictly as an unquoted numeric literal.
	if !strings.Contains(content, "port = 8080") {
		t.Errorf("expected numeric port 8080 without quotes, got content:\n%s", content)
	}

	// Boolean field must be written strictly without quotes.
	if !strings.Contains(content, "enabled = true") {
		t.Errorf("expected boolean enabled = true without quotes, got content:\n%s", content)
	}

	if !strings.Contains(content, "# Enable SSL/TLS connection") {
		t.Errorf("missing ssl comment, got content:\n%s", content)
	}
}

// TestRepairTOMLFile_PreserveExistingAndInjectMissing verifies non-destructive schema merging.
func TestRepairTOMLFile_PreserveExistingAndInjectMissing(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "config_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "config.toml")

	originalUserContent := `# === MY CUSTOM LOCAL CONFIG ===
# Please do not delete this comment.

app_name = "my-custom-prod-app" #inline comment for app_name

[ssl]
   # Custom padding inside section
   cert = "/var/certs/prod.pem"
`

	if err := os.WriteFile(configPath, []byte(originalUserContent), 0644); err != nil {
		t.Fatalf("failed to write original user config: %v", err)
	}

	schema, err := BuildSchema(reflect.TypeOf(RepairAppConfig{}), "")
	if err != nil {
		t.Fatalf("failed to build schema: %v", err)
	}

	patched, err := RepairTOMLFile(configPath, schema)
	if err != nil {
		t.Fatalf("failed to repair TOML file: %v", err)
	}

	if !patched {
		t.Error("expected patched to be true because port and ssl.enabled were injected")
	}

	repairedBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read repaired config: %v", err)
	}
	repairedContent := string(repairedBytes)

	if !strings.Contains(repairedContent, "# === MY CUSTOM LOCAL CONFIG ===") {
		t.Error("user manual top header comment was destroyed!")
	}

	if !strings.Contains(repairedContent, `#inline comment for app_name`) {
		t.Error("inline user comment was destroyed!")
	}

	if !strings.Contains(repairedContent, `cert = "/var/certs/prod.pem"`) {
		t.Error("user custom 'cert' value was overwritten by default value!")
	}

	if !strings.Contains(repairedContent, "   # Custom padding inside section") {
		t.Error("user custom formatting padding was corrupted during AST editing!")
	}

	if !strings.Contains(repairedContent, "port = 8080") {
		t.Error("missing root field 'port' was not injected or injected with quotes")
	}
	if !strings.Contains(repairedContent, "# Server listening port") {
		t.Error("comment for injected field 'port' was not applied")
	}

	if !strings.Contains(repairedContent, "enabled = true") {
		t.Error("missing nested field 'ssl.enabled' was not injected or injected with quotes")
	}
	if !strings.Contains(repairedContent, "# Enable SSL/TLS connection") {
		t.Error("comment for injected nested field 'ssl.enabled' was not applied")
	}
}
