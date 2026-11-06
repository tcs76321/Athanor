package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/doctor"
)

// runDoctor implements `athanor doctor` (ROADMAP M7-T5; ARCHITECTURE §30.2).
// It loads the config, runs every check through the OS probes, prints an
// aligned report, and exits non-zero when any check fails.
func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	configPath := fs.String("config", "config.yaml", "path to config.yaml")
	stateDir := fs.String("state-dir", "state", "state directory (database, logs, backups)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	probes := newOSProbes()
	// A host-side doctor inspecting a Core Pod config should probe the host
	// loopback, not the container-only host.containers.internal alias.
	if !probes.InContainer() {
		cfg = hostDoctorConfig(cfg)
	}
	rep := doctor.Run(context.Background(), cfg, doctor.Options{StateDir: *stateDir}, probes)
	printReport(rep)
	if !rep.OK() {
		_, _, fail := rep.Counts()
		return fmt.Errorf("doctor: %d check(s) failed", fail)
	}
	return nil
}

// printReport renders the check table. Remediations print on their own line
// so long §12.3 recommendations stay readable.
func printReport(rep doctor.Report) {
	for _, c := range rep.Checks {
		label := "ok  "
		switch c.Status {
		case doctor.StatusWarn:
			label = "warn"
		case doctor.StatusFail:
			label = "FAIL"
		}
		fmt.Printf("[%s] %-16s %s\n", label, c.Name, c.Detail)
		if c.Remediation != "" && c.Status != doctor.StatusOK {
			fmt.Printf("       %-16s   -> %s\n", "", c.Remediation)
		}
	}
	ok, warn, fail := rep.Counts()
	fmt.Printf("athanor doctor: %d ok, %d warn, %d fail\n", ok, warn, fail)
}

// osProbes is the production doctor.Probes implementation: the only place
// doctor touches the OS (os/exec, HTTP, /proc, sysctl). Gate G1 allowlists
// this file by name (internal/gate/gate_test.go).
type osProbes struct {
	client *http.Client
}

func newOSProbes() *osProbes {
	return &osProbes{client: &http.Client{Timeout: 5 * time.Second}}
}

func (osProbes) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (osProbes) Command(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// getJSON performs one loopback/LAN GET and decodes the JSON body.
func (o *osProbes) getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s", url, resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func (o *osProbes) OllamaVersion(ctx context.Context, baseURL string) (string, error) {
	var body struct {
		Version string `json:"version"`
	}
	if err := o.getJSON(ctx, strings.TrimRight(baseURL, "/")+"/api/version", &body); err != nil {
		return "", err
	}
	return body.Version, nil
}

func (o *osProbes) OllamaModels(ctx context.Context, baseURL string) ([]string, error) {
	var body struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := o.getJSON(ctx, strings.TrimRight(baseURL, "/")+"/api/tags", &body); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(body.Models))
	for _, m := range body.Models {
		if m.Name != "" {
			out = append(out, m.Name)
		} else {
			out = append(out, m.Model)
		}
	}
	return out, nil
}

func (o *osProbes) OllamaResident(ctx context.Context, baseURL string) ([]string, error) {
	var body struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := o.getJSON(ctx, strings.TrimRight(baseURL, "/")+"/api/ps", &body); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(body.Models))
	for _, m := range body.Models {
		if m.Name != "" {
			out = append(out, m.Name)
		}
	}
	return out, nil
}

// TotalMemoryBytes reads physical memory (macOS sysctl, Linux /proc/meminfo).
func (osProbes) TotalMemoryBytes(ctx context.Context) (uint64, bool) {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.CommandContext(ctx, "sysctl", "-n", "hw.memsize").Output()
		if err != nil {
			return 0, false
		}
		n, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	case "linux":
		raw, err := os.ReadFile("/proc/meminfo")
		if err != nil {
			return 0, false
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.HasPrefix(line, "MemTotal:") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return 0, false
			}
			kb, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0, false
			}
			return kb * 1024, true
		}
		return 0, false
	default:
		return 0, false
	}
}

// FreeDiskBytes runs `df -k` and parses the available column.
func (osProbes) FreeDiskBytes(ctx context.Context, path string) (uint64, bool) {
	out, err := exec.CommandContext(ctx, "df", "-k", path).Output()
	if err != nil {
		return 0, false
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return 0, false
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 4 {
		return 0, false
	}
	kb, err := strconv.ParseUint(fields[3], 10, 64)
	if err != nil {
		return 0, false
	}
	return kb * 1024, true
}

// DirWritable creates the directory (if needed) and writes a probe file.
func (osProbes) DirWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".athanor-doctor-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

// InContainer reports whether the daemon runs inside a Core Pod. Podman writes
// /run/.containerenv and Docker writes /.dockerenv (M9-T7; ADR-0069).
func (osProbes) InContainer() bool {
	for _, marker := range []string{"/run/.containerenv", "/.dockerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	return false
}

// GPU reports an NVIDIA GPU through nvidia-smi when available. A 1080 Ti-class
// Linux host is a supported target; no GPU simply means CPU inference.
func (osProbes) GPU(ctx context.Context) (doctor.GPUInfo, bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	info := doctor.GPUInfo{NPU: detectNPU(ctx)}
	if runtime.GOOS == "darwin" {
		info = appleGPU(ctx, info)
	} else {
		info = nvidiaGPU(ctx, info)
		if info.Name == "" {
			info = amdGPU(ctx, info)
		}
	}
	return info, info.Name != ""
}

func nvidiaGPU(ctx context.Context, info doctor.GPUInfo) doctor.GPUInfo {
	out, err := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=name,memory.total", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return info
	}
	var first bool
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, ",")
		if len(parts) < 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		if name == "" {
			continue
		}
		if !first {
			info.Name = name
			info.Kind = "cuda"
			info.Backend = "CUDA"
			if mb, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
				info.MemoryMiB = mb
			}
			first = true
		}
		info.Devices++
	}
	return info
}

func amdGPU(ctx context.Context, info doctor.GPUInfo) doctor.GPUInfo {
	out, err := exec.CommandContext(ctx, "rocm-smi", "--showproductname", "--showmeminfo", "vram").Output()
	if err != nil {
		return info
	}
	names := map[string]string{}
	memory := map[string]int{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "GPU[") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		id := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		switch {
		case strings.Contains(value, "Card series:") || strings.Contains(value, "Card model:"):
			names[id] = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(value, "Card series:"), "Card model:"))
		case strings.Contains(value, "VRAM Total Memory (B):"):
			raw := strings.TrimSpace(strings.TrimPrefix(value, "VRAM Total Memory (B):"))
			if b, err := strconv.ParseInt(raw, 10, 64); err == nil {
				memory[id] = int(b / (1 << 20))
			}
		}
	}
	for id, name := range names {
		if info.Name == "" {
			info.Name = name
			info.Kind = "rocm"
			info.Backend = "ROCm"
			info.MemoryMiB = memory[id]
		}
		info.Devices++
	}
	return info
}

func appleGPU(ctx context.Context, info doctor.GPUInfo) doctor.GPUInfo {
	out, err := exec.CommandContext(ctx, "system_profiler", "-json", "SPDisplaysDataType").Output()
	if err != nil {
		return info
	}
	var doc struct {
		Displays []struct {
			Model  string `json:"sppci_model"`
			VRAM   string `json:"spdisplays_vram"`
			Shared string `json:"spdisplays_vram_shared"`
		} `json:"SPDisplaysDataType"`
	}
	if json.Unmarshal(out, &doc) != nil {
		return info
	}
	for _, d := range doc.Displays {
		if d.Model == "" {
			continue
		}
		if info.Name == "" {
			info.Name = d.Model
			info.Kind = "metal"
			info.Backend = "Metal"
			info.Unified = d.Shared != "" || strings.Contains(strings.ToLower(d.Model), "apple")
			if mb, ok := parseMemoryMiB(d.VRAM); ok {
				info.MemoryMiB = mb
			} else if mb, ok := parseMemoryMiB(d.Shared); ok {
				info.MemoryMiB = mb
			}
		}
		info.Devices++
	}
	return info
}

func parseMemoryMiB(s string) (int, bool) {
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, false
	}
	switch strings.ToUpper(fields[1]) {
	case "GB":
		return n * 1024, true
	case "MB":
		return n, true
	default:
		return 0, false
	}
}

func detectNPU(ctx context.Context) string {
	if runtime.GOOS == "darwin" {
		return ""
	}
	out, err := exec.CommandContext(ctx, "lspci").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "neural") || strings.Contains(lower, "npu") || strings.Contains(lower, "ai accelerator") {
			if i := strings.Index(line, ": "); i >= 0 {
				return strings.TrimSpace(line[i+2:])
			}
			return strings.TrimSpace(line)
		}
	}
	return ""
}
