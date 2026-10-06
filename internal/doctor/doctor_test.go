package doctor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/config"
)

// fakeProbes is the injected OS boundary. Each field seeds one fault.
type fakeProbes struct {
	paths       map[string]string
	commands    map[string]string
	commandErrs map[string]error
	version     string
	versionErr  error
	models      []string
	modelsErr   error
	mem         uint64
	memOK       bool
	disk        uint64
	diskOK      bool
	writableErr error
}

func (f *fakeProbes) LookPath(name string) (string, error) {
	if p, ok := f.paths[name]; ok {
		return p, nil
	}
	return "", fmt.Errorf("%s: not found", name)
}

func (f *fakeProbes) Command(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name + " " + strings.Join(args, " ")
	if err, ok := f.commandErrs[key]; ok {
		return nil, err
	}
	if out, ok := f.commands[key]; ok {
		return []byte(out), nil
	}
	return nil, fmt.Errorf("unexpected command %q", key)
}

func (f *fakeProbes) OllamaVersion(context.Context, string) (string, error) {
	return f.version, f.versionErr
}

func (f *fakeProbes) OllamaModels(context.Context, string) ([]string, error) {
	return f.models, f.modelsErr
}

func (f *fakeProbes) TotalMemoryBytes(context.Context) (uint64, bool) { return f.mem, f.memOK }

func (f *fakeProbes) FreeDiskBytes(context.Context, string) (uint64, bool) { return f.disk, f.diskOK }

func (f *fakeProbes) DirWritable(string) error { return f.writableErr }

// goodProbes returns a healthy host with all persona models installed.
func goodProbes() *fakeProbes {
	return &fakeProbes{
		paths:    map[string]string{"podman": "/usr/local/bin/podman", "git": "/usr/bin/git"},
		commands: map[string]string{"podman info --format {{.Host.Security.Rootless}}": "true\n"},
		version:  "0.35.1",
		models: []string{
			"qwen2.5:7b", "qwen2.5-coder:32b", "mistral-nemo:12b", "phi3:3.8b", "llama3.1:8b",
		},
		mem:    32 << 30,
		memOK:  true,
		disk:   100 << 30,
		diskOK: true,
	}
}

// goodConfig is the default config with the two personas the shipped
// defaults under-provision (for the code floor) raised, so the baseline has
// no warnings.
func goodConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("config.Default: %v", err)
	}
	cfg.Personas.Security.ContextTarget = 32768
	cfg.Personas.Tall.ContextTarget = 32768
	cfg.JobPod.Image = "python:3.12-alpine"
	return cfg
}

func runAll(t *testing.T, cfg *config.Config, p *fakeProbes) Report {
	t.Helper()
	return Run(context.Background(), cfg, Options{StateDir: t.TempDir()}, p)
}

func TestRunAllGood(t *testing.T) {
	cfg := goodConfig(t)
	rep := runAll(t, cfg, goodProbes())
	if !rep.OK() {
		t.Fatalf("expected all checks to pass, got: %+v", rep.Checks)
	}
	if ok, warn, fail := rep.Counts(); fail != 0 || warn != 0 {
		t.Fatalf("counts = ok:%d warn:%d fail:%d, want no warn/fail: %+v", ok, warn, fail, rep.Checks)
	}
}

func TestRunMissingPodman(t *testing.T) {
	p := goodProbes()
	delete(p.paths, "podman")
	rep := runAll(t, goodConfig(t), p)
	if rep.OK() {
		t.Fatal("expected failure when podman is absent")
	}
	if c := findCheck(rep, "podman"); c == nil || c.Status != StatusFail || !strings.Contains(c.Remediation, "install rootless Podman") {
		t.Fatalf("podman check = %+v", c)
	}
}

func TestRunPodmanNotRootless(t *testing.T) {
	p := goodProbes()
	p.commands["podman info --format {{.Host.Security.Rootless}}"] = "false\n"
	rep := runAll(t, goodConfig(t), p)
	c := findCheck(rep, "podman")
	if c == nil || c.Status != StatusFail || !strings.Contains(c.Detail, "not running rootless") {
		t.Fatalf("podman check = %+v", c)
	}
}

func TestRunPodmanInfoError(t *testing.T) {
	p := goodProbes()
	p.commandErrs = map[string]error{"podman info --format {{.Host.Security.Rootless}}": fmt.Errorf("machine not running")}
	rep := runAll(t, goodConfig(t), p)
	if c := findCheck(rep, "podman"); c == nil || c.Status != StatusFail {
		t.Fatalf("podman check = %+v", c)
	}
}

func TestRunMissingGit(t *testing.T) {
	p := goodProbes()
	delete(p.paths, "git")
	rep := runAll(t, goodConfig(t), p)
	if c := findCheck(rep, "git"); c == nil || c.Status != StatusFail {
		t.Fatalf("git check = %+v", c)
	}
}

func TestRunOllamaUnreachable(t *testing.T) {
	p := goodProbes()
	p.versionErr = fmt.Errorf("connection refused")
	rep := runAll(t, goodConfig(t), p)
	c := findCheck(rep, "ollama")
	if c == nil || c.Status != StatusFail || !strings.Contains(c.Detail, "unreachable") {
		t.Fatalf("ollama check = %+v", c)
	}
}

func TestRunMissingModel(t *testing.T) {
	p := goodProbes()
	p.models = []string{"qwen2.5:7b", "qwen2.5-coder:32b", "mistral-nemo:12b", "llama3.1:8b"}
	rep := runAll(t, goodConfig(t), p)
	c := findCheck(rep, "models")
	if c == nil || c.Status != StatusFail {
		t.Fatalf("models check = %+v", c)
	}
	if !strings.Contains(c.Remediation, "ollama pull phi3:3.8b") {
		t.Fatalf("remediation = %q, want an ollama pull for phi3:3.8b", c.Remediation)
	}
}

func TestRunModelsLatestTagTolerated(t *testing.T) {
	cfg := goodConfig(t)
	cfg.Personas.Alternative.Model = "llama3.1"
	p := goodProbes()
	p.models = []string{"qwen2.5:7b", "qwen2.5-coder:32b", "mistral-nemo:12b", "phi3:3.8b", "llama3.1:latest"}
	rep := runAll(t, cfg, p)
	if c := findCheck(rep, "models"); c == nil || c.Status != StatusOK {
		t.Fatalf("models check = %+v (llama3.1:latest should satisfy llama3.1)", c)
	}
}

func TestRunLowMemory(t *testing.T) {
	p := goodProbes()
	p.mem = 4 << 30
	rep := runAll(t, goodConfig(t), p)
	c := findCheck(rep, "memory")
	if c == nil || c.Status != StatusWarn || !strings.Contains(c.Detail, "below") {
		t.Fatalf("memory check = %+v", c)
	}
}

func TestRunLowDisk(t *testing.T) {
	p := goodProbes()
	p.disk = 1 << 30
	rep := runAll(t, goodConfig(t), p)
	if c := findCheck(rep, "disk"); c == nil || c.Status != StatusWarn {
		t.Fatalf("disk check = %+v", c)
	}
}

func TestRunUnwritableStateDir(t *testing.T) {
	p := goodProbes()
	p.writableErr = fmt.Errorf("permission denied")
	rep := runAll(t, goodConfig(t), p)
	if c := findCheck(rep, "state_dir"); c == nil || c.Status != StatusFail {
		t.Fatalf("state_dir check = %+v", c)
	}
}

func TestRunEmptyJobPodImage(t *testing.T) {
	cfg := goodConfig(t)
	cfg.JobPod.Image = ""
	rep := runAll(t, cfg, goodProbes())
	if c := findCheck(rep, "job_pod_image"); c == nil || c.Status != StatusWarn {
		t.Fatalf("job_pod_image check = %+v", c)
	}
}

func TestRunContextBelowFloor(t *testing.T) {
	cfg := goodConfig(t)
	cfg.Personas.Security.ContextTarget = 8192
	rep := runAll(t, cfg, goodProbes())
	c := findCheck(rep, "context:security")
	if c == nil || c.Status != StatusWarn {
		t.Fatalf("context:security check = %+v", c)
	}
	if !strings.Contains(c.Remediation, "8192") && !strings.Contains(c.Remediation, "32768") {
		t.Fatalf("remediation should name the floor or target: %q", c.Remediation)
	}
	// A context shortfall is advisory, not a hard failure.
	if !rep.OK() {
		t.Fatalf("context shortfall must not fail doctor: %+v", rep.Checks)
	}
}

func TestRunNetworkNotDeny(t *testing.T) {
	cfg := goodConfig(t)
	cfg.Network.DefaultPolicy = "allow"
	rep := runAll(t, cfg, goodProbes())
	if c := findCheck(rep, "network"); c == nil || c.Status != StatusWarn {
		t.Fatalf("network check = %+v", c)
	}
}

func TestRunNilConfig(t *testing.T) {
	rep := Run(context.Background(), nil, Options{}, goodProbes())
	if rep.OK() {
		t.Fatal("nil config must fail")
	}
	if c := findCheck(rep, "config"); c == nil || c.Status != StatusFail {
		t.Fatalf("config check = %+v", c)
	}
}

func TestModelPresent(t *testing.T) {
	cases := []struct {
		want    string
		have    []string
		present bool
	}{
		{"qwen2.5:7b", []string{"qwen2.5:7b"}, true},
		{"llama3.1", []string{"llama3.1:latest"}, true},
		{"llama3.1:latest", []string{"llama3.1"}, true},
		{"phi3:3.8b", []string{"qwen2.5:7b"}, false},
		{"qwen2.5:7b", nil, false},
	}
	for _, c := range cases {
		if got := modelPresent(c.want, c.have); got != c.present {
			t.Errorf("modelPresent(%q, %v) = %v, want %v", c.want, c.have, got, c.present)
		}
	}
}

func findCheck(rep Report, name string) *Check {
	for i := range rep.Checks {
		if rep.Checks[i].Name == name {
			return &rep.Checks[i]
		}
	}
	return nil
}
