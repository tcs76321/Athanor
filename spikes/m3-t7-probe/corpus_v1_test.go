// Benchmark v1 validation. These tests are the guardrail on the corpus: they
// pin the composition the design promises, prove every task has a
// deterministic check, and (when python3 is available) execute every code
// task's real test command against a reference solution so no task ships with
// an unsatisfiable or wrong check.
package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	benchTasksPath     = "../../eval/bench/tasks.yaml"
	benchMemoryPath    = "../../eval/bench/memory-scripts.yaml"
	benchHumanPath     = "../../eval/bench/human-review.yaml"
	benchReferencePath = "../../eval/bench/reference.yaml"
)

// repoPath resolves a corpus-relative path (fixtures, schemas) from this
// package's directory.
func repoPath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join("..", "..", p)
}

func readBenchYAML(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := yaml.Unmarshal(data, v); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

// TestBenchCorpusV1 pins the deterministic corpus: 70 tasks, the designed
// archetype/tier spread, unique ids, metadata on every task, and a real check.
func TestBenchCorpusV1(t *testing.T) {
	goals, err := loadCorpus(benchTasksPath)
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	if len(goals) != 70 {
		t.Fatalf("deterministic tasks = %d, want 70", len(goals))
	}

	ids := map[string]bool{}
	arch := map[string]int{}
	tier := map[string]int{}
	heldOut := 0
	advisory := map[string]bool{
		"required_substrings_source": true, "preserve_entities_from_fixture": true,
		"every_item_once": true, "must_flag_contradiction": true,
		"must_not_fabricate": true, "no_fabricated_commands": true,
		"max_new_claims": true, "exactly_one_cta": true,
		"required_citations_from_fixture": true,
	}
	for _, g := range goals {
		if g.Name == "" {
			t.Errorf("task %d has no id", g.Number)
		}
		if ids[g.Name] {
			t.Errorf("duplicate task id %q", g.Name)
		}
		ids[g.Name] = true
		arch[g.Archetype]++
		tier[tierOf(g.Source)]++
		if g.HeldOut {
			heldOut++
		}
		if g.ContextDemand == "" {
			t.Errorf("%s: context_demand is required", g.Name)
		}
		switch g.ContextDemand {
		case "short", "long", "repo", "memory":
		default:
			t.Errorf("%s: context_demand %q is not short|long|repo|memory", g.Name, g.ContextDemand)
		}
		if g.Archetype == "code" {
			if strings.TrimSpace(g.TestCommand) == "" {
				t.Errorf("%s: code task has no test_command", g.Name)
			}
		} else if len(g.Checks) == 0 {
			t.Errorf("%s: non-code task has no checks", g.Name)
		} else {
			deterministic := false
			for kind := range g.Checks {
				if !advisory[kind] {
					deterministic = true
					break
				}
			}
			if !deterministic {
				t.Errorf("%s: every check is advisory; the task has no deterministic gate", g.Name)
			}
		}
		if g.Fixture != "" {
			if _, err := os.Stat(repoPath(g.Fixture)); err != nil {
				t.Errorf("%s: fixture %q missing: %v", g.Name, g.Fixture, err)
			}
		}
		if g.Repo != "" {
			if _, err := os.Stat(repoPath(g.Repo)); err != nil {
				t.Errorf("%s: repo %q missing: %v", g.Name, g.Repo, err)
			}
		}
		if schema, ok := g.Checks["json_schema"].(string); ok {
			if _, err := os.Stat(repoPath(schema)); err != nil {
				t.Errorf("%s: json_schema %q missing: %v", g.Name, schema, err)
			}
		}
	}

	wantArch := map[string]int{"code": 30, "document": 22, "text": 12, "data": 6}
	for k, want := range wantArch {
		if arch[k] != want {
			t.Errorf("archetype %s = %d, want %d", k, arch[k], want)
		}
	}
	wantTier := map[string]int{"E": 13, "M": 31, "H": 26}
	for k, want := range wantTier {
		if tier[k] != want {
			t.Errorf("tier %s = %d, want %d", k, tier[k], want)
		}
	}
	if heldOut != 14 {
		t.Errorf("held-out deterministic tasks = %d, want 14 (20%% of 70)", heldOut)
	}
}

// TestBenchExtras pins the memory and human-review strata and the 20/80
// held-out share.
func TestBenchExtras(t *testing.T) {
	var mem struct {
		Scripts []struct {
			ID      string `yaml:"id"`
			HeldOut bool   `yaml:"held_out"`
			Steps   []struct {
				Kind string `yaml:"kind"`
				Goal string `yaml:"goal"`
			} `yaml:"steps"`
		} `yaml:"scripts"`
	}
	readBenchYAML(t, benchMemoryPath, &mem)
	if len(mem.Scripts) != 4 {
		t.Fatalf("memory scripts = %d, want 4", len(mem.Scripts))
	}
	memHeld := 0
	for _, s := range mem.Scripts {
		if s.ID == "" {
			t.Error("memory script without id")
		}
		if len(s.Steps) < 3 {
			t.Errorf("%s: want >= 3 steps, got %d", s.ID, len(s.Steps))
		}
		for _, step := range s.Steps {
			switch step.Kind {
			case "task":
				if step.Goal == "" {
					t.Errorf("%s: task step has no goal", s.ID)
				}
			case "correction", "insight", "filler":
			default:
				t.Errorf("%s: bad step kind %q", s.ID, step.Kind)
			}
		}
		if s.HeldOut {
			memHeld++
		}
	}
	if memHeld != 3 {
		t.Errorf("held-out memory scripts = %d, want 3", memHeld)
	}

	var human struct {
		Tasks []struct {
			ID          string            `yaml:"id"`
			Goal        string            `yaml:"goal"`
			Fixture     string            `yaml:"fixture"`
			HeldOut     bool              `yaml:"held_out"`
			HumanReview bool              `yaml:"human_review"`
			Rubric      map[string]string `yaml:"rubric"`
		} `yaml:"tasks"`
	}
	readBenchYAML(t, benchHumanPath, &human)
	if len(human.Tasks) != 6 {
		t.Fatalf("human-review tasks = %d, want 6", len(human.Tasks))
	}
	humanHeld := 0
	for _, task := range human.Tasks {
		if task.ID == "" || task.Goal == "" {
			t.Errorf("human task missing id/goal: %+v", task)
		}
		if !task.HumanReview {
			t.Errorf("%s: human_review must be true", task.ID)
		}
		if task.Fixture != "" {
			if _, err := os.Stat(repoPath(task.Fixture)); err != nil {
				t.Errorf("%s: fixture %q missing: %v", task.ID, task.Fixture, err)
			}
		}
		for _, k := range []string{"5", "3", "1"} {
			if task.Rubric[k] == "" {
				t.Errorf("%s: rubric[%s] is empty", task.ID, k)
			}
		}
		if task.HeldOut {
			humanHeld++
		}
	}
	if humanHeld != 3 {
		t.Errorf("held-out human tasks = %d, want 3", humanHeld)
	}

	if total := len(mem.Scripts) + len(human.Tasks); total != 10 {
		t.Fatalf("extras = %d, want 10", total)
	}
	if held := 14 + memHeld + humanHeld; held != 20 {
		t.Errorf("held-out total = %d, want 20 of 80 (25%%)", held)
	}
}

// TestBenchDataChecks runs the real YAML check maps against known-good outputs.
// This pins the YAML decoding (nested `json_field_matches` maps) and the schema
// paths, which the Go-level unit tests cannot see.
func TestBenchDataChecks(t *testing.T) {
	goals, err := loadCorpus(benchTasksPath)
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	byName := map[string]sampleGoal{}
	for _, g := range goals {
		byName[g.Name] = g
	}

	good := map[string]string{
		"data-e-csv-json":         `[{"name":"Ada","age":36},{"name":"Sam","age":41}]`,
		"data-m-filter":           `[{"id":1,"status":"active"},{"id":3,"status":"active"}]`,
		"data-m-dedupe-sort":      `[{"id":1,"v":"b"},{"id":2,"v":"c"},{"id":10,"v":"d"}]`,
		"data-m-nested-normalize": `[{"id":1,"city":"Oslo","country":"NO"},{"id":2,"city":"Lima","country":"PE"}]`,
		"data-h-schema-validate":  `[{"id":7,"created_at":"2026-03-01T10:00:00Z","severity":"warning"},{"id":9,"created_at":"2026-03-02T10:30:00Z","severity":"error"}]`,
		"data-json-normalize":     `[{"timestamp":"2026-01-02T14:03:11Z","severity":"warning","message":"disk almost full"},{"timestamp":"2026-01-02T14:05:00Z","severity":"error","message":"database connection refused"},{"timestamp":"2026-01-03T09:15:42Z","severity":"info","message":"nightly backup completed"},{"timestamp":"2026-01-04T07:00:00Z","severity":"warning","message":"certificate expires in 30 days"},{"timestamp":"2026-01-04T07:01:00Z","severity":"info","message":"certificate renewed"},{"timestamp":"2026-01-05T22:30:05Z","severity":"error","message":"out of memory"}]`,
	}
	for name, output := range good {
		g, ok := byName[name]
		if !ok {
			t.Fatalf("%s: task not found", name)
		}
		checks := map[string]any{}
		for k, v := range g.Checks {
			checks[k] = v
		}
		if p, ok := checks["json_schema"].(string); ok {
			checks["json_schema"] = repoPath(p)
		}
		if pass, results := runChecks(checks, output); !pass {
			t.Errorf("%s: good output failed: %s (%+v)", name, output, results)
		}
	}

	bad := []struct{ name, output string }{
		// duplicate id violates array_unique_by and array_length
		{"data-m-dedupe-sort", `[{"id":1,"v":"b"},{"id":1,"v":"c"}]`},
		// local timestamp violates json_field_matches
		{"data-h-schema-validate", `[{"id":7,"created_at":"2026-03-01 10:00:00","severity":"warning"},{"id":9,"created_at":"2026-03-02T10:30:00Z","severity":"error"}]`},
		// wrong length / extra key
		{"data-m-filter", `[{"id":1,"status":"active"}]`},
	}
	for _, c := range bad {
		g := byName[c.name]
		checks := map[string]any{}
		for k, v := range g.Checks {
			checks[k] = v
		}
		if p, ok := checks["json_schema"].(string); ok {
			checks["json_schema"] = repoPath(p)
		}
		if pass, _ := runChecks(checks, c.output); pass {
			t.Errorf("%s: bad output passed: %s", c.name, c.output)
		}
	}
}

// TestBenchProtocol pins the run contract: one canonical persona set, the
// pinned model digests, the exact role/context/inference values, a one-pass
// full slice, and a short anchor drawn only from the tuning slice.
func TestBenchProtocol(t *testing.T) {
	p, err := loadProtocol("../../eval/bench/protocol.yaml")
	if err != nil {
		t.Fatalf("loadProtocol: %v", err)
	}
	if p.Benchmark != "v1" || p.Status != "frozen" {
		t.Errorf("benchmark=%q status=%q, want v1/frozen", p.Benchmark, p.Status)
	}

	wantRoles := map[string]protocolRole{
		"wide":        {Model: "ornith-1.5:9b", Family: "qwen35", ContextTarget: 32768, Temperature: 0.7},
		"tall":        {Model: "ornith-1.5:9b", Family: "qwen35", ContextTarget: 16384, Temperature: 0.2},
		"main":        {Model: "ornith-1.5:9b", Family: "qwen35", ContextTarget: 32768, Temperature: 0.4},
		"security":    {Model: "granite4.2:3b", Family: "granite", ContextTarget: 32768, Temperature: 0.0},
		"alternative": {Model: "ornith-1.5:9b", Family: "qwen35", ContextTarget: 32768, Temperature: 0.8},
	}
	for role, want := range wantRoles {
		if got := p.CanonicalPersonaSet.Roles[role]; got != want {
			t.Errorf("role %s = %+v, want %+v", role, got, want)
		}
	}
	if p.CanonicalPersonaSet.Inference.MaxOutputTokens != 4096 ||
		p.CanonicalPersonaSet.Inference.Think ||
		p.CanonicalPersonaSet.Inference.JudgmentSeed != "off" ||
		p.CanonicalPersonaSet.Inference.EngineVersion == "" {
		t.Errorf("inference drift: %+v", p.CanonicalPersonaSet.Inference)
	}
	if p.CanonicalPersonaSet.Runtime.CorePodImage == "" ||
		p.CanonicalPersonaSet.Runtime.HostClass == "" {
		t.Errorf("runtime drift: %+v", p.CanonicalPersonaSet.Runtime)
	}
	if p.CanonicalPersonaSet.JobPod.Image != "athanor-jobpod:latest" {
		t.Errorf("job pod image = %q, want athanor-jobpod:latest", p.CanonicalPersonaSet.JobPod.Image)
	}
	if p.CanonicalPersonaSet.Execution.DivergenceCandidates != 3 ||
		p.CanonicalPersonaSet.Execution.MaxReflectionLoops != 2 {
		t.Errorf("execution drift: %+v", p.CanonicalPersonaSet.Execution)
	}
	if !p.CanonicalPersonaSet.Context.SeedActiveChunk ||
		p.CanonicalPersonaSet.Context.MemoryEmbeddingModel == "" ||
		p.CanonicalPersonaSet.Context.MemorySearchTopK == 0 {
		t.Errorf("context/embedding drift: %+v", p.CanonicalPersonaSet.Context)
	}
	if p.RunShape.Full.Passes != 1 || p.RunShape.Full.Arm != "full" ||
		p.RunShape.Full.MachineBudgetHours > 12 || p.RunShape.Full.PerJobTimeout != "45m" {
		t.Errorf("full run shape drift: %+v", p.RunShape.Full)
	}
	if p.RunShape.Anchor.Passes != 1 || p.RunShape.Anchor.MachineBudgetHours > 12 ||
		p.RunShape.Anchor.Tasks != "eval/bench/anchor.yaml" || p.RunShape.Anchor.Arm != "single" {
		t.Errorf("anchor run shape drift: %+v", p.RunShape.Anchor)
	}

	// The protocol roster must be the probe's roster, with a digest for each.
	if len(p.Roster) != len(probeModels) {
		t.Fatalf("protocol roster = %d, probeModels = %d", len(p.Roster), len(probeModels))
	}
	for i, m := range probeModels {
		e := p.Roster[i]
		if e.Label != m.Label || e.Model != m.Model || e.Family != m.Family {
			t.Errorf("roster[%d] = %+v, want label/model/family %s/%s/%s",
				i, e, m.Label, m.Model, m.Family)
		}
		if e.Digest == "" {
			t.Errorf("roster[%d] %s has no digest", i, e.Label)
		}
	}
	if p.Embeddings.Model == "" || p.Embeddings.Digest == "" {
		t.Error("embedding model/digest is not pinned")
	}

	// The generated canonical config must agree with the protocol. This is the
	// guard against a code change silently altering the frozen run.
	m, ok := modelByLabel("ornith9b")
	if !ok {
		t.Fatal("ornith9b missing from probeModels")
	}
	a, ok := namedArm("full")
	if !ok {
		t.Fatal("full arm missing")
	}
	raw := probeConfigYAML(m, a, "off", "127.0.0.1:7420", false, probePolicy{JudgeModel: "granite4.2:3b"})
	var cfg struct {
		Personas  map[string]protocolRole `yaml:"personas"`
		Inference struct {
			MaxOutputTokens int    `yaml:"max_output_tokens"`
			Think           bool   `yaml:"think"`
			JudgmentSeed    string `yaml:"judgment_seed"`
		} `yaml:"inference"`
		Execution struct {
			DivergenceCandidates int `yaml:"divergence_candidates"`
		} `yaml:"execution"`
		ContextEngine struct {
			MemoryEmbeddingModel string `yaml:"memory_embedding_model"`
			MemorySearchTopK     int    `yaml:"memory_search_top_k"`
		} `yaml:"context_engine"`
		Limits struct {
			MaxConcurrentJobs int `yaml:"max_concurrent_jobs"`
		} `yaml:"limits"`
		JobPod struct {
			Image string `yaml:"image"`
		} `yaml:"job_pod"`
	}
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("parse canonical config: %v", err)
	}
	for role, want := range wantRoles {
		if got := cfg.Personas[role]; got != want {
			t.Errorf("config role %s = %+v, want %+v", role, got, want)
		}
	}
	if cfg.Execution.DivergenceCandidates != 3 {
		t.Errorf("config divergence_candidates = %d, want 3", cfg.Execution.DivergenceCandidates)
	}
	if cfg.Limits.MaxConcurrentJobs != 1 {
		t.Errorf("config max_concurrent_jobs = %d, want 1", cfg.Limits.MaxConcurrentJobs)
	}
	if cfg.JobPod.Image != p.CanonicalPersonaSet.JobPod.Image {
		t.Errorf("config job_pod.image = %q, want %q", cfg.JobPod.Image, p.CanonicalPersonaSet.JobPod.Image)
	}
	if cfg.ContextEngine.MemoryEmbeddingModel != p.Embeddings.Model ||
		cfg.ContextEngine.MemorySearchTopK != p.CanonicalPersonaSet.Context.MemorySearchTopK {
		t.Errorf("config embedding drift: %+v", cfg.ContextEngine)
	}

	// The anchor is 12 non-held-out tasks, stratified, and every id exists.
	goals, err := loadCorpus(benchTasksPath)
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	byName := map[string]sampleGoal{}
	for _, g := range goals {
		byName[g.Name] = g
	}
	anchored, err := filterByAnchor(goals, "../../eval/bench/anchor.yaml")
	if err != nil {
		t.Fatalf("filterByAnchor: %v", err)
	}
	if len(anchored) != 12 {
		t.Fatalf("anchor tasks = %d, want 12", len(anchored))
	}
	tiers := map[string]int{}
	for _, g := range anchored {
		if g.HeldOut {
			t.Errorf("anchor uses held-out task %s", g.Name)
		}
		tiers[tierOf(g.Source)]++
	}
	wantTiers := map[string]int{"E": 3, "M": 5, "H": 4}
	for k, want := range wantTiers {
		if tiers[k] != want {
			t.Errorf("anchor tier %s = %d, want %d", k, tiers[k], want)
		}
	}
	if _, ok := byName["code-refactor-provided"]; !ok {
		t.Error("anchor repo-backed task is missing from the corpus")
	}
}

// TestCheckRoster pins the digest comparison: a missing model and a changed
// digest are both loud, not silently accepted.
func TestCheckRoster(t *testing.T) {
	roster := []protocolRosterEntry{
		{Label: "a", Model: "m1", Digest: "d1"},
		{Label: "b", Model: "m2", Digest: "d2"},
	}
	if issues := checkRoster(roster, map[string]string{"m1": "d1", "m2": "d2"}); len(issues) != 0 {
		t.Errorf("matching roster reported %v", issues)
	}
	issues := checkRoster(roster, map[string]string{"m1": "d1", "m2": "other"})
	if len(issues) != 1 || !strings.Contains(issues[0], "digest") {
		t.Errorf("digest mismatch issues = %v", issues)
	}
	if issues := checkRoster(roster, map[string]string{}); len(issues) != 2 {
		t.Errorf("missing-model issues = %v, want 2", issues)
	}
}

// TestFilterByAnchorUnknownID proves a typo in the anchor is loud, not a
// silently smaller onboarding signal.
func TestFilterByAnchorUnknownID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "anchor.yaml")
	if err := os.WriteFile(path, []byte("version: 1\ntasks:\n  - id: not-a-task\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := filterByAnchor([]sampleGoal{{Name: "real"}}, path); err == nil {
		t.Error("unknown anchor id was accepted")
	}
}

// TestBenchCodeReferences runs every code task's test_command against the
// corresponding reference solution. It is the proof that each deterministic
// check is satisfiable and correctly written. Skipped when python3 is absent
// (the benchmark itself runs in the Job Pod, which has python).
func TestBenchCodeReferences(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH; reference-solution execution skipped")
	}
	goals, err := loadCorpus(benchTasksPath)
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	var refs struct {
		Solutions map[string]string `yaml:"solutions"`
	}
	readBenchYAML(t, benchReferencePath, &refs)
	if len(refs.Solutions) == 0 {
		t.Fatal("reference.yaml has no solutions")
	}

	codeTasks := 0
	for _, g := range goals {
		if g.Archetype != "code" {
			continue
		}
		codeTasks++
		solution, ok := refs.Solutions[g.Name]
		if !ok {
			t.Errorf("%s: no reference solution", g.Name)
			continue
		}
		if strings.TrimSpace(g.TestCommand) == "" {
			t.Errorf("%s: no test_command", g.Name)
			continue
		}

		filename := "solution.py"
		if g.Name == "code-refactor-provided" {
			filename = "messy.py"
		}
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, filename), []byte(solution), 0o644); err != nil {
			t.Fatalf("%s: write reference: %v", g.Name, err)
		}
		cmdLine := strings.Replace(g.TestCommand, "python -c", python+" -c", 1)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, "sh", "-c", cmdLine)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Errorf("%s: reference failed its own check: %v\n%s", g.Name, err, output)
		}
	}
	if codeTasks != 30 {
		t.Errorf("code tasks = %d, want 30", codeTasks)
	}
}
