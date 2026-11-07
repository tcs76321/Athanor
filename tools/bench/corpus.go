// Corpus loader (M8-T16): reads eval/bench/tasks.yaml (ADR-0061) into the
// probe's sampleGoal shape so `run -corpus` can drive the harder benchmark
// instead of the locked M3-T7 sampleGoals. The corpus is the specification;
// this file is the bridge.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// corpusTask mirrors one entry in eval/bench/tasks.yaml. Only the fields the
// probe consumes are declared; the YAML decoder ignores the rest (the rubric,
// checks, expected failure modes are read by the check engine / humans).
type corpusTask struct {
	ID            string         `yaml:"id"`
	Title         string         `yaml:"title"`
	Archetype     string         `yaml:"archetype"`
	Tier          string         `yaml:"tier"`
	Language      string         `yaml:"language"`
	Goal          string         `yaml:"goal"`
	Criteria      []string       `yaml:"criteria"`
	Fixture       string         `yaml:"fixture"`
	Repo          string         `yaml:"repo"`
	TestCommand   string         `yaml:"test_command"`
	Checks        map[string]any `yaml:"checks"`
	HeldOut       bool           `yaml:"held_out"`
	ContextDemand string         `yaml:"context_demand"`
	Category      string         `yaml:"category"`
	// Steps turns the task into a multi-goal sequence in one project
	// (ADR-0116): each step is submitted only after the previous one reaches
	// a terminal state. The top-level Goal is the sequence description; a
	// step supplies its own goal/criteria/check.
	Steps []corpusStep `yaml:"steps"`
}

// corpusStep is one goal in a sequence. It is intentionally a small subset:
// goal, criteria, and the deterministic check (test command or declared
// checks).
type corpusStep struct {
	Goal        string         `yaml:"goal"`
	Criteria    []string       `yaml:"criteria"`
	TestCommand string         `yaml:"test_command"`
	Checks      map[string]any `yaml:"checks"`
}

// indexSummary mirrors the daemon's IndexSummary JSON (M8-T19).
type indexSummary struct {
	Discovered int `json:"discovered"`
	Indexed    int `json:"indexed"`
	Skipped    int `json:"skipped"`
	Chunks     int `json:"chunks"`
	Summarized int `json:"summarized"`
	Embedded   int `json:"embedded"`
	Failed     int `json:"failed"`
}

// corpusFile is the top-level tasks.yaml shape.
type corpusFile struct {
	Version int          `yaml:"version"`
	Tasks   []corpusTask `yaml:"tasks"`
}

// versionLocalPath resolves a corpus-declared path to the corpus version's
// own copy when one exists. A frozen corpus can otherwise depend on a sibling
// version's fixture: two v2 data tasks declare their schema under
// eval/bench/fixtures (v1), and the v2 digest pins only the v2 copies. The
// content is currently identical, but resolving to the version-local file
// makes a v2 run independent of v1 without editing the frozen YAML (ADR-0092).
func versionLocalPath(root, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	rootClean := filepath.Clean(root)
	// v1 lives at the benchmark root itself; there is no sibling version to
	// leak from, so leave its paths unchanged.
	version := filepath.Base(rootClean)
	if version == "" || version == "." || version == "bench" {
		return p
	}
	// Corpus paths are declared relative to the benchmark root (eval/bench).
	// A path already under this version is already local.
	rel, err := filepath.Rel("eval/bench", filepath.Clean(p))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p
	}
	if rel == version || strings.HasPrefix(rel, version+string(filepath.Separator)) {
		return p
	}
	candidate := filepath.Join(rootClean, rel)
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return p
}

// normalizeCorpusPaths rewrites a task's fixture and schema paths to the
// version-local copy when one exists. It is a load-time resolution, not a
// corpus edit; the digest and task semantics are unchanged.
func normalizeCorpusPaths(root string, t *corpusTask) {
	t.Fixture = versionLocalPath(root, t.Fixture)
	t.Repo = versionLocalPath(root, t.Repo)
	if t.Checks == nil {
		return
	}
	if p, ok := t.Checks["json_schema"].(string); ok {
		t.Checks["json_schema"] = versionLocalPath(root, p)
	}
}

func convertSteps(in []corpusStep) []sampleStep {
	if len(in) == 0 {
		return nil
	}
	out := make([]sampleStep, 0, len(in))
	for _, s := range in {
		out = append(out, sampleStep(s))
	}
	return out
}

// loadCorpus reads a bench corpus and converts it to probe goals. Number is
// the 1-based position (the probe's stable ordering); Source records the tier
// ("bench:H" etc.) so reports can group by difficulty.
func loadCorpus(path string) ([]sampleGoal, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading corpus %s: %w", path, err)
	}
	var cf corpusFile
	if err := yaml.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("parsing corpus %s: %w", path, err)
	}
	if len(cf.Tasks) == 0 {
		return nil, fmt.Errorf("corpus %s has no tasks", path)
	}
	root := filepath.Dir(path)
	out := make([]sampleGoal, 0, len(cf.Tasks))
	for i, t := range cf.Tasks {
		if t.ID == "" || t.Goal == "" {
			return nil, fmt.Errorf("corpus %s: task %d missing id or goal", path, i+1)
		}
		normalizeCorpusPaths(root, &t)
		out = append(out, sampleGoal{
			Number:        i + 1,
			Name:          t.ID,
			Source:        "bench:" + t.Tier,
			Archetype:     t.Archetype,
			Goal:          t.Goal,
			Criteria:      t.Criteria,
			Fixture:       t.Fixture,
			Repo:          t.Repo,
			TestCommand:   t.TestCommand,
			Checks:        t.Checks,
			HeldOut:       t.HeldOut,
			ContextDemand: t.ContextDemand,
			Category:      t.Category,
			Steps:         convertSteps(t.Steps),
		})
	}
	return out, nil
}
