package main

import "testing"

func TestSampleGoals_SetIsCompleteAndUnique(t *testing.T) {
	if len(sampleGoals) != 10 {
		t.Fatalf("sampleGoals = %d, want 10", len(sampleGoals))
	}
	wantArchetypes := map[string]int{"text": 2, "code": 6, "document": 2}
	gotArchetypes := map[string]int{}
	names := map[string]bool{}
	numbers := map[int]bool{}
	for _, g := range sampleGoals {
		if g.Name == "" || g.Goal == "" || len(g.Criteria) == 0 {
			t.Errorf("goal %d is incomplete: %+v", g.Number, g)
		}
		if names[g.Name] {
			t.Errorf("duplicate goal name %q", g.Name)
		}
		names[g.Name] = true
		if numbers[g.Number] {
			t.Errorf("duplicate goal number %d", g.Number)
		}
		numbers[g.Number] = true
		if g.Source != "M1-T8" && g.Source != "M3-T2" {
			t.Errorf("goal %d has unknown source %q", g.Number, g.Source)
		}
		gotArchetypes[g.Archetype]++
	}
	for arch, want := range wantArchetypes {
		if gotArchetypes[arch] != want {
			t.Errorf("archetype %q count = %d, want %d", arch, gotArchetypes[arch], want)
		}
	}
}

func TestArms(t *testing.T) {
	if len(arms) != 2 {
		t.Fatalf("arms = %d, want 2", len(arms))
	}
	byName := map[string]arm{}
	for _, a := range arms {
		byName[a.Name] = a
	}
	if d := byName["dialectical"]; d.Candidates != 3 || d.Runs != 3 {
		t.Errorf("dialectical arm = %+v, want candidates=3 runs=3", d)
	}
	if s := byName["single"]; s.Candidates != 1 || s.Runs != 1 {
		t.Errorf("single arm = %+v, want candidates=1 runs=1", s)
	}
}

func TestProbeModels(t *testing.T) {
	if len(probeModels) == 0 {
		t.Fatal("probeModels is empty")
	}
	generators := map[string]bool{}
	for _, m := range probeModels {
		if m.Label == "" || m.Model == "" {
			t.Errorf("incomplete model %+v", m)
		}
		if m.ContextTarget < 32768 {
			t.Errorf("model %q context_target = %d, below the code floor 32768", m.Label, m.ContextTarget)
		}
		generators[m.Model] = true
	}
	if len(judgeModels) < 2 {
		t.Errorf("judgeModels = %v, want at least two independent third-party judges", judgeModels)
	}
	seen := map[string]bool{}
	for _, j := range judgeModels {
		if generators[j] {
			t.Errorf("judge %q is also a generator; judges must be independent", j)
		}
		if seen[j] {
			t.Errorf("duplicate judge %q", j)
		}
		seen[j] = true
	}
}
