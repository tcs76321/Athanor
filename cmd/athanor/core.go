package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Core Pod runtime helpers (ADR-0078). The Core Pod is the supported daemon
// runtime; the host `serve` path is a development escape hatch. This file
// owns the container lifecycle so `install.sh` and `athanor start` share one
// implementation.
//
// It uses the allowlisted `execClient` (cmd/athanor/jobpod_client.go), so
// Gate G1 has no new os/exec surface.

const (
	defaultCoreImage = "athanor-core:latest"
	defaultCoreName  = "athanor-core"
	defaultStateVol  = "athanor-state"
	defaultWorkVol   = "athanor-workspace"
	defaultCoreAddr  = "127.0.0.1:7420"
)

// coreOptions is one Core Pod launch.
type coreOptions struct {
	Image           string
	Name            string
	ConfigPath      string
	StateVolume     string
	WorkspaceVolume string
	TokenDir        string
	ScratchDir      string
	SocketPath      string
	Addr            string
}

// coreRunArgs is the pure `podman run` argv for a Core Pod. It is separated
// from the runner so tests can pin the mounts and the loopback publish
// without a container runtime.
func coreRunArgs(o coreOptions) []string {
	return []string{
		"run", "-d",
		"--name", o.Name,
		"--security-opt", "label=disable",
		"-p", o.Addr + ":7420",
		"-v", o.SocketPath + ":/run/podman/podman.sock",
		"-v", o.StateVolume + ":/var/lib/athanor/state",
		"-v", o.TokenDir + ":/var/lib/athanor/state/tokens",
		"-v", o.ScratchDir + ":/var/lib/athanor/state/scratch",
		"-v", o.WorkspaceVolume + ":/var/lib/athanor/workspace",
		"-v", o.ConfigPath + ":/etc/athanor/config.yaml:ro",
		o.Image,
	}
}

// coreSocketPath resolves the machine socket the Core Pod mounts. macOS
// exposes it through `podman machine ssh`; Linux uses XDG_RUNTIME_DIR (or the
// uid fallback). An explicit -socket always wins.
func coreSocketPath(ctx context.Context, client *execClient, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if runtime.GOOS == "darwin" {
		out, stderr, err := client.Run(ctx, "machine", "ssh", `printf %s "$XDG_RUNTIME_DIR"`)
		if err != nil {
			return "", fmt.Errorf("resolving the podman machine socket: %w: %s", err, strings.TrimSpace(string(stderr)))
		}
		dir := strings.TrimSpace(string(out))
		if dir == "" {
			return "", fmt.Errorf("podman machine ssh returned an empty XDG_RUNTIME_DIR (is the machine running?)")
		}
		return filepath.Join(dir, "podman", "podman.sock"), nil
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = fmt.Sprintf("/run/user/%d", os.Getuid())
	}
	return filepath.Join(dir, "podman", "podman.sock"), nil
}

// sameImage tolerates podman's registry-qualified form
// (localhost/athanor-core:latest) when the configured value is the short tag.
func sameImage(have, want string) bool {
	if have == want {
		return true
	}
	return strings.HasSuffix(have, "/"+want)
}

// coreUp ensures the image exists, starts (or reuses) the named Core
// container, and waits for /healthz. Starting an already-running container is
// idempotent so `athanor start` can be re-run.
func coreUp(ctx context.Context, o coreOptions) error {
	client := NewExecClient()
	if _, stderr, err := client.Run(ctx, "image", "exists", o.Image); err != nil {
		return fmt.Errorf("core image %q not found (build it with `make core-image`): %s", o.Image, strings.TrimSpace(string(stderr)))
	}
	if _, _, err := client.Run(ctx, "container", "exists", o.Name); err == nil {
		out, stderr, err := client.Run(ctx, "container", "inspect", "--format", "{{.Config.Image}}", o.Name)
		if err != nil {
			return fmt.Errorf("inspecting existing container %s: %w: %s", o.Name, err, strings.TrimSpace(string(stderr)))
		}
		image := strings.TrimSpace(string(out))
		if !sameImage(image, o.Image) {
			return fmt.Errorf("container %q already exists with image %q, not %q; remove it or pass -container-name", o.Name, image, o.Image)
		}
		if _, stderr, err := client.Run(ctx, "start", o.Name); err != nil {
			return fmt.Errorf("starting Core container %s: %w: %s", o.Name, err, strings.TrimSpace(string(stderr)))
		}
	} else {
		for _, vol := range []string{o.StateVolume, o.WorkspaceVolume} {
			if _, _, err := client.Run(ctx, "volume", "exists", vol); err == nil {
				continue
			}
			if _, stderr, err := client.Run(ctx, "volume", "create", vol); err != nil {
				return fmt.Errorf("creating volume %s: %w: %s", vol, err, strings.TrimSpace(string(stderr)))
			}
		}
		if err := os.MkdirAll(o.TokenDir, 0o700); err != nil {
			return fmt.Errorf("creating token directory %s: %w", o.TokenDir, err)
		}
		if err := os.MkdirAll(o.ScratchDir, 0o755); err != nil {
			return fmt.Errorf("creating scratch directory %s: %w", o.ScratchDir, err)
		}
		if _, stderr, err := client.Run(ctx, coreRunArgs(o)...); err != nil {
			return fmt.Errorf("starting Core Pod: %w: %s", err, strings.TrimSpace(string(stderr)))
		}
	}
	return waitCoreHealth(ctx, o.Addr)
}

// waitCoreHealth polls the loopback health endpoint for up to 60 seconds.
func waitCoreHealth(ctx context.Context, addr string) error {
	url := "http://" + addr + "/healthz"
	deadline := time.Now().Add(60 * time.Second)
	probes := newOSProbes()
	for {
		var body map[string]any
		if err := probes.getJSON(ctx, url, &body); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("core Pod did not become healthy at %s within 60s", url)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// coreOptionsFromConfig builds launch options from the loaded config and the
// start flags. The token host directory must be set: the Core writes tokens
// into its state volume, while the Podman machine must be able to see the
// same path for the Job Pod bind mount.
func coreOptionsFromConfig(cfgTokenDir, cfgScratchDir, configPath, addr, image, name, stateVol, workVol, socket string) (coreOptions, error) {
	if cfgTokenDir == "" {
		return coreOptions{}, fmt.Errorf("job_pod.token_host_dir is empty: set it to a host-visible directory (e.g. ~/.config/athanor/tokens) for Core Pod mode")
	}
	if cfgScratchDir == "" {
		return coreOptions{}, fmt.Errorf("job_pod.scratch_host_dir is empty: set it to a host-visible directory (e.g. ~/.config/athanor/scratch) for Core Pod mode")
	}
	absConfig, err := filepath.Abs(configPath)
	if err != nil {
		return coreOptions{}, err
	}
	absTokens, err := filepath.Abs(cfgTokenDir)
	if err != nil {
		return coreOptions{}, err
	}
	absScratch, err := filepath.Abs(cfgScratchDir)
	if err != nil {
		return coreOptions{}, err
	}
	return coreOptions{
		Image:           image,
		Name:            name,
		ConfigPath:      absConfig,
		StateVolume:     stateVol,
		WorkspaceVolume: workVol,
		TokenDir:        absTokens,
		ScratchDir:      absScratch,
		SocketPath:      socket,
		Addr:            addr,
	}, nil
}
