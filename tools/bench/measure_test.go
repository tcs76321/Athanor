package main

import "testing"

// TestGeneratorMeasurements pins M9-T2b: the accept rate is per model, the
// benchmark label (jobPassed) decides acceptance, blank models are skipped,
// and Samples is the job count (so repeats raise the sample count the
// MinSamples guard reads).
func TestGeneratorMeasurements(t *testing.T) {
	metrics := []jobMetrics{
		{Model: "ornith-1.5:9b", Archetype: "code", State: "completed", Winner: "new"},
		{Model: "ornith-1.5:9b", Archetype: "code", State: "completed", Winner: "none"},
		{Model: "ornith-1.5:9b", Archetype: "code", State: "completed", Winner: "previous"},
		{Model: "gemma4:12b-mlx", Archetype: "code", State: "completed", Winner: "new"},
		{Model: "", Archetype: "code", State: "completed", Winner: "new"},
	}
	got := generatorMeasurements(metrics, "main")
	if len(got) != 2 {
		t.Fatalf("measurements = %d, want 2: %+v", len(got), got)
	}
	if got[0].Model != "gemma4:12b-mlx" || got[0].Samples != 1 || got[0].Score != 1.0 {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].Model != "ornith-1.5:9b" || got[1].Samples != 3 {
		t.Errorf("got[1] = %+v", got[1])
	}
	if got[1].Score < 0.33 || got[1].Score > 0.34 {
		t.Errorf("ornith accept rate = %v, want ~0.333", got[1].Score)
	}
	for _, m := range got {
		if m.Role != "main" {
			t.Errorf("role = %q, want main", m.Role)
		}
	}
}

// TestGeneratorMeasurementsUseBenchmarkLabel pins ADR-0092 in the onboarding
// reducer: a recorded test pass counts even when the engine rejected the row;
// an engine accept whose recorded test failed does not.
func TestGeneratorMeasurementsUseBenchmarkLabel(t *testing.T) {
	pass, fail := true, false
	metrics := []jobMetrics{
		{Model: "m", Archetype: "code", State: "failed", Winner: "none", CodeTestPassed: &pass, LabelVersion: benchLabelVersion},
		{Model: "m", Archetype: "code", State: "completed", Winner: "new", CodeTestPassed: &fail, LabelVersion: benchLabelVersion},
	}
	got := generatorMeasurements(metrics, "main")
	if len(got) != 1 || got[0].Samples != 2 {
		t.Fatalf("measurements = %+v, want one with 2 samples", got)
	}
	if got[0].Score != 0.5 {
		t.Errorf("score = %v, want 0.5", got[0].Score)
	}
}
