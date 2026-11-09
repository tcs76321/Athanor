package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadQualityRubricV3(t *testing.T) {
	r, err := loadQualityRubric(benchV3Rubric)
	if err != nil {
		t.Fatalf("loadQualityRubric: %v", err)
	}
	if r.ID != "quality-v1" {
		t.Errorf("id = %q, want quality-v1", r.ID)
	}
	if len(r.Dimensions) != 8 {
		t.Errorf("dimensions = %d, want 8", len(r.Dimensions))
	}
	for _, name := range []string{"correctness", "depth", "originality", "appropriate_complexity"} {
		if _, ok := r.Dimensions[name]; !ok {
			t.Errorf("missing dimension %q", name)
		}
	}
}

func TestParseQualityContent(t *testing.T) {
	content := `{"scores":{"correctness":5,"depth":4,"originality":3.5,"bloat":9},"overall":4.2,"rationale":"solid"}`
	got, err := parseQualityContent(content)
	if err != nil {
		t.Fatalf("parseQualityContent: %v", err)
	}
	if got.Overall != 4.2 {
		t.Errorf("overall = %v, want 4.2", got.Overall)
	}
	if got.Scores["depth"] != 4 {
		t.Errorf("depth = %v, want 4", got.Scores["depth"])
	}
	if got.Scores["bloat"] != 5 {
		t.Errorf("out-of-range score was not clamped: %v", got.Scores["bloat"])
	}
	if got.Rationale != "solid" {
		t.Errorf("rationale = %q", got.Rationale)
	}

	// A missing overall falls back to the mean; a non-JSON body is an error.
	got, err = parseQualityContent(`{"scores":{"a":2,"b":4}}`)
	if err != nil || got.Overall != 3 {
		t.Errorf("mean fallback = %+v / %v, want 3", got, err)
	}
	if _, err := parseQualityContent("no json here"); err == nil {
		t.Error("non-JSON content accepted")
	}
}

func TestQualityPromptIncludesRubricAndArtifact(t *testing.T) {
	r, err := loadQualityRubric(benchV3Rubric)
	if err != nil {
		t.Fatal(err)
	}
	p := qualityPrompt(r, collectedArtifact{GoalName: "g", Text: "the artifact"})
	for _, want := range []string{"RUBRIC", "appropriate_complexity", "the artifact", `"scores"`, "bloat"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	long := qualityPrompt(r, collectedArtifact{GoalName: "g", Text: strings.Repeat("x", 20000)})
	if !strings.Contains(long, "truncated") {
		t.Error("long artifact was not marked truncated")
	}
}

func TestQualityAgreement(t *testing.T) {
	mk := func(goal, arm string, run int, judge string, overall float64) qualityScore {
		return qualityScore{GoalName: goal, Arm: arm, Run: run, Judge: judge, Overall: overall}
	}
	same := []qualityScore{
		mk("a", "single", 1, "j1", 1), mk("a", "single", 1, "j2", 1),
		mk("b", "single", 1, "j1", 2), mk("b", "single", 1, "j2", 2),
		mk("c", "single", 1, "j1", 3), mk("c", "single", 1, "j2", 3),
	}
	if got := qualityAgreement(same); got < 0.999 {
		t.Errorf("identical judges agreement = %v, want ~1", got)
	}
	inverse := []qualityScore{
		mk("a", "single", 1, "j1", 1), mk("a", "single", 1, "j2", 3),
		mk("b", "single", 1, "j1", 2), mk("b", "single", 1, "j2", 2),
		mk("c", "single", 1, "j1", 3), mk("c", "single", 1, "j2", 1),
	}
	if got := qualityAgreement(inverse); got > -0.999 {
		t.Errorf("inverse judges agreement = %v, want ~-1", got)
	}
	if got := qualityAgreement(same[:3]); got != 0 {
		t.Errorf("one judge agreement = %v, want 0", got)
	}
}

func TestCallQualityJudge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ollamaChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if req.Model != "judge-model" {
			t.Errorf("model = %q", req.Model)
		}
		if len(req.Messages) != 1 || !strings.Contains(req.Messages[0]["content"], "RUBRIC") {
			t.Errorf("messages = %+v", req.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"content":"{\"scores\":{\"depth\":4},\"overall\":4,\"rationale\":\"ok\"}"},"done":true}`))
	}))
	defer srv.Close()

	got, err := callQualityJudge(srv.URL, "judge-model", "RUBRIC artifact", 1)
	if err != nil {
		t.Fatalf("callQualityJudge: %v", err)
	}
	if got.Overall != 4 || got.Scores["depth"] != 4 || got.Rationale != "ok" {
		t.Errorf("score = %+v", got)
	}
	if got.Raw == "" {
		t.Error("raw response was not recorded")
	}
}

func TestQualitySeedDeterministic(t *testing.T) {
	first := qualitySeed("a", "j")
	second := qualitySeed("a", "j")
	if first != second {
		t.Error("seed is not deterministic")
	}
	if first == qualitySeed("a", "k") {
		t.Error("seed does not depend on the judge")
	}
}

func TestLoadArtifactsMergesTree(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a/out", "b/out"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
		rows := []collectedArtifact{{GoalName: name, Arm: "single", Run: 1, ArtifactID: name, Text: "x"}}
		raw, _ := json.Marshal(rows)
		if err := os.WriteFile(filepath.Join(dir, name, "artifacts.json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := loadArtifacts(dir)
	if err != nil {
		t.Fatalf("loadArtifacts: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("artifacts = %d, want 2", len(got))
	}
}
