package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ilyakaznacheev/cleanenv"
)

// envPrefixModule mirrors a platform module config: relative env names only.
// The owning (embedding) struct is responsible for namespacing via env-prefix.
type envPrefixModule struct {
	Port string `toml:"port" env:"PORT"`
}

type envPrefixRoot struct {
	A envPrefixModule `toml:"a" env-prefix:"A_"`
	B envPrefixModule `toml:"b" env-prefix:"B_"`
}

// TestEnvPrefixNamespacing pins the platform env-var convention: modules declare
// relative names and the embedding struct namespaces them via env-prefix, so
// multiple instances of the same module are driven by distinct variables and
// an ambient bare variable cannot leak into module configs.
func TestEnvPrefixNamespacing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.toml")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("A_PORT", "8080")
	t.Setenv("B_PORT", "9090")
	t.Setenv("PORT", "1234")

	var cfg envPrefixRoot
	if err := cleanenv.ReadConfig(path, &cfg); err != nil {
		t.Fatalf("cleanenv.ReadConfig: %v", err)
	}

	if cfg.A.Port != "8080" {
		t.Errorf("A.Port = %q, want 8080 (A_PORT)", cfg.A.Port)
	}
	if cfg.B.Port != "9090" {
		t.Errorf("B.Port = %q, want 9090 (B_PORT)", cfg.B.Port)
	}
	if cfg.A.Port == "1234" || cfg.B.Port == "1234" {
		t.Errorf("ambient bare PORT leaked into module configs: A=%q B=%q", cfg.A.Port, cfg.B.Port)
	}
}
