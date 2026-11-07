package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/onboard"
)

// defaultAuditReserveBytes is what the audit leaves for the OS, apps, the Core,
// and the Podman VM outside Ollama's resident model.
const defaultAuditReserveBytes int64 = 4 << 30

func modelAuditPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "model-fit.json")
}

// runModelAudit loads each configured persona model at its largest context
// target, reads Ollama's actual loaded size from /api/ps, unloads it, and
// builds a measured fit plan. It is deliberately explicit: loading models is
// expensive, so `athanor start` only reads the resulting file.
func runModelAudit(ctx context.Context, cfg *config.Config, probes *osProbes, hostBytes, reserveBytes int64) (onboard.Audit, error) {
	if hostBytes <= 0 {
		return onboard.Audit{}, fmt.Errorf("cannot audit model fit: host memory is unknown")
	}
	if reserveBytes <= 0 {
		if cfg != nil && cfg.Inference.HostReserveMB > 0 {
			reserveBytes = int64(cfg.Inference.HostReserveMB) << 20
		} else {
			reserveBytes = defaultAuditReserveBytes
		}
	}
	audit := onboard.Audit{
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
		HostBytes:    hostBytes,
		ReserveBytes: reserveBytes,
		BudgetBytes:  hostBytes - reserveBytes,
	}
	if audit.BudgetBytes < 1024 {
		audit.BudgetBytes = 1024
	}
	baseURL := strings.TrimRight(cfg.Inference.OllamaURL, "/")
	catalog, err := ollamaCatalog(ctx, probes, baseURL)
	if err != nil {
		return onboard.Audit{}, fmt.Errorf("listing installed models: %w", err)
	}
	files := map[string]int64{}
	for _, m := range catalog {
		files[m.Name] = m.SizeBytes
	}
	roles, _ := modelRoles(cfg, catalog)

	// Measure each unique model at the largest context any role asks for.
	contextFor := map[string]int{}
	for _, r := range roles {
		if r.ContextTarget > contextFor[r.Model] {
			contextFor[r.Model] = r.ContextTarget
		}
	}
	names := make([]string, 0, len(contextFor))
	for name := range contextFor {
		names = append(names, name)
	}
	sort.Strings(names)
	loaded := map[string]int64{}
	errs := map[string]string{}
	client := &http.Client{Timeout: 15 * time.Minute}
	for _, name := range names {
		if _, ok := files[name]; !ok {
			errs[name] = "model is not installed"
			continue
		}
		size, merr := measureModel(ctx, client, baseURL, name, contextFor[name])
		if merr != nil {
			errs[name] = merr.Error()
			continue
		}
		loaded[name] = size
	}

	for _, r := range roles {
		entry := onboard.AuditRole{
			Role: r.Role, Model: r.Model, ContextTarget: r.ContextTarget,
			FileBytes: files[r.Model],
		}
		if msg, ok := errs[r.Model]; ok {
			entry.Error = msg
		} else {
			entry.LoadedBytes = loaded[r.Model]
			entry.Fits = entry.LoadedBytes > 0 && entry.LoadedBytes <= audit.BudgetBytes
			if entry.LoadedBytes == 0 {
				entry.Error = "Ollama did not report a loaded size"
			}
		}
		audit.Roles = append(audit.Roles, entry)
	}
	audit.Resident = residentModels(ctx, probes, baseURL)
	// Capacity is derived from the measured roles. The Podman VM size is not
	// measured here, so CapacityReport assumes the host budget and labels the
	// result; the Core Pod passes its own runtime budget when it starts.
	perPod := int64(0)
	if cfg != nil && cfg.JobPod.MemoryMB > 0 {
		perPod = int64(cfg.JobPod.MemoryMB) << 20
	}
	embedBytes := int64(0)
	embedModel := ""
	if cfg != nil {
		embedModel = cfg.ContextEngine.MemoryEmbeddingModel
		if embedModel != "" {
			embedBytes = 300 << 20 // nomic-class embedding model
		}
	}
	maxJobs, maxLLM := 1, 1
	podman := false
	jobPodImage := ""
	if cfg != nil {
		maxJobs = cfg.Limits.MaxConcurrentJobs
		maxLLM = cfg.Limits.MaxConcurrentLLMCalls
		jobPodImage = cfg.JobPod.Image
		podman = jobPodImage != ""
	}
	audit.Capacity = ptr(onboard.CapacityReport(audit, onboard.CapacityOptions{
		CoreReserveBytes: 1 << 30, PerPodBytes: perPod, MaxJobs: maxJobs,
		MaxLLMCalls: maxLLM, Podman: podman, JobPodImage: jobPodImage,
		EmbeddingModel: embedModel, EmbeddingBytes: embedBytes,
		RepoScratchBytes: 2 << 30,
	}))
	return audit, nil
}

// measureModel loads one model at numCtx with a one-token generation, reads the
// actual resident size, then asks Ollama to unload it.
func measureModel(ctx context.Context, client *http.Client, baseURL, model string, numCtx int) (int64, error) {
	load := map[string]any{
		"model": model, "prompt": "ok", "stream": false, "keep_alive": "2m",
		"options": map[string]any{"num_ctx": numCtx, "num_predict": 1},
	}
	if err := ollamaPost(ctx, client, baseURL+"/api/generate", load, nil); err != nil {
		_ = unloadModel(ctx, client, baseURL, model)
		return 0, fmt.Errorf("loading at %d context: %w", numCtx, err)
	}
	size, err := loadedSize(ctx, client, baseURL, model)
	_ = unloadModel(ctx, client, baseURL, model)
	if err != nil {
		return 0, err
	}
	return size, nil
}

func loadedSize(ctx context.Context, client *http.Client, baseURL, model string) (int64, error) {
	var body struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
			Size  int64  `json:"size"`
		} `json:"models"`
	}
	if err := getJSONClient(ctx, client, baseURL+"/api/ps", &body); err != nil {
		return 0, fmt.Errorf("reading /api/ps: %w", err)
	}
	for _, m := range body.Models {
		name := m.Name
		if name == "" {
			name = m.Model
		}
		if name == model || modelBaseName(name) == modelBaseName(model) {
			if m.Size <= 0 {
				return 0, fmt.Errorf("ollama reported an empty size for %s", model)
			}
			return m.Size, nil
		}
	}
	return 0, fmt.Errorf("model %s is not resident after load", model)
}

func unloadModel(ctx context.Context, client *http.Client, baseURL, model string) error {
	return ollamaPost(ctx, client, baseURL+"/api/generate", map[string]any{
		"model": model, "stream": false, "keep_alive": 0,
	}, nil)
}

func residentModels(ctx context.Context, probes *osProbes, baseURL string) []string {
	var body struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := probes.getJSON(ctx, baseURL+"/api/ps", &body); err != nil {
		return nil
	}
	out := make([]string, 0, len(body.Models))
	for _, m := range body.Models {
		name := m.Name
		if name == "" {
			name = m.Model
		}
		if name != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func ollamaPost(ctx context.Context, client *http.Client, url string, in any, out any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s returned %s: %s", url, resp.Status, strings.TrimSpace(string(msg)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

func getJSONClient(ctx context.Context, client *http.Client, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s", url, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

func modelBaseName(name string) string {
	if i := strings.IndexAny(name, ":@"); i >= 0 {
		return name[:i]
	}
	return name
}

func writeModelAudit(path string, a onboard.Audit) error {
	raw, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

func readModelAudit(path string) (onboard.Audit, bool, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return onboard.Audit{}, false, nil
	}
	if err != nil {
		return onboard.Audit{}, false, err
	}
	var a onboard.Audit
	if err := json.Unmarshal(raw, &a); err != nil {
		return onboard.Audit{}, false, fmt.Errorf("parsing %s: %w", path, err)
	}
	return a, true, nil
}

// checkModelAudit enforces the measured plan at startup. A missing audit is a
// warning, not a failure (the operator may not have run it yet); a measured
// non-fit is a hard error so a known-bad configuration does not start silently.
func checkModelAudit(path string, cfg *config.Config, hostBytes int64) (onboard.Audit, error) {
	a, ok, err := readModelAudit(path)
	if err != nil {
		return onboard.Audit{}, err
	}
	if !ok {
		fmt.Printf("model fit: no measured audit at %s; run `athanor models -audit` to measure KV-inclusive fit\n", path)
		return onboard.Audit{}, nil
	}
	if a.HostBytes > 0 && hostBytes > 0 && absDiff(a.HostBytes, hostBytes)*10 > hostBytes {
		fmt.Printf("model fit: audit host memory %.1f GiB differs from the current %.1f GiB; re-run `athanor models -audit`\n",
			float64(a.HostBytes)/(1<<30), float64(hostBytes)/(1<<30))
	}
	if cfg != nil {
		seen := map[string]onboard.AuditRole{}
		for _, r := range a.Roles {
			seen[r.Role] = r
		}
		for _, role := range []string{"wide", "tall", "main", "security", "alternative"} {
			pc, ok := cfg.Personas.Role(role)
			if !ok || pc.Model == "" {
				continue
			}
			ar, ok := seen[role]
			if !ok || ar.Model != pc.Model || (pc.ContextTarget > 0 && ar.ContextTarget != pc.ContextTarget) {
				return a, fmt.Errorf("model fit audit is stale for %s: audit has %q at %d, config has %q at %d; re-run `athanor models -audit`",
					role, ar.Model, ar.ContextTarget, pc.Model, pc.ContextTarget)
			}
		}
	}
	if fails := a.FailingRoles(); len(fails) > 0 {
		var parts []string
		for _, r := range fails {
			parts = append(parts, fmt.Sprintf("%s=%s (%s)", r.Role, r.Model, auditReason(r)))
		}
		return a, fmt.Errorf("model fit audit rejects the configured models: %s; fix config.yaml or re-run `athanor models -audit`", strings.Join(parts, ", "))
	}
	if len(a.Resident) > 1 {
		fmt.Printf("model fit: %d models were resident at audit time (%s); set OLLAMA_MAX_LOADED_MODELS=1 to keep one model hot\n",
			len(a.Resident), strings.Join(a.Resident, ", "))
	}
	return a, nil
}

func auditReason(r onboard.AuditRole) string {
	if r.Error != "" {
		return r.Error
	}
	return fmt.Sprintf("loaded %.1f GiB > budget", float64(r.LoadedBytes)/(1<<30))
}

func absDiff(a, b int64) int64 {
	if a > b {
		return a - b
	}
	return b - a
}

func printModelAudit(a onboard.Audit) {
	fmt.Printf("model fit audit — host %.1f GiB, reserve %.1f GiB, budget %.1f GiB\n",
		float64(a.HostBytes)/(1<<30), float64(a.ReserveBytes)/(1<<30), float64(a.BudgetBytes)/(1<<30))
	fmt.Printf("%-12s %-24s %-8s %-10s %-10s %s\n", "ROLE", "MODEL", "CTX", "FILE", "LOADED", "FIT")
	for _, r := range a.Roles {
		fit := "yes"
		if !r.Fits {
			fit = "NO"
		}
		if r.Error != "" {
			fit = "ERROR: " + r.Error
		}
		fmt.Printf("%-12s %-24s %-8d %-10s %-10s %s\n",
			r.Role, r.Model, r.ContextTarget,
			fmt.Sprintf("%.1fG", float64(r.FileBytes)/(1<<30)),
			fmt.Sprintf("%.1fG", float64(r.LoadedBytes)/(1<<30)), fit)
	}
	if len(a.Resident) > 1 {
		fmt.Printf("resident at end: %s\n", strings.Join(a.Resident, ", "))
	}
	if c := a.Capacity; c != nil {
		solo := "none"
		if c.Solo != nil {
			solo = fmt.Sprintf("%s@%d", c.Solo.Model, c.Solo.Context)
		}
		fmt.Printf("capacity: solo=%s", solo)
		if c.SwarmPods != nil {
			fmt.Printf(" pods=%d×%d", c.SwarmPods.Slots, c.SwarmPods.Context)
		}
		if c.SwarmLight != nil {
			fmt.Printf(" light=%d×%d", c.SwarmLight.Slots, c.SwarmLight.Context)
		}
		fmt.Println()
		for _, r := range c.Reasons {
			fmt.Printf("  %-5s %s: %s\n", r.Severity, r.Code, r.Detail)
		}
	}
}

func auditOutPath(configPath, explicit string) string {
	if explicit != "" {
		return explicit
	}
	return modelAuditPath(configPath)
}

// unloadConfiguredModels asks Ollama to evict every configured persona model
// when Athanor enters Stopped. It deliberately does not stop Ollama itself:
// the service may be used by other local software.
func unloadConfiguredModels(ctx context.Context, cfg *config.Config) {
	if cfg == nil {
		return
	}
	base := strings.TrimRight(cfg.Inference.OllamaURL, "/")
	if base == "" {
		return
	}
	client := &http.Client{Timeout: 30 * time.Second}
	seen := map[string]bool{}
	for _, name := range []string{"wide", "tall", "main", "security", "alternative"} {
		pc, ok := cfg.Personas.Role(name)
		if !ok || pc.Model == "" || seen[pc.Model] {
			continue
		}
		seen[pc.Model] = true
		if err := unloadModel(ctx, client, base, pc.Model); err != nil {
			slog.Warn("operator: unloading model", "model", pc.Model, "err", err)
		}
	}
}

func ptr[T any](v T) *T { return &v }
