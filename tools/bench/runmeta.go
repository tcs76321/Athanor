package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// runMeta records how a benchmark result was produced. It exists so a reader
// can tell a Core Pod headline from a host smoke run without trusting a
// directory name or a README. ADR-0126.
type runMeta struct {
	Schema      int               `json:"schema"`
	Runtime     string            `json:"runtime"` // core-pod | host
	Diagnostic  bool              `json:"diagnostic"`
	HostClass   string            `json:"host_class,omitempty"`
	CoreImage   string            `json:"core_image,omitempty"`
	CoreDigest  string            `json:"core_digest,omitempty"`
	OllamaURL   string            `json:"ollama_url,omitempty"`
	OllamaVer   string            `json:"ollama_version,omitempty"`
	Model       string            `json:"model,omitempty"`
	JudgeModel  string            `json:"judge_model,omitempty"`
	Models      []string          `json:"models,omitempty"`
	Digests     map[string]string `json:"model_digests,omitempty"`
	GitCommit   string            `json:"git_commit,omitempty"`
	ContextPol  string            `json:"context_policy,omitempty"`
	CastPolicy  string            `json:"cast_policy,omitempty"`
	Slots       int               `json:"slots,omitempty"`
	TaskCount   int               `json:"task_count,omitempty"`
	Passes      int               `json:"passes,omitempty"`
	Rows        int               `json:"rows,omitempty"`
	StartedAt   string            `json:"started_at"`
	CompletedAt string            `json:"completed_at,omitempty"`
	Complete    bool              `json:"complete"`
}

const runMetaFile = "run.json"

// resolveRuntime derives the run runtime from the explicit flag and the
// external (already-running Core Pod) mode. A core-pod run must be external:
// the probe cannot manage the container it is running inside.
func resolveRuntime(explicit string, external bool) (string, error) {
	switch strings.ToLower(strings.TrimSpace(explicit)) {
	case "":
		if external {
			return "core-pod", nil
		}
		return "host", nil
	case "core-pod", "corepod", "core":
		if !external {
			return "", fmt.Errorf("-runtime core-pod requires -external (the probe runs inside the Core Pod)")
		}
		return "core-pod", nil
	case "host":
		if external {
			return "", fmt.Errorf("-runtime host conflicts with -external (external means the Core Pod)")
		}
		return "host", nil
	default:
		return "", fmt.Errorf("unknown -runtime %q (want core-pod or host)", explicit)
	}
}

func loadRunMeta(dir string) (runMeta, error) {
	b, err := os.ReadFile(filepath.Join(dir, runMetaFile))
	if err != nil {
		return runMeta{}, err
	}
	var m runMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return runMeta{}, fmt.Errorf("parsing %s: %w", runMetaFile, err)
	}
	return m, nil
}

func writeRunMeta(dir string, m runMeta) error {
	m.Schema = 1
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(filepath.Join(dir, runMetaFile), b, 0o644)
}

// newRunMeta builds the immutable part of the metadata at run start.
func newRunMeta(runtimeName, ollamaURL, model, judge string, external bool) runMeta {
	m := runMeta{
		Runtime:    runtimeName,
		Diagnostic: runtimeName != "core-pod",
		HostClass:  runtime.GOOS + "/" + runtime.GOARCH,
		OllamaURL:  ollamaURL,
		Model:      model,
		JudgeModel: judge,
		GitCommit:  currentGitCommit(),
		StartedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if runtimeName == "core-pod" {
		m.CoreImage = os.Getenv("ATHANOR_CORE_IMAGE")
		m.CoreDigest = os.Getenv("ATHANOR_CORE_IMAGE_DIGEST")
	}
	if tags, version := ollamaFacts(ollamaURL); tags != nil {
		m.OllamaVer = version
		m.Digests = tags
	}
	return m
}

// ollamaFacts returns the installed model digests and the server version.
// A failure is not fatal for harness work; the metadata simply omits them.
func ollamaFacts(baseURL string) (map[string]string, string) {
	base := strings.TrimRight(baseURL, "/")
	client := &http.Client{Timeout: 5 * time.Second}
	digests := map[string]string{}
	if resp, err := client.Get(base + "/api/tags"); err == nil {
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode == http.StatusOK {
			var tags struct {
				Models []struct {
					Name   string `json:"name"`
					Digest string `json:"digest"`
				} `json:"models"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&tags); err == nil {
				for _, m := range tags.Models {
					digests[m.Name] = m.Digest
				}
			}
		}
	}
	version := ""
	if resp, err := client.Get(base + "/api/version"); err == nil {
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode == http.StatusOK {
			var v struct {
				Version string `json:"version"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&v); err == nil {
				version = v.Version
			}
		}
	}
	if len(digests) == 0 {
		digests = nil
	}
	return digests, version
}

func currentGitCommit() string {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// modelDigests narrows the full tag map to the models a run actually names.
func modelDigests(all map[string]string, models ...string) map[string]string {
	if len(all) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, name := range models {
		if name == "" {
			continue
		}
		if d, ok := all[name]; ok {
			out[name] = d
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sortedKeys is a deterministic helper for tests and reports.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// renderRunHeader is the report banner. A host run is explicitly diagnostic:
// it is useful for harness work and never a headline result (ADR-0126).
func renderRunHeader(m runMeta) string {
	var b strings.Builder
	b.WriteString("# Run evidence\n\n")
	if m.Runtime == "" {
		b.WriteString("**Runtime:** unknown (no run.json) — treat as **DIAGNOSTIC**, not a headline result.\n\n")
		return b.String()
	}
	if m.Runtime == "core-pod" {
		b.WriteString("**Runtime:** Core Pod — **HEADLINE** (supported runtime).\n\n")
	} else {
		b.WriteString("**Runtime:** host binary — **DIAGNOSTIC**, not a headline result.\n\n")
	}
	if m.Model != "" {
		fmt.Fprintf(&b, "- Model: `%s`\n", m.Model)
	}
	if m.JudgeModel != "" {
		fmt.Fprintf(&b, "- Judge: `%s`\n", m.JudgeModel)
	}
	if m.CoreImage != "" {
		fmt.Fprintf(&b, "- Core image: `%s`\n", m.CoreImage)
	}
	if m.CoreDigest != "" {
		fmt.Fprintf(&b, "- Core digest: `%s`\n", m.CoreDigest)
	}
	if m.OllamaVer != "" {
		fmt.Fprintf(&b, "- Ollama: `%s`\n", m.OllamaVer)
	}
	if m.GitCommit != "" {
		fmt.Fprintf(&b, "- Revision: `%s`\n", m.GitCommit)
	}
	if m.ContextPol != "" {
		fmt.Fprintf(&b, "- Context policy: `%s`\n", m.ContextPol)
	}
	if m.CastPolicy != "" {
		fmt.Fprintf(&b, "- Cast policy: `%s`\n", m.CastPolicy)
	}
	if m.Slots > 0 {
		fmt.Fprintf(&b, "- Slots: %d\n", m.Slots)
	}
	if m.StartedAt != "" {
		fmt.Fprintf(&b, "- Started: %s\n", m.StartedAt)
	}
	if m.CompletedAt != "" {
		fmt.Fprintf(&b, "- Completed: %s\n", m.CompletedAt)
	}
	if !m.Complete {
		b.WriteString("- **Incomplete:** no completion marker.\n")
	}
	b.WriteString("\n")
	if len(m.Digests) > 0 {
		b.WriteString("| model | digest |\n|---|---|\n")
		for _, k := range sortedKeys(m.Digests) {
			fmt.Fprintf(&b, "| `%s` | `%s` |\n", k, shortDigest(m.Digests[k]))
		}
		b.WriteString("\n")
	}
	return b.String()
}
