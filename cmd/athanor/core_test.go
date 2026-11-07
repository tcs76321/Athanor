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
		ScratchDir:      "/home/u/.config/athanor/scratch",
		SocketPath:      "/run/user/1000/podman/podman.sock",
		Addr:            "127.0.0.1:7420",
		HostMemoryBytes: 34359738368,
		AcceleratorJSON: `{"Name":"RTX 4090","MemoryMiB":24576,"Kind":"cuda","Backend":"CUDA"}`,
	}
	args := strings.Join(coreRunArgs(o), " ")
	for _, want := range []string{
		"run -d",
		"--name athanor-core",
		"-p 127.0.0.1:7420:7420",
		"-v /run/user/1000/podman/podman.sock:/run/podman/podman.sock",
		"-v athanor-state:/var/lib/athanor/state",
		"-v /home/u/.config/athanor/tokens:/var/lib/athanor/state/tokens",
		"-v /home/u/.config/athanor/scratch:/var/lib/athanor/state/scratch",
		"-v athanor-workspace:/var/lib/athanor/workspace",
		"-v /home/u/.config/athanor/config.yaml:/etc/athanor/config.yaml",
		"--security-opt no-new-privileges",
		"--pids-limit 4096",
		"-e ATHANOR_HOST_MEMORY_BYTES=34359738368",
		`-e ATHANOR_ACCELERATOR={"Name":"RTX 4090","MemoryMiB":24576,"Kind":"cuda","Backend":"CUDA"}`,
		"athanor-core:latest",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("coreRunArgs missing %q in %q", want, args)
		}
	}
}

func TestCoreMountsMatch(t *testing.T) {
	e := coreOptions{
		ConfigPath:      "/home/u/.config/athanor/config.yaml",
		StateVolume:     "athanor-state",
		WorkspaceVolume: "athanor-workspace",
		TokenDir:        "/home/u/.config/athanor/tokens",
		ScratchDir:      "/home/u/.config/athanor/scratch",
		SocketPath:      "/run/user/1000/podman/podman.sock",
	}
	desired := coreDesiredMounts(e)
	current := []coreMount{
		{Name: "athanor-state", Destination: "/var/lib/athanor/state"},
		{Name: "athanor-workspace", Destination: "/var/lib/athanor/workspace"},
		{Source: "/run/user/1000/podman/podman.sock", Destination: "/run/podman/podman.sock"},
		{Source: "/home/u/.config/athanor/tokens", Destination: "/var/lib/athanor/state/tokens"},
		{Source: "/home/u/.config/athanor/scratch", Destination: "/var/lib/athanor/state/scratch"},
		{Source: "/home/u/.config/athanor/config.yaml", Destination: "/etc/athanor/config.yaml"},
	}
	if !coreMountsMatch(current, desired) {
		t.Fatal("matching mounts reported stale")
	}
	current[5].Source = "/home/u/old-config.yaml"
	if coreMountsMatch(current, desired) {
		t.Fatal("changed config bind reported as matching")
	}
}

func TestCoreOptionsRequireTokenDir(t *testing.T) {
	_, err := coreOptionsFromConfig("", "scratch", "config.yaml", "127.0.0.1:7420",
		"img", "name", "state", "work", "/sock")
	if err == nil || !strings.Contains(err.Error(), "token_host_dir") {
		t.Fatalf("coreOptionsFromConfig(empty token dir) = %v, want a token_host_dir error", err)
	}
	_, err = coreOptionsFromConfig("tokens", "", "config.yaml", "127.0.0.1:7420",
		"img", "name", "state", "work", "/sock")
	if err == nil || !strings.Contains(err.Error(), "scratch_host_dir") {
		t.Fatalf("coreOptionsFromConfig(empty scratch dir) = %v, want a scratch_host_dir error", err)
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
	if cfg.JobPod.Image == "" || cfg.JobPod.TokenHostDir != "/tmp/athanor-tokens" || cfg.JobPod.ScratchHostDir == "" {
		t.Errorf("job_pod = %+v, want image, token host dir, and scratch host dir", cfg.JobPod)
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
