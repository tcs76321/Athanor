// Benchmark run protocol (eval/bench/protocol.yaml). This is the machine-
// readable half of the freeze: the task set is pinned by CORPUS.digest, and
// the models, digests, personas, context targets, inference parameters, and
// run shape are pinned here. Changing any of them is a new benchmark version.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type protocolRole struct {
	Model         string  `yaml:"model"`
	Family        string  `yaml:"family"`
	ContextTarget int     `yaml:"context_target"`
	Temperature   float64 `yaml:"temperature"`
}

type protocolInference struct {
	Backend         string `yaml:"backend"`
	EngineVersion   string `yaml:"engine_version"`
	OllamaURL       string `yaml:"ollama_url"`
	JSONFormat      bool   `yaml:"json_format"`
	MaxOutputTokens int    `yaml:"max_output_tokens"`
	Think           bool   `yaml:"think"`
	KeepAlive       string `yaml:"keep_alive"`
	JudgmentSeed    string `yaml:"judgment_seed"`
}

type protocolExecution struct {
	DivergenceCandidates int  `yaml:"divergence_candidates"`
	MaxReflectionLoops   int  `yaml:"max_reflection_loops"`
	EngineCodeGates      bool `yaml:"engine_code_gates"`
	DAGDecomposition     bool `yaml:"dag_decomposition"`
}

type protocolContext struct {
	SeedActiveChunk      bool   `yaml:"seed_active_chunk"`
	SeedActiveMaxBytes   int    `yaml:"seed_active_max_bytes"`
	RepositoryIndexLimit int    `yaml:"repository_index_limit"`
	MemoryEmbeddingModel string `yaml:"memory_embedding_model"`
	MemorySearchTopK     int    `yaml:"memory_search_top_k"`
}

type protocolPersonaSet struct {
	Name      string                  `yaml:"name"`
	Roles     map[string]protocolRole `yaml:"roles"`
	Inference protocolInference       `yaml:"inference"`
	Execution protocolExecution       `yaml:"execution"`
	Context   protocolContext         `yaml:"context"`
	JobPod    struct {
		Image string `yaml:"image"`
	} `yaml:"job_pod"`
	Runtime struct {
		CorePodImage       string `yaml:"core_pod_image"`
		CorePodImageDigest string `yaml:"core_pod_image_digest"`
		HostClass          string `yaml:"host_class"`
	} `yaml:"runtime"`
}

type protocolRunShape struct {
	Full struct {
		Tasks              string  `yaml:"tasks"`
		Passes             int     `yaml:"passes"`
		Arm                string  `yaml:"arm"`
		MachineBudgetHours float64 `yaml:"machine_budget_hours"`
		PerJobTimeout      string  `yaml:"per_job_timeout"`
		MemoryScripts      string  `yaml:"memory_scripts"`
	} `yaml:"full"`
	Anchor struct {
		Tasks              string  `yaml:"tasks"`
		Passes             int     `yaml:"passes"`
		Arm                string  `yaml:"arm"`
		PerModelTimeout    string  `yaml:"per_model_timeout"`
		MachineBudgetHours float64 `yaml:"machine_budget_hours"`
	} `yaml:"anchor"`
}

type protocolRosterEntry struct {
	Label  string `yaml:"label"`
	Model  string `yaml:"model"`
	Family string `yaml:"family"`
	Digest string `yaml:"digest"`
}

type benchProtocol struct {
	Version             int                   `yaml:"version"`
	Benchmark           string                `yaml:"benchmark"`
	Cut                 string                `yaml:"cut"`
	Status              string                `yaml:"status"`
	TaskDigest          string                `yaml:"task_digest"`
	CanonicalPersonaSet protocolPersonaSet    `yaml:"canonical_persona_set"`
	RunShape            protocolRunShape      `yaml:"run_shape"`
	Roster              []protocolRosterEntry `yaml:"roster"`
	Embeddings          struct {
		Model  string `yaml:"model"`
		Digest string `yaml:"digest"`
	} `yaml:"embeddings"`
	Scoring map[string]string `yaml:"scoring"`
}

var protocolRoleNames = []string{"wide", "tall", "main", "security", "alternative"}

// loadProtocol reads and sanity-checks the run protocol.
func loadProtocol(path string) (benchProtocol, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return benchProtocol{}, fmt.Errorf("reading protocol %s: %w", path, err)
	}
	var p benchProtocol
	if err := yaml.Unmarshal(data, &p); err != nil {
		return benchProtocol{}, fmt.Errorf("parsing protocol %s: %w", path, err)
	}
	if p.Version != 1 || p.Benchmark == "" {
		return benchProtocol{}, fmt.Errorf("protocol %s: unsupported version/benchmark", path)
	}
	if len(p.CanonicalPersonaSet.Roles) != len(protocolRoleNames) {
		return benchProtocol{}, fmt.Errorf("protocol %s: want %d persona roles, got %d",
			path, len(protocolRoleNames), len(p.CanonicalPersonaSet.Roles))
	}
	for _, role := range protocolRoleNames {
		if _, ok := p.CanonicalPersonaSet.Roles[role]; !ok {
			return benchProtocol{}, fmt.Errorf("protocol %s: missing persona role %q", path, role)
		}
	}
	if p.RunShape.Full.Passes != 1 {
		return benchProtocol{}, fmt.Errorf("protocol %s: full.passes = %d, want 1 (one full slice)",
			path, p.RunShape.Full.Passes)
	}
	if p.RunShape.Full.MachineBudgetHours <= 0 || p.RunShape.Full.MachineBudgetHours > 12 {
		return benchProtocol{}, fmt.Errorf("protocol %s: full.machine_budget_hours = %v, want (0, 12]",
			path, p.RunShape.Full.MachineBudgetHours)
	}
	if len(p.Roster) == 0 {
		return benchProtocol{}, fmt.Errorf("protocol %s: roster is empty", path)
	}
	return p, nil
}

// checkRoster compares the pinned roster with the installed models. actual maps
// the exact model name (tag included) to its Ollama digest. It returns a list
// of human-readable mismatches; empty means every pinned model is installed at
// the pinned digest.
func checkRoster(roster []protocolRosterEntry, actual map[string]string) []string {
	var issues []string
	for _, e := range roster {
		got, ok := actual[e.Model]
		switch {
		case !ok:
			issues = append(issues, fmt.Sprintf("%s (%s): not installed", e.Label, e.Model))
		case e.Digest != "" && got != e.Digest:
			issues = append(issues, fmt.Sprintf("%s (%s): digest %s, want %s",
				e.Label, e.Model, shortDigest(got), shortDigest(e.Digest)))
		}
	}
	return issues
}

func shortDigest(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}

// runVerifyModels checks the pinned protocol roster against the local Ollama.
func runVerifyModels(args []string) {
	fs := flag.NewFlagSet("verify-models", flag.ExitOnError)
	protocolPath := fs.String("protocol", "eval/bench/protocol.yaml", "path to the frozen run protocol")
	ollamaURL := fs.String("ollama", "http://localhost:11434", "Ollama base URL")
	_ = fs.Parse(args)

	p, err := loadProtocol(*protocolPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "verify-models:", err)
		os.Exit(1)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(strings.TrimRight(*ollamaURL, "/") + "/api/tags")
	if err != nil {
		fmt.Fprintf(os.Stderr, "verify-models: cannot reach Ollama at %s: %v\n", *ollamaURL, err)
		os.Exit(1)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "verify-models: Ollama /api/tags returned %s\n", resp.Status)
		os.Exit(1)
	}
	var tags struct {
		Models []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		fmt.Fprintln(os.Stderr, "verify-models: decoding /api/tags:", err)
		os.Exit(1)
	}
	actual := map[string]string{}
	for _, m := range tags.Models {
		actual[m.Name] = m.Digest
	}

	issues := checkRoster(p.Roster, actual)
	if p.Embeddings.Model != "" {
		got, ok := actual[p.Embeddings.Model]
		switch {
		case !ok:
			issues = append(issues, fmt.Sprintf("embedding (%s): not installed", p.Embeddings.Model))
		case p.Embeddings.Digest != "" && got != p.Embeddings.Digest:
			issues = append(issues, fmt.Sprintf("embedding (%s): digest %s, want %s",
				p.Embeddings.Model, shortDigest(got), shortDigest(p.Embeddings.Digest)))
		}
	}

	fmt.Printf("protocol %s/%s — roster %d, embeddings %s\n",
		p.Benchmark, p.CanonicalPersonaSet.Name, len(p.Roster), p.Embeddings.Model)
	for _, e := range p.Roster {
		status := "ok"
		if _, ok := actual[e.Model]; !ok {
			status = "missing"
		} else if e.Digest != "" && actual[e.Model] != e.Digest {
			status = "digest mismatch"
		}
		fmt.Printf("  %-12s %-40s %-16s %s\n", e.Label, e.Model, status, shortDigest(e.Digest))
	}
	if len(issues) > 0 {
		for _, issue := range issues {
			fmt.Fprintln(os.Stderr, "MISMATCH:", issue)
		}
		os.Exit(1)
	}
	fmt.Println("protocol models OK")
}

// filterByAnchor narrows a corpus to the task ids in an anchor file, preserving
// the anchor's order. An unknown id is a loud error: a silently smaller anchor
// would quietly change the onboarding signal.
func filterByAnchor(goals []sampleGoal, anchorPath string) ([]sampleGoal, error) {
	if anchorPath == "" {
		return goals, nil
	}
	data, err := os.ReadFile(anchorPath)
	if err != nil {
		return nil, fmt.Errorf("reading anchor %s: %w", anchorPath, err)
	}
	var a struct {
		Tasks []struct {
			ID string `yaml:"id"`
		} `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("parsing anchor %s: %w", anchorPath, err)
	}
	if len(a.Tasks) == 0 {
		return nil, fmt.Errorf("anchor %s has no tasks", anchorPath)
	}
	byName := make(map[string]sampleGoal, len(goals))
	for _, g := range goals {
		byName[g.Name] = g
	}
	out := make([]sampleGoal, 0, len(a.Tasks))
	for _, t := range a.Tasks {
		g, ok := byName[t.ID]
		if !ok {
			return nil, fmt.Errorf("anchor %s: task %q is not in the corpus", anchorPath, t.ID)
		}
		out = append(out, g)
	}
	return out, nil
}
