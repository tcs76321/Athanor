package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/tcs76321/athanor/internal/config"
	"gopkg.in/yaml.v3"
)

// defaultAddr is the daemon's default loopback address (§21.8).
const defaultAddr = "http://127.0.0.1:7420"

// apiCall performs one JSON request against the daemon and decodes the
// response into out (when non-nil).
func apiCall(method, url string, body any, out any) error {
	var reqBody io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("daemon unreachable at %s (is `athanor serve` running?): %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &errBody)
		if errBody.Error != "" {
			return fmt.Errorf("%s", errBody.Error)
		}
		return fmt.Errorf("daemon returned %s: %s", resp.Status, raw)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// criteriaFlag parses a semicolon-separated acceptance-criteria list.
type criteriaFlag []string

func (c *criteriaFlag) String() string { return strings.Join(*c, "; ") }
func (c *criteriaFlag) Set(v string) error {
	for _, s := range strings.FieldsFunc(v, func(r rune) bool { return r == ';' || r == '\n' }) {
		if s = strings.TrimSpace(s); s != "" {
			*c = append(*c, s)
		}
	}
	return nil
}

// runInit writes a config.yaml containing every default value (M1-T7:
// fresh clone → running daemon in under 5 minutes of user time).
func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	path := fs.String("out", "config.yaml", "where to write the config")
	core := fs.Bool("core", false, "write a Core Pod config (host.containers.internal Ollama URL + token host dir)")
	tokenDir := fs.String("token-host-dir", "", "Core Pod: host-visible token directory (default ~/.config/athanor/tokens)")
	scratchDir := fs.String("scratch-host-dir", "", "Core Pod: host-visible scratch directory (default: next to the config)")
	jobPodImage := fs.String("jobpod-image", "athanor-jobpod:latest", "Core Pod: Job Pod base image")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := os.Stat(*path); err == nil {
		return fmt.Errorf("%s already exists (refusing to overwrite)", *path)
	}
	cfg, err := config.Default()
	if err != nil {
		return err
	}
	if *core {
		cfg.Inference.OllamaURL = "http://host.containers.internal:11434"
		cfg.JobPod.Image = *jobPodImage
		dir := *tokenDir
		if dir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("resolving the home directory for the token dir: %w", err)
			}
			dir = filepath.Join(home, ".config", "athanor", "tokens")
		}
		cfg.JobPod.TokenHostDir = dir
		scratch := *scratchDir
		if scratch == "" {
			scratch = filepath.Join(filepath.Dir(*path), "scratch")
		}
		cfg.JobPod.ScratchHostDir = scratch
	}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*path, raw, 0o600); err != nil {
		return err
	}
	fmt.Printf("wrote %s (every default; edit what you need, delete the rest)\n", *path)
	return nil
}
