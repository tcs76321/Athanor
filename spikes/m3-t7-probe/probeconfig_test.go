package main

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestProbeConfigYAML validates the generated config end-to-end: it must
// parse as YAML, carry the arm's candidate count, map every persona to
// the model under test, meet the code floor on the generation/evaluation
// roles, and grant the code-archetype job-pod tools.
func TestProbeConfigYAML(t *testing.T) {
	roles := []string{"wide", "tall", "main", "security", "alternative"}
	for _, m := range probeModels {
		for _, a := range arms {
			raw := probeConfigYAML(m, a, "off")
			var cfg map[string]any
			if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
				t.Fatalf("config for %s/%s is not valid YAML: %v\n%s", m.Label, a.Name, err, raw)
			}

			exec, ok := cfg["execution"].(map[string]any)
			if !ok {
				t.Fatalf("%s/%s: missing execution block", m.Label, a.Name)
			}
			if got, _ := exec["divergence_candidates"].(int); got != a.Candidates {
				t.Errorf("%s/%s: divergence_candidates = %v, want %d", m.Label, a.Name, exec["divergence_candidates"], a.Candidates)
			}

			personas, ok := cfg["personas"].(map[string]any)
			if !ok {
				t.Fatalf("%s/%s: missing personas block", m.Label, a.Name)
			}
			for _, role := range roles {
				pc, ok := personas[role].(map[string]any)
				if !ok {
					t.Fatalf("%s/%s: missing persona %q", m.Label, a.Name, role)
				}
				if pc["model"] != m.Model {
					t.Errorf("%s/%s: persona %q model = %v, want %q", m.Label, a.Name, role, pc["model"], m.Model)
				}
			}
			// Every role except tall must meet the code floor, since the
			// engine evaluates code candidates on `security` and generates
			// on `main`/`alternative`, and ingests with `wide`.
			for _, role := range []string{"wide", "main", "security", "alternative"} {
				pc := personas[role].(map[string]any)
				if got, _ := pc["context_target"].(int); got < 32768 {
					t.Errorf("%s/%s: persona %q context_target = %v, below coding floor 32768", m.Label, a.Name, role, pc["context_target"])
				}
			}

			jp, ok := cfg["job_pod"].(map[string]any)
			if !ok {
				t.Fatalf("%s/%s: missing job_pod block", m.Label, a.Name)
			}
			tools, _ := jp["default_tools"].([]any)
			if len(tools) != 3 {
				t.Errorf("%s/%s: default_tools = %v, want the 3 code tools", m.Label, a.Name, jp["default_tools"])
			}
			if jp["image"] != "python:3.12-alpine" {
				t.Errorf("%s/%s: job_pod.image = %v, want python:3.12-alpine", m.Label, a.Name, jp["image"])
			}
		}
	}
}

// TestProbeConfigSeedPolicy pins the seed knob passed through to the
// generated config.
func TestProbeConfigSeedPolicy(t *testing.T) {
	raw := probeConfigYAML(probeModels[0], arms[0], "derived")
	var cfg map[string]any
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	inference := cfg["inference"].(map[string]any)
	if inference["judgment_seed"] != "derived" {
		t.Errorf("judgment_seed = %v, want derived", inference["judgment_seed"])
	}
	if inference["json_format"] != true {
		t.Errorf("json_format = %v, want true", inference["json_format"])
	}
}
