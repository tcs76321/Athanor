package main

import (
	"testing"

	"github.com/tcs76321/athanor/internal/config"
	"gopkg.in/yaml.v3"
)

// TestParseRoleModels pins the -roles parser: role=tag pairs, whitespace
// tolerated, and a loud error on a typo (never a silent fallback to -model).
func TestParseRoleModels(t *testing.T) {
	got, err := parseRoleModels("main=qwen3.8:27b-mlx, tall=granite4.2:30b,security=granite4.2:3b")
	if err != nil {
		t.Fatal(err)
	}
	if got["main"] != "qwen3.8:27b-mlx" || got["tall"] != "granite4.2:30b" || got["security"] != "granite4.2:3b" {
		t.Errorf("parsed = %v", got)
	}
	if _, err := parseRoleModels("main"); err == nil {
		t.Error("an entry without '=' must error")
	}
	if _, err := parseRoleModels("boss=x"); err == nil {
		t.Error("an unknown role must error")
	}
	if got, err := parseRoleModels(""); err != nil || got != nil {
		t.Errorf("empty roles = %v, %v; want nil, nil", got, err)
	}
}

// TestProbeConfigRoleModelsAndDAG pins the heterogeneous per-role models and
// the DAG switch: a real deployment runs different models per role, and the
// generated config must carry them (with each role's declared family) and the
// dag_decomposition flag, and still pass the real loader.
func TestProbeConfigRoleModelsAndDAG(t *testing.T) {
	raw := probeConfigYAML(probeModels[0], arms[0], "off", "127.0.0.1:7420", false, probePolicy{
		RoleModels: map[string]string{
			"main":        "qwen3.8:27b-mlx",
			"tall":        "granite4.2:30b",
			"alternative": "ornith-1.5:9b",
			"wide":        "gemma4:e4b-mlx",
			"security":    "granite4.2:3b",
		},
		DAG: true,
	})
	if _, err := config.Parse([]byte(raw)); err != nil {
		t.Fatalf("heterogeneous config rejected by internal/config: %v\n%s", err, raw)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	p := cfg["personas"].(map[string]any)
	want := map[string]string{
		"main": "qwen3.8:27b-mlx", "tall": "granite4.2:30b",
		"alternative": "ornith-1.5:9b", "wide": "gemma4:e4b-mlx", "security": "granite4.2:3b",
	}
	for role, model := range want {
		if got := p[role].(map[string]any)["model"]; got != model {
			t.Errorf("persona %s model = %v, want %v", role, got, model)
		}
	}
	// Families come from the matrix's true lineage (ornith reports qwen35).
	if fam := p["tall"].(map[string]any)["family"]; fam != "granite" {
		t.Errorf("tall family = %v, want granite", fam)
	}
	if fam := p["alternative"].(map[string]any)["family"]; fam != "qwen35" {
		t.Errorf("alternative family = %v, want qwen35", fam)
	}
	if exec := cfg["execution"].(map[string]any); exec["dag_decomposition"] != true {
		t.Errorf("dag_decomposition = %v, want true", exec["dag_decomposition"])
	}
}
