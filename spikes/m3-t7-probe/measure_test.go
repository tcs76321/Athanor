package main

import "testing"

// TestGeneratorMeasurements pins M9-T2b: the accept rate is per model, a winner
// of new|previous counts as accepted, blank models are skipped, and Samples is
// the job count (so repeats raise the sample count the MinSamples guard reads).
func TestGeneratorMeasurements(t *testing.T) {
	metrics := []jobMetrics{
		{Model: "ornith-1.5:9b", Winner: "new"},
		{Model: "ornith-1.5:9b", Winner: "none"},
		{Model: "ornith-1.5:9b", Winner: "previous"},
		{Model: "gemma4:12b-mlx", Winner: "new"},
		{Model: "", Winner: "new"},
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
	if got[1].Score < 0.66 || got[1].Score > 0.67 {
		t.Errorf("ornith accept rate = %v, want ~0.667", got[1].Score)
	}
	for _, m := range got {
		if m.Role != "main" {
			t.Errorf("role = %q, want main", m.Role)
		}
	}
}
