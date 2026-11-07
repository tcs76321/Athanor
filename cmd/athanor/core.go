package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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
	// ReposDir is a host-visible directory mounted at the same absolute path
	// inside the Core. The Core cannot read an arbitrary host path through a
	// named volume; a repository-backed project works when its path lives
	// under this directory.
	ReposDir   string
	SocketPath string
	Addr       string
	// HostMemoryBytes is the physical host's memory, passed to the Core so
	// model-fit budgets do not mistake the Podman VM's memory for the host's.
	HostMemoryBytes uint64
	// ModelAudit is the measured model-fit JSON, passed to the Core so the UI
	// can show the same plan the startup guard enforced.
	ModelAudit string
	// AcceleratorJSON is the host accelerator probe result. The Core Pod
	// cannot see the host GPU/NPU, so the host passes what it found into the
	// Core's resource planning.
	AcceleratorJSON string
}

// coreRunArgs is the pure `podman run` argv for a Core Pod. It is separated
// from the runner so tests can pin the mounts and the loopback publish
// without a container runtime.
func coreRunArgs(o coreOptions) []string {
	args := []string{
		"run", "-d",
		"--name", o.Name,
		"--security-opt", "label=disable",
		"--security-opt", "no-new-privileges",
		"--pids-limit", "4096",
		"-p", o.Addr + ":7420",
		"-v", o.SocketPath + ":/run/podman/podman.sock",
		"-v", o.StateVolume + ":/var/lib/athanor/state",
		"-v", o.TokenDir + ":/var/lib/athanor/state/tokens",
		"-v", o.ScratchDir + ":/var/lib/athanor/state/scratch",
		"-v", o.WorkspaceVolume + ":/var/lib/athanor/workspace",
	}
	if o.ReposDir != "" {
		args = append(args, "-v", o.ReposDir+":"+o.ReposDir)
	}
	args = append(args,
		"-v", o.ConfigPath+":/etc/athanor/config.yaml",
	)
	if o.HostMemoryBytes > 0 {
		args = append(args, "-e", "ATHANOR_HOST_MEMORY_BYTES="+strconv.FormatUint(o.HostMemoryBytes, 10))
	}
	if o.ModelAudit != "" {
		args = append(args, "-e", "ATHANOR_MODEL_AUDIT="+o.ModelAudit)
	}
	if o.AcceleratorJSON != "" {
		args = append(args, "-e", "ATHANOR_ACCELERATOR="+o.AcceleratorJSON)
	}
	return append(args, o.Image)
}

// coreMount is one inspected mount entry. Named volumes carry Name; bind
// mounts carry Source. Destination is the stable key for comparison.
type coreMount struct {
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
}

// coreDesiredMounts is the mount contract for a Core Pod. A container created
// with a different config path, token/scratch directory, socket, or volume set
// cannot be repaired by `podman start`; it must be recreated.
func coreDesiredMounts(o coreOptions) []coreMount {
	mounts := []coreMount{
		{Name: o.StateVolume, Destination: "/var/lib/athanor/state"},
		{Name: o.WorkspaceVolume, Destination: "/var/lib/athanor/workspace"},
		{Source: o.SocketPath, Destination: "/run/podman/podman.sock"},
		{Source: o.TokenDir, Destination: "/var/lib/athanor/state/tokens"},
		{Source: o.ScratchDir, Destination: "/var/lib/athanor/state/scratch"},
		{Source: o.ConfigPath, Destination: "/etc/athanor/config.yaml"},
	}
	if o.ReposDir != "" {
		mounts = append(mounts, coreMount{Source: o.ReposDir, Destination: o.ReposDir})
	}
	return mounts
}

// coreMountsMatch reports whether the existing container has the mounts the
// current configuration requires. Named volumes compare by name; bind mounts
// compare by canonical source.
func coreMountsMatch(current, desired []coreMount) bool {
	if len(current) != len(desired) {
		return false
	}
	byDest := make(map[string]coreMount, len(current))
	for _, m := range current {
		byDest[m.Destination] = m
	}
	for _, want := range desired {
		got, ok := byDest[want.Destination]
		if !ok {
			return false
		}
		if want.Name != "" {
			if got.Name != want.Name {
				return false
			}
			continue
		}
		if !samePath(got.Source, want.Source) {
			return false
		}
	}
	return true
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

// coreUp ensures the image exists, starts (or reuses) the named Core
// container, and waits for /healthz. A container built from a different image
// ID or with a different mount contract is recreated rather than silently
// started with stale code or stale paths; the state and workspace volumes are
// preserved.
func coreUp(ctx context.Context, o coreOptions) error {
	client := NewExecClient()
	out, stderr, err := client.Run(ctx, "image", "inspect", "--format", "{{.Id}}", o.Image)
	if err != nil {
		return fmt.Errorf("core image %q not found (build it with `make core-image`): %s", o.Image, strings.TrimSpace(string(stderr)))
	}
	imageID := strings.TrimSpace(string(out))

	recreate := false
	if _, _, err := client.Run(ctx, "container", "exists", o.Name); err == nil {
		idOut, idStderr, idErr := client.Run(ctx, "container", "inspect", "--format", "{{.Image}}", o.Name)
		if idErr != nil {
			return fmt.Errorf("inspecting existing container %s: %w: %s", o.Name, idErr, strings.TrimSpace(string(idStderr)))
		}
		currentID := strings.TrimSpace(string(idOut))
		mountOut, mountStderr, mountErr := client.Run(ctx, "container", "inspect", "--format", "{{json .Mounts}}", o.Name)
		var currentMounts []coreMount
		if mountErr != nil {
			_ = mountStderr
			recreate = true
		} else if json.Unmarshal(mountOut, &currentMounts) != nil {
			recreate = true
		} else {
			recreate = currentID != imageID || !coreMountsMatch(currentMounts, coreDesiredMounts(o))
		}
		if !recreate {
			if _, stderr, err := client.Run(ctx, "start", o.Name); err != nil {
				return fmt.Errorf("starting Core container %s: %w: %s", o.Name, err, strings.TrimSpace(string(stderr)))
			}
			return waitCoreHealthOrLog(ctx, client, o)
		}
		fmt.Printf("recreating Core Pod %s: image or mount contract changed\n", o.Name)
		if _, stderr, err := client.Run(ctx, "rm", "-f", o.Name); err != nil {
			return fmt.Errorf("removing stale Core container %s: %w: %s", o.Name, err, strings.TrimSpace(string(stderr)))
		}
	}

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
	if err := os.MkdirAll(o.ScratchDir, 0o700); err != nil {
		return fmt.Errorf("creating scratch directory %s: %w", o.ScratchDir, err)
	}
	if o.ReposDir != "" {
		if err := os.MkdirAll(o.ReposDir, 0o755); err != nil {
			return fmt.Errorf("creating repositories directory %s: %w", o.ReposDir, err)
		}
	}
	if _, stderr, err := client.Run(ctx, coreRunArgs(o)...); err != nil {
		return fmt.Errorf("starting Core Pod: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	return waitCoreHealthOrLog(ctx, client, o)
}

// waitCoreHealthOrLog turns a health timeout into an actionable error that
// includes the container's last log lines. First-use failures should not
// require a separate `podman logs` investigation.
func waitCoreHealthOrLog(ctx context.Context, client *execClient, o coreOptions) error {
	if err := waitCoreHealth(ctx, o.Addr); err != nil {
		logs, _, _ := client.Run(ctx, "logs", "--tail", "80", o.Name)
		if len(logs) > 0 {
			return fmt.Errorf("%w\n--- Core Pod logs ---\n%s", err, strings.TrimSpace(string(logs)))
		}
		return err
	}
	return nil
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
func coreOptionsFromConfig(cfgTokenDir, cfgScratchDir, reposDir, configPath, addr, image, name, stateVol, workVol, socket string) (coreOptions, error) {
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
	absRepos := ""
	if reposDir != "" {
		absRepos, err = filepath.Abs(reposDir)
		if err != nil {
			return coreOptions{}, err
		}
	}
	return coreOptions{
		Image:           image,
		Name:            name,
		ConfigPath:      absConfig,
		StateVolume:     stateVol,
		WorkspaceVolume: workVol,
		TokenDir:        absTokens,
		ScratchDir:      absScratch,
		ReposDir:        absRepos,
		SocketPath:      socket,
		Addr:            addr,
	}, nil
}
