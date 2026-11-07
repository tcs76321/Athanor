package onboard

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

type qualificationManifest struct {
	Categories map[string][]string `yaml:"categories"`
}

func loadTaskIDs(t *testing.T, path string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Tasks []struct {
			ID string `yaml:"id"`
		} `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, task := range doc.Tasks {
		if task.ID != "" {
			out[task.ID] = true
		}
	}
	return out
}

// TestQualificationCategoriesTraceToCorpus keeps the qualification screen
// anchored to the work the benchmark measures. Every category must name at
// least one real benchmark or diagnostic task, and every ladder category must
// appear in the manifest.
func TestQualificationCategoriesTraceToCorpus(t *testing.T) {
	root := filepath.Join("..", "..")
	raw, err := os.ReadFile(filepath.Join(root, "eval", "qualification", "categories.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest qualificationManifest
	if err := yaml.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Categories) == 0 {
		t.Fatal("qualification manifest has no categories")
	}
	known := loadTaskIDs(t, filepath.Join(root, "eval", "bench", "v2", "tasks.yaml"))
	for id := range loadTaskIDs(t, filepath.Join(root, "eval", "diagnostic", "tasks.yaml")) {
		known[id] = true
	}
	for category, ids := range manifest.Categories {
		if len(ids) == 0 {
			t.Errorf("category %q has no provenance task", category)
		}
		for _, id := range ids {
			if !known[id] {
				t.Errorf("category %q references unknown task %q", category, id)
			}
		}
	}

	ladder := DefaultLadder()
	ladderCats := map[string]bool{}
	for _, c := range ladder.Generators {
		ladderCats[c.Capability] = true
	}
	for _, c := range ladder.Judges {
		ladderCats[c.Capability] = true
	}
	var missing []string
	for category := range manifest.Categories {
		if !ladderCats[category] {
			missing = append(missing, category)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("manifest categories without a ladder case: %v", missing)
	}
}
