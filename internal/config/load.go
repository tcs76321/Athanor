// Loading and defaults for the Athanor configuration (ARCHITECTURE §29).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// Sentinel errors callers can match on for distinct failure modes.
var (
	ErrFileNotFound = errors.New("config file not found")
	ErrEmptyConfig  = errors.New("config file is empty")
)

// Categories is the closed set of event-log categories (ARCHITECTURE §28.1).
var Categories = []string{
	"jobs", "recovery", "alarms", "airlock", "network", "podman",
	"inference", "feedback", "strategy", "context", "daydream",
	"power", "backup",
}

// Load reads, parses, applies defaults to, and validates the configuration
// at path. Each failure mode produces a distinct error.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrFileNotFound, path)
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, err
	}
	cfg.SourcePath = path
	return cfg, nil
}

// Default returns the fully-defaulted configuration (the same result Load
// produces for a config file that specifies nothing). It exists so the
// daemon can boot on a fresh clone before the user has written a
// config.yaml — explicit absence falls back to defaults with a notice,
// while a present-but-invalid file still fails loudly.
func Default() (*Config, error) {
	cfg := &Config{}
	if err := validateRaw(cfg); err != nil {
		return nil, fmt.Errorf("default config invalid (bug in defaults.go): %w", err)
	}
	applyDefaults(cfg)
	if err := validateCross(cfg); err != nil {
		return nil, fmt.Errorf("default config invalid (bug in defaults.go): %w", err)
	}
	return cfg, nil
}

// Parse decodes raw YAML bytes, applies defaults for omitted optional
// fields, and validates the result.
func Parse(data []byte) (*Config, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, ErrEmptyConfig
	}
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // reject unknown keys outright
	if err := dec.Decode(&cfg); err != nil {
		var terr *yaml.TypeError
		if errors.As(err, &terr) {
			return nil, fmt.Errorf("wrong-typed config: %w", err)
		}
		return nil, fmt.Errorf("malformed config: %w", err)
	}
	// A config is one document. A second document used to be silently
	// ignored, which made a duplicated or stray block look accepted.
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, fmt.Errorf("malformed config: multiple YAML documents (one config document is allowed)")
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("malformed config: %w", err)
	}
	if err := validateRaw(&cfg); err != nil {
		return nil, err
	}
	applyDefaults(&cfg)
	if err := validateCross(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
