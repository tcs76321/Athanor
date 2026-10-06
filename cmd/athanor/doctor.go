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
	rep := doctor.Run(context.Background(), cfg, doctor.Options{StateDir: *stateDir}, newOSProbes())
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
