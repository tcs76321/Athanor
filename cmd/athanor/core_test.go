package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/config"
)

func TestCoreRunArgs(t *testing.T) {
	o := coreOptions{
		Image:           "athanor-core:latest",
		Name:            "athanor-core",
		ConfigPath:      "/home/u/.config/athanor/config.yaml",
		StateVolume:     "athanor-state",
		WorkspaceVolume: "athanor-workspace",
		TokenDir:        "/home/u/.config/athanor/tokens",
		SocketPath:      "/run/user/1000/podman/podman.sock",
		Addr:            "127.0.0.1:7420",
	}
	args := strings.Join(coreRunArgs(o), " ")
	for _, want := range []string{
		"run -d",
		"--name athanor-core",
		"-p 127.0.0.1:7420:7420",
		"-v /run/user/1000/podman/podman.sock:/run/podman/podman.sock",
		"-v athanor-state:/var/lib/athanor/state",
		"-v /home/u/.config/athanor/tokens:/var/lib/athanor/state/tokens",
		"-v athanor-workspace:/var/lib/athanor/workspace",
		"-v /home/u/.config/athanor/config.yaml:/etc/athanor/config.yaml:ro",
		"athanor-core:latest",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("coreRunArgs missing %q in %q", want, args)
		}
	}
}

func TestCoreOptionsRequireTokenDir(t *testing.T) {
	_, err := coreOptionsFromConfig("", "config.yaml", "127.0.0.1:7420",
		"img", "name", "state", "work", "/sock")
	if err == nil || !strings.Contains(err.Error(), "token_host_dir") {
		t.Fatalf("coreOptionsFromConfig(empty token dir) = %v, want a token_host_dir error", err)
	}
}

func TestHostDoctorConfigRewritesContainerAlias(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Inference.OllamaURL = "http://host.containers.internal:11434"
	got := hostDoctorConfig(cfg)
	if got.Inference.OllamaURL != "http://127.0.0.1:11434" {
		t.Errorf("host doctor URL = %q, want loopback", got.Inference.OllamaURL)
	}
	if cfg.Inference.OllamaURL != "http://host.containers.internal:11434" {
		t.Error("hostDoctorConfig mutated the original config")
	}
}

func TestInitCoreWritesContainerConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := runInit([]string{"-out", path, "-core", "-token-host-dir", "/tmp/athanor-tokens"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Inference.OllamaURL != "http://host.containers.internal:11434" {
		t.Errorf("ollama_url = %q, want the Core Pod alias", cfg.Inference.OllamaURL)
	}
	if cfg.JobPod.Image == "" || cfg.JobPod.TokenHostDir != "/tmp/athanor-tokens" {
		t.Errorf("job_pod = %+v, want image and token host dir", cfg.JobPod)
	}
}

// TestCoreSocketExplicit pins the -socket escape hatch without touching
// podman.
func TestCoreSocketExplicit(t *testing.T) {
	got, err := coreSocketPath(context.Background(), nil, "/tmp/podman.sock")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/podman.sock" {
		t.Errorf("socket = %q", got)
	}
}

func TestAllowlistContains(t *testing.T) {
	if !allowlistContains(nil, "127.0.0.1:7421") {
		t.Error("empty allowlist should accept any address")
	}
	list := []string{"127.0.0.1:7420", "localhost:7420"}
	if !allowlistContains(list, "127.0.0.1:7420") {
		t.Error("allowlisted address rejected")
	}
	if allowlistContains(list, "127.0.0.1:7421") {
		t.Error("non-allowlisted address accepted")
	}
}
