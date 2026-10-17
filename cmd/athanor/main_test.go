package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/config"
)

// TestLoadConfigMissingFallsBackToDefaults proves a fresh clone (no
// config.yaml) still boots: M1-T7 requires daemon startup with zero manual
// configuration steps.
func TestLoadConfigMissingFallsBackToDefaults(t *testing.T) {
	cfg, err := loadConfig(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("loadConfig(missing) err = %v, want nil (defaults fallback)", err)
	}
	if cfg == nil {
		t.Fatal("loadConfig(missing) = nil config")
	}
	if cfg.Inference.DefaultBackend != "ollama" {
		t.Errorf("default backend = %q, want ollama", cfg.Inference.DefaultBackend)
	}
	if cfg.Personas.Security.Model == "" {
		t.Error("defaults not applied to personas")
	}
	if cfg.SourcePath != "" {
		t.Errorf("SourcePath = %q, want empty for fallback config", cfg.SourcePath)
	}
}

// TestLoadConfigInvalidStillFails proves that only *absence* falls back:
// a file that exists but is semantically invalid fails loudly.
func TestLoadConfigInvalidStillFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := writeFile(path, "version: 99\n"); err != nil {
		t.Fatal(err)
	}
	_, err := loadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "unsupported config version") {
		t.Fatalf("loadConfig(invalid) err = %v, want version error", err)
	}
}

// TestRunInvalidConfigRejected proves the boot sequence aborts on a bad
// config before touching the filesystem beyond reading it.
func TestRunInvalidConfigRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	bad := "version: 99\n"
	if err := writeFile(path, bad); err != nil {
		t.Fatal(err)
	}
	err := run(path, "127.0.0.1:0", filepath.Join(dir, "state"), false)
	if err == nil || !strings.Contains(err.Error(), "loading config") {
		t.Fatalf("err = %v, want config loading failure", err)
	}
}

// TestRunMalformedConfigRejected proves malformed YAML (as opposed to
// semantically invalid) also aborts boot with a specific error.
func TestRunMalformedConfigRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := writeFile(path, "version: 2\n  bad indent: ["); err != nil {
		t.Fatal(err)
	}
	err := run(path, "127.0.0.1:0", filepath.Join(dir, "state"), false)
	if err == nil || !strings.Contains(err.Error(), "loading config") {
		t.Fatalf("err = %v, want config loading failure", err)
	}
}

// TestLoadConfigValidFileWins proves an existing config file is used as-is
// (the fallback must never mask a user's configuration).
func TestLoadConfigValidFileWins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := "version: 2\nlogging:\n  level: debug\n"
	if err := writeFile(path, body); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig(valid) err = %v, want nil", err)
	}
	if cfg.SourcePath != path {
		t.Errorf("SourcePath = %q, want %q", cfg.SourcePath, path)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("logging.level = %q, want debug (file value)", cfg.Logging.Level)
	}
}

// TestConfigErrFileNotFoundIsSentinel guards the sentinel error contract
// loadConfig depends on.
func TestConfigErrFileNotFoundIsSentinel(t *testing.T) {
	_, err := config.Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if !errors.Is(err, config.ErrFileNotFound) {
		t.Fatalf("Load(missing) err = %v, want ErrFileNotFound", err)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}
