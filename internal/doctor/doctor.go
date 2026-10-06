// Package doctor implements the §30.2 first-run diagnostics: a set of
// checks over the host, the inference backend, the configuration, and the
// context-feasibility rules, each with an actionable remediation.
//
// The package is pure orchestration over a Probes interface (the OS/backend
// boundary). The production probes (os/exec, HTTP, /proc, sysctl) live in
// cmd/athanor/doctor.go so this package — and every other internal/ package
// — stays free of process execution, preserving Gate G1 (ROADMAP §3). Tests
// inject a fake Probes and seed one fault per check.
//
// Check severities:
//
//   - fail: a hard prerequisite (Podman, Git, Ollama, a persona model, a
//     writable state dir). The daemon cannot do useful work without it.
//   - warn: a soft problem or an advisory (low memory, low disk, no Job Pod
//     image, a context target below the archetype floor). The daemon can
//     still boot; the operator is told what it will cost.
//   - ok: verified.
package doctor

import (
	"context"
	"fmt"
	"strings"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/llm"
)

// Status is a check outcome.
type Status string

const (
	StatusOK   Status = "ok"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// Advisory thresholds. Local-first inference wants headroom: below 8 GiB the
// model set in §12.2 will not co-exist; below 5 GiB of free disk the SQLite
// store, artifacts, backups, and Job Pod images are cramped.
const (
	minMemoryBytes = 8 << 30
	minDiskBytes   = 5 << 30
)

// Check is one diagnostic result.
type Check struct {
	Name        string
	Status      Status
	Detail      string
	Remediation string
}

// Report is the ordered set of checks doctor ran.
type Report struct {
	Checks []Check
}

func (r *Report) add(c Check) { r.Checks = append(r.Checks, c) }

// OK reports whether no check failed.
func (r Report) OK() bool {
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			return false
		}
	}
	return true
}

// Counts returns the number of ok, warn, and fail checks.
func (r Report) Counts() (ok, warn, fail int) {
	for _, c := range r.Checks {
		switch c.Status {
		case StatusFail:
			fail++
		case StatusWarn:
			warn++
		default:
			ok++
		}
	}
	return
}

// Probes is the OS/backend boundary doctor reads through. The production
// implementation is cmd/athanor/doctor.go; tests supply a fake.
type Probes interface {
	// LookPath finds an executable on PATH.
	LookPath(name string) (string, error)
	// Command runs a command and returns its combined output.
	Command(ctx context.Context, name string, args ...string) ([]byte, error)
	// OllamaVersion returns the inference backend's version string.
	OllamaVersion(ctx context.Context, baseURL string) (string, error)
	// OllamaModels returns the installed model names (e.g. "qwen2.5:7b").
	OllamaModels(ctx context.Context, baseURL string) ([]string, error)
	// TotalMemoryBytes returns physical memory, or ok=false when unknown.
	TotalMemoryBytes(ctx context.Context) (uint64, bool)
	// FreeDiskBytes returns free space at path, or ok=false when unknown.
	FreeDiskBytes(ctx context.Context, path string) (uint64, bool)
	// DirWritable verifies the directory can be created and written.
	DirWritable(path string) error
}

// Options are the paths doctor inspects.
type Options struct {
	StateDir string
}

// persona is one role's model and context target.
type persona struct {
	Role          string
	Model         string
	ContextTarget int
}

// personas returns the five fixed roles in §12.1 order.
func personas(cfg *config.Config) []persona {
	return []persona{
		{llm.RoleWide, cfg.Personas.Wide.Model, cfg.Personas.Wide.ContextTarget},
		{llm.RoleTall, cfg.Personas.Tall.Model, cfg.Personas.Tall.ContextTarget},
		{llm.RoleMain, cfg.Personas.Main.Model, cfg.Personas.Main.ContextTarget},
		{llm.RoleSecurity, cfg.Personas.Security.Model, cfg.Personas.Security.ContextTarget},
		{llm.RoleAlternative, cfg.Personas.Alternative.Model, cfg.Personas.Alternative.ContextTarget},
	}
}

// Run performs every check in a fixed order and returns the report. A nil
// config is reported as a single failure rather than panicking.
func Run(ctx context.Context, cfg *config.Config, opts Options, p Probes) Report {
	var r Report
	if cfg == nil {
		r.add(Check{Name: "config", Status: StatusFail, Detail: "no configuration loaded"})
		return r
	}
	r.checkPodman(ctx, p)
	r.checkGit(p)
	r.checkOllama(ctx, cfg, p)
	r.checkMemory(ctx, p)
	r.checkDisk(ctx, opts, p)
	r.checkStateDir(opts, p)
	r.checkJobPodImage(cfg)
	r.checkContext(cfg)
	r.checkNetwork(cfg)
	r.checkPower(cfg)
	return r
}

// checkPodman verifies Podman is installed and running rootless (§21.1).
func (r *Report) checkPodman(ctx context.Context, p Probes) {
	path, err := p.LookPath("podman")
	if err != nil {
		r.add(Check{
			Name:        "podman",
			Status:      StatusFail,
			Detail:      "podman not found on PATH",
			Remediation: "install rootless Podman (https://podman.io/); on macOS also run `podman machine init && podman machine start`",
		})
		return
	}
	out, err := p.Command(ctx, "podman", "info", "--format", "{{.Host.Security.Rootless}}")
	if err != nil {
		r.add(Check{
			Name:        "podman",
			Status:      StatusFail,
			Detail:      fmt.Sprintf("podman info failed: %v", err),
			Remediation: "ensure the Podman machine/service is running; verify `podman info` succeeds as this user",
		})
		return
	}
	if strings.TrimSpace(string(out)) != "true" {
		r.add(Check{
			Name:        "podman",
			Status:      StatusFail,
			Detail:      "podman is not running rootless",
			Remediation: "configure rootless Podman; Athanor never runs privileged containers (§21.1)",
		})
		return
	}
	r.add(Check{Name: "podman", Status: StatusOK, Detail: path + " (rootless)"})
}

// checkGit verifies Git is available (§14 Git-as-undo; §30.2).
func (r *Report) checkGit(p Probes) {
	path, err := p.LookPath("git")
	if err != nil {
		r.add(Check{
			Name:        "git",
			Status:      StatusFail,
			Detail:      "git not found on PATH",
			Remediation: "install Git (required for atomic agent commits on agent branches, §14)",
		})
		return
	}
	r.add(Check{Name: "git", Status: StatusOK, Detail: path})
}

// checkOllama verifies the backend is reachable and every persona model is
// installed. A missing model is a hard failure with the exact `ollama pull`
// remediation (§30.2).
func (r *Report) checkOllama(ctx context.Context, cfg *config.Config, p Probes) {
	ver, err := p.OllamaVersion(ctx, cfg.Inference.OllamaURL)
	if err != nil {
		r.add(Check{
			Name:        "ollama",
			Status:      StatusFail,
			Detail:      fmt.Sprintf("unreachable at %s: %v", cfg.Inference.OllamaURL, err),
			Remediation: "start Ollama and verify inference.ollama_url (default http://host.containers.internal:11434)",
		})
		return
	}
	r.add(Check{Name: "ollama", Status: StatusOK, Detail: fmt.Sprintf("%s at %s", ver, cfg.Inference.OllamaURL)})

	models, err := p.OllamaModels(ctx, cfg.Inference.OllamaURL)
	if err != nil {
		r.add(Check{Name: "models", Status: StatusWarn, Detail: fmt.Sprintf("could not list models: %v", err)})
		return
	}
	var missing []string
	for _, pr := range personas(cfg) {
		if pr.Model != "" && !modelPresent(pr.Model, models) {
			missing = append(missing, pr.Model)
		}
	}
	if len(missing) > 0 {
		r.add(Check{
			Name:        "models",
			Status:      StatusFail,
			Detail:      "missing persona model(s): " + strings.Join(missing, ", "),
			Remediation: pullCommand(missing),
		})
		return
	}
	r.add(Check{Name: "models", Status: StatusOK, Detail: "all persona models present"})
}

// modelPresent reports whether want is installed, tolerating Ollama's
// implicit ":latest" tag.
func modelPresent(want string, available []string) bool {
	w := stripLatest(want)
	for _, a := range available {
		if stripLatest(a) == w {
			return true
		}
	}
	return false
}

func stripLatest(s string) string { return strings.TrimSuffix(strings.TrimSpace(s), ":latest") }

// pullCommand renders the remediation for one or more missing models.
func pullCommand(models []string) string {
	cmds := make([]string, 0, len(models))
	for _, m := range models {
		cmds = append(cmds, "ollama pull "+m)
	}
	return strings.Join(cmds, " && ")
}

// checkMemory reports physical memory and warns below the advisory floor.
func (r *Report) checkMemory(ctx context.Context, p Probes) {
	bytes, ok := p.TotalMemoryBytes(ctx)
	if !ok {
		r.add(Check{Name: "memory", Status: StatusWarn, Detail: "could not determine physical memory"})
		return
	}
	giB := float64(bytes) / float64(1<<30)
	if bytes < minMemoryBytes {
		r.add(Check{
			Name:        "memory",
			Status:      StatusWarn,
			Detail:      fmt.Sprintf("%.1f GiB — below the %.0f GiB advisory minimum", giB, float64(minMemoryBytes)/(1<<30)),
			Remediation: "use smaller models or add RAM; see §12.2/§12.3",
		})
		return
	}
	r.add(Check{Name: "memory", Status: StatusOK, Detail: fmt.Sprintf("%.1f GiB", giB)})
}

// checkDisk reports free space at the state directory.
func (r *Report) checkDisk(ctx context.Context, opts Options, p Probes) {
	dir := opts.StateDir
	if dir == "" {
		dir = "."
	}
	free, ok := p.FreeDiskBytes(ctx, dir)
	if !ok {
		r.add(Check{Name: "disk", Status: StatusWarn, Detail: "could not determine free disk space"})
		return
	}
	giB := float64(free) / float64(1<<30)
	if free < minDiskBytes {
		r.add(Check{
			Name:        "disk",
			Status:      StatusWarn,
			Detail:      fmt.Sprintf("%.1f GiB free at %s — below the %.0f GiB advisory minimum", giB, dir, float64(minDiskBytes)/(1<<30)),
			Remediation: "free disk space; SQLite, artifacts, backups, and Job Pod images all live under the state directory",
		})
		return
	}
	r.add(Check{Name: "disk", Status: StatusOK, Detail: fmt.Sprintf("%.1f GiB free at %s", giB, dir)})
}

// checkStateDir verifies the state directory can be created and written.
func (r *Report) checkStateDir(opts Options, p Probes) {
	if opts.StateDir == "" {
		r.add(Check{Name: "state_dir", Status: StatusWarn, Detail: "no state directory configured"})
		return
	}
	if err := p.DirWritable(opts.StateDir); err != nil {
		r.add(Check{
			Name:        "state_dir",
			Status:      StatusFail,
			Detail:      fmt.Sprintf("%s is not writable: %v", opts.StateDir, err),
			Remediation: "create the directory and ensure the daemon user owns it",
		})
		return
	}
	r.add(Check{Name: "state_dir", Status: StatusOK, Detail: opts.StateDir + " writable"})
}

// checkJobPodImage warns when tool execution has no image (ADR-0024 §6).
func (r *Report) checkJobPodImage(cfg *config.Config) {
	if cfg.JobPod.Image == "" {
		r.add(Check{
			Name:        "job_pod_image",
			Status:      StatusWarn,
			Detail:      "job_pod.image is empty — execute_code / run_tests / lint will answer 503",
			Remediation: "set job_pod.image to a Job Pod base image (see config.example.yaml)",
		})
		return
	}
	r.add(Check{Name: "job_pod_image", Status: StatusOK, Detail: cfg.JobPod.Image})
}

// checkContext surfaces the §12.6 context-floor tension: a persona whose
// context target is below the code-archetype floor will pause jobs in the
// phases the floor binds. This is advisory at doctor time (Warn), because
// the engine enforces it at runtime and an operator may intentionally
// under-provision non-code work.
func (r *Report) checkContext(cfg *config.Config) {
	floor, ok := llm.FloorFor(llm.ArchetypeCode, cfg.ContextEngine)
	if !ok {
		r.add(Check{Name: "context", Status: StatusWarn, Detail: "no code-archetype context floor defined"})
		return
	}
	for _, pr := range personas(cfg) {
		if pr.ContextTarget >= floor {
			r.add(Check{
				Name:   "context:" + pr.Role,
				Status: StatusOK,
				Detail: fmt.Sprintf("%d tokens meets the code floor (%d)", pr.ContextTarget, floor),
			})
			continue
		}
		r.add(Check{
			Name:        "context:" + pr.Role,
			Status:      StatusWarn,
			Detail:      fmt.Sprintf("%s targets %d tokens, below the code floor (%d); code jobs may pause per §12.6", pr.Role, pr.ContextTarget, floor),
			Remediation: fmt.Sprintf("raise personas.%s.context_target to >= %d or select a model that can run it", pr.Role, floor),
		})
	}
}

// checkNetwork reports the egress posture (§21.5).
func (r *Report) checkNetwork(cfg *config.Config) {
	pol := strings.ToLower(strings.TrimSpace(cfg.Network.DefaultPolicy))
	if pol == "deny" {
		r.add(Check{Name: "network", Status: StatusOK, Detail: fmt.Sprintf("default deny; %d allowlisted domain(s)", len(cfg.Network.AllowList))})
		return
	}
	r.add(Check{
		Name:        "network",
		Status:      StatusWarn,
		Detail:      fmt.Sprintf("default_policy is %q (expected \"deny\")", cfg.Network.DefaultPolicy),
		Remediation: "set network.default_policy: deny unless every egress must be allowed",
	})
}

// checkPower reports the §24 power posture (informational).
func (r *Report) checkPower(cfg *config.Config) {
	r.add(Check{
		Name:   "power",
		Status: StatusOK,
		Detail: fmt.Sprintf("AC required=%t, pause at %d%% battery, daydream on idle=%t",
			config.Val(cfg.Power.RequireACForDeepWork, true),
			cfg.Power.BatteryPauseThresholdPercent,
			config.Val(cfg.Power.DaydreamOnIdle, true)),
	})
}
