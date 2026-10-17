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
