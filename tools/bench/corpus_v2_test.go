package main

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	benchV2Tasks     = "../../eval/bench/v2/tasks.yaml"
	benchV2Memory    = "../../eval/bench/v2/memory-scripts.yaml"
	benchV2Human     = "../../eval/bench/v2/human-review.yaml"
	benchV2Protocol  = "../../eval/bench/v2/protocol.yaml"
	benchV2Anchor    = "../../eval/bench/v2/anchor.yaml"
	benchV2Reference = "../../eval/bench/v2/reference"
)

// TestBenchCorpusV2 pins the hard H/X composition: 41 tasks, no E/M, the
// designed archetype/tier spread, 10 held-out, and a real deterministic gate.
func TestBenchCorpusV2(t *testing.T) {
	goals, err := loadCorpus(benchV2Tasks)
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	if len(goals) != 41 {
		t.Fatalf("v2 deterministic tasks = %d, want 41", len(goals))
	}
	advisory := map[string]bool{
		"required_substrings_source": true, "preserve_entities_from_fixture": true,
		"every_item_once": true, "must_flag_contradiction": true,
		"must_not_fabricate": true, "no_fabricated_commands": true,
		"max_new_claims": true, "exactly_one_cta": true,
		"required_citations_from_fixture": true,
	}
	ids := map[string]bool{}
	arch, tier := map[string]int{}, map[string]int{}
	heldOut, adversarial := 0, 0
	for _, g := range goals {
		if g.Name == "" || ids[g.Name] {
			t.Errorf("empty or duplicate task id %q", g.Name)
		}
		ids[g.Name] = true
		arch[g.Archetype]++
		tier[tierOf(g.Source)]++
		if g.HeldOut {
			heldOut++
		}
		if g.Category == "adversarial" {
			adversarial++
		}
		if g.ContextDemand == "" {
			t.Errorf("%s: context_demand missing", g.Name)
		}
		switch g.ContextDemand {
		case "short", "long", "repo", "memory":
		default:
			t.Errorf("%s: bad context_demand %q", g.Name, g.ContextDemand)
		}
		if g.Archetype == "code" {
			if strings.TrimSpace(g.TestCommand) == "" {
				t.Errorf("%s: code task has no test_command", g.Name)
			}
			if _, err := os.Stat(filepath.Join(benchV2Reference, g.Name)); err != nil {
				t.Errorf("%s: reference directory missing: %v", g.Name, err)
			}
		} else {
			if len(g.Checks) == 0 {
				t.Errorf("%s: non-code task has no checks", g.Name)
				continue
			}
			deterministic := false
			for kind := range g.Checks {
				if !advisory[kind] {
					deterministic = true
				}
			}
			if !deterministic {
				t.Errorf("%s: every check is advisory", g.Name)
			}
		}
		if g.Fixture != "" {
			if _, err := os.Stat(repoPath(g.Fixture)); err != nil {
				t.Errorf("%s: fixture missing: %v", g.Name, err)
			}
		}
		if g.Repo != "" {
			if _, err := os.Stat(repoPath(g.Repo)); err != nil {
				t.Errorf("%s: repo missing: %v", g.Name, err)
			}
		}
		if schema, ok := g.Checks["json_schema"].(string); ok {
			if _, err := os.Stat(repoPath(schema)); err != nil {
				t.Errorf("%s: json_schema missing: %v", g.Name, err)
			}
		}
	}

	wantArch := map[string]int{"code": 18, "document": 13, "text": 5, "data": 5}
	for k, want := range wantArch {
		if arch[k] != want {
			t.Errorf("archetype %s = %d, want %d", k, arch[k], want)
		}
	}
	if tier["H"] != 26 || tier["X"] != 15 {
		t.Errorf("tiers = %v, want H26/X15", tier)
	}
	if heldOut != 10 {
		t.Errorf("held-out = %d, want 10", heldOut)
	}
	if adversarial != 6 {
		t.Errorf("adversarial = %d, want 6", adversarial)
	}

	// The v2 fairness changes must be present on the H floor.
	byName := map[string]sampleGoal{}
	for _, g := range goals {
		byName[g.Name] = g
	}
	if _, ok := byName["adv-h-unsafe-request"].Checks["must_contain_any"]; !ok {
		t.Error("adv-h-unsafe-request must use must_contain_any in v2")
	}
	for _, id := range []string{"data-json-normalize", "data-h-schema-validate"} {
		if v, _ := byName[id].Checks["json_extract"].(bool); !v {
			t.Errorf("%s: json_extract missing in v2", id)
		}
	}
}

func TestBenchExtrasV2(t *testing.T) {
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
	readBenchYAML(t, benchV2Memory, &mem)
	if len(mem.Scripts) != 4 {
		t.Fatalf("memory scripts = %d, want 4", len(mem.Scripts))
	}
	memHeld := 0
	for _, s := range mem.Scripts {
		if len(s.Steps) < 3 {
			t.Errorf("%s: want >= 3 steps", s.ID)
		}
		if s.HeldOut {
			memHeld++
		}
	}
	if memHeld != 3 {
		t.Errorf("held-out memory = %d, want 3", memHeld)
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
	readBenchYAML(t, benchV2Human, &human)
	if len(human.Tasks) != 6 {
		t.Fatalf("human tasks = %d, want 6", len(human.Tasks))
	}
	humanHeld := 0
	for _, task := range human.Tasks {
		if !task.HumanReview {
			t.Errorf("%s: human_review must be true", task.ID)
		}
		if task.Goal == "" {
			t.Errorf("%s: no goal", task.ID)
		}
		for _, k := range []string{"5", "3", "1"} {
			if task.Rubric[k] == "" {
				t.Errorf("%s: rubric[%s] empty", task.ID, k)
			}
		}
		if task.Fixture != "" {
			if _, err := os.Stat(repoPath(task.Fixture)); err != nil {
				t.Errorf("%s: fixture missing: %v", task.ID, err)
			}
		}
		if task.HeldOut {
			humanHeld++
		}
	}
	if humanHeld != 3 {
		t.Errorf("held-out human = %d, want 3", humanHeld)
	}
	if held := 10 + memHeld + humanHeld; held != 16 {
		t.Errorf("held-out total = %d, want 16 of 51", held)
	}
}

func TestBenchProtocolV2(t *testing.T) {
	p, err := loadProtocol(benchV2Protocol)
	if err != nil {
		t.Fatalf("loadProtocol v2: %v", err)
	}
	if p.Benchmark != "v2" || p.Version != 2 {
		t.Errorf("benchmark=%q version=%d, want v2/2", p.Benchmark, p.Version)
	}
	if p.TaskDigest != "eval/bench/v2/CORPUS.digest" {
		t.Errorf("task digest = %q", p.TaskDigest)
	}
	if p.RunShape.Full.Tasks != "eval/bench/v2/tasks.yaml" {
		t.Errorf("full tasks = %q", p.RunShape.Full.Tasks)
	}
	if p.RunShape.Anchor.Tasks != "eval/bench/v2/anchor.yaml" {
		t.Errorf("anchor tasks = %q", p.RunShape.Anchor.Tasks)
	}
	if p.RunShape.Full.Passes != 1 || p.RunShape.Anchor.Passes != 1 || p.RunShape.Anchor.Arm != "single" {
		t.Errorf("run shape drift: %+v / %+v", p.RunShape.Full, p.RunShape.Anchor)
	}
	if len(p.CanonicalPersonaSet.Roles) != 5 || p.CanonicalPersonaSet.JobPod.Image != "athanor-jobpod:latest" {
		t.Errorf("canonical set drift: %+v", p.CanonicalPersonaSet)
	}
	if len(p.Roster) != len(probeModels) {
		t.Errorf("roster = %d, want %d", len(p.Roster), len(probeModels))
	}

	goals, err := loadCorpus(benchV2Tasks)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]sampleGoal{}
	for _, g := range goals {
		byName[g.Name] = g
	}
	anchored, err := filterByAnchor(goals, benchV2Anchor)
	if err != nil {
		t.Fatalf("filterByAnchor v2: %v", err)
	}
	if len(anchored) != 12 {
		t.Fatalf("v2 anchor = %d, want 12", len(anchored))
	}
	for _, g := range anchored {
		if g.HeldOut {
			t.Errorf("anchor uses held-out task %s", g.Name)
		}
		if tierOf(g.Source) != "H" {
			t.Errorf("anchor task %s tier = %s, want H", g.Name, tierOf(g.Source))
		}
	}
}

// copyBenchTree copies src into dst, preserving relative paths.
func copyBenchTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copy %s: %v", src, err)
	}
}

// TestBenchCodeReferencesV2 executes every v2 code task's real test command
// against its reference solution, with the starter fixture when one exists.
func TestBenchCodeReferencesV2(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	goals, err := loadCorpus(benchV2Tasks)
	if err != nil {
		t.Fatal(err)
	}
	code := 0
	for _, g := range goals {
		if g.Archetype != "code" {
			continue
		}
		code++
		dir := t.TempDir()
		if g.Fixture != "" {
			copyBenchTree(t, repoPath(g.Fixture), dir)
		}
		ref := filepath.Join(benchV2Reference, g.Name)
		if _, err := os.Stat(ref); err != nil {
			t.Errorf("%s: reference missing: %v", g.Name, err)
			continue
		}
		copyBenchTree(t, ref, dir)
		if strings.TrimSpace(g.TestCommand) == "" {
			t.Errorf("%s: no test_command", g.Name)
			continue
		}
		cmdLine := strings.Replace(g.TestCommand, "python -c", python+" -c", 1)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		cmd := exec.CommandContext(ctx, "sh", "-c", cmdLine)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Errorf("%s: reference failed its check: %v\n%s", g.Name, err, out)
		}
	}
	if code != 18 {
		t.Errorf("code tasks = %d, want 18", code)
	}
}

// TestBenchDataChecksV2 runs the v2 data checks on correct fenced outputs.
func TestBenchDataChecksV2(t *testing.T) {
	goals, err := loadCorpus(benchV2Tasks)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]sampleGoal{}
	for _, g := range goals {
		byName[g.Name] = g
	}
	good := map[string]string{
		"x-data-reconcile":     "Here is the array:\n```json\n[{\"id\":1,\"name\":\"Ada\",\"status\":\"active\"},{\"id\":2,\"name\":\"Sam\",\"status\":\"active\"},{\"id\":3,\"name\":\"Lin\",\"status\":\"active\"}]\n```\n",
		"x-data-schema-evolve": "```json\n[{\"id\":7,\"created_at\":\"2026-03-01T10:00:00Z\",\"severity\":\"warning\"},{\"id\":9,\"created_at\":\"2026-03-02T10:30:00Z\",\"severity\":\"error\"}]\n```\n",
		"x-data-aggregate":     "```json\n[{\"user\":\"ada\",\"total\":12,\"count\":2},{\"user\":\"sam\",\"total\":5,\"count\":1}]\n```\n",
	}
	for name, output := range good {
		g := byName[name]
		checks := map[string]any{}
		for k, v := range g.Checks {
			checks[k] = v
		}
		if p, ok := checks["json_schema"].(string); ok {
			checks["json_schema"] = repoPath(p)
		}
		if pass, results := runChecks(checks, output); !pass {
			t.Errorf("%s: good output failed: %+v", name, results)
		}
	}
}
