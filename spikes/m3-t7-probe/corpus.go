// Corpus loader (M8-T16): reads eval/bench/tasks.yaml (ADR-0061) into the
// probe's sampleGoal shape so `run -corpus` can drive the harder benchmark
// instead of the locked M3-T7 sampleGoals. The corpus is the specification;
// this file is the bridge.
package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// corpusTask mirrors one entry in eval/bench/tasks.yaml. Only the fields the
// probe consumes are declared; the YAML decoder ignores the rest (the rubric,
// checks, expected failure modes are read by the check engine / humans).
type corpusTask struct {
	ID          string   `yaml:"id"`
	Title       string   `yaml:"title"`
	Archetype   string   `yaml:"archetype"`
	Tier        string   `yaml:"tier"`
	Language    string   `yaml:"language"`
	Goal        string   `yaml:"goal"`
	Criteria    []string `yaml:"criteria"`
	Fixture     string   `yaml:"fixture"`
	TestCommand string   `yaml:"test_command"`
}

// corpusFile is the top-level tasks.yaml shape.
type corpusFile struct {
	Version int          `yaml:"version"`
	Tasks   []corpusTask `yaml:"tasks"`
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
	out := make([]sampleGoal, 0, len(cf.Tasks))
	for i, t := range cf.Tasks {
		if t.ID == "" || t.Goal == "" {
			return nil, fmt.Errorf("corpus %s: task %d missing id or goal", path, i+1)
		}
		out = append(out, sampleGoal{
			Number:      i + 1,
			Name:        t.ID,
			Source:      "bench:" + t.Tier,
			Archetype:   t.Archetype,
			Goal:        t.Goal,
			Criteria:    t.Criteria,
			Fixture:     t.Fixture,
			TestCommand: t.TestCommand,
		})
	}
	return out, nil
}
