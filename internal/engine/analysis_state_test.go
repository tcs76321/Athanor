package engine

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/analysis"
)

func TestAnalysisStateRoundTrip(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	_, _, jobID := env.createProjectTask(t, "text", "analysis round trip with enough goal text")

	want := analysis.Analysis{
		Parsed: true,
		Units: []analysis.Unit{
			{ID: "u1", Goal: "parse", Mode: analysis.Direct, Falsifier: "sample parses"},
			{ID: "u2", Goal: "precedence", Mode: analysis.Deliberate, Reason: "hidden constraints"},
		},
	}
	if err := env.eng.setAnalysis(ctx, jobID, want); err != nil {
		t.Fatal(err)
	}
	got, ok := env.eng.getAnalysis(ctx, jobID)
	if !ok {
		t.Fatal("stored analysis was not found")
	}
	if len(got.Units) != 2 || got.Units[1].Mode != analysis.Deliberate {
		t.Errorf("analysis = %+v", got)
	}

	missing, ok := env.eng.getAnalysis(ctx, "no-such-job")
	if ok {
		t.Error("missing analysis reported as found")
	}
	if len(missing.Units) != 1 || missing.Units[0].Mode != analysis.Direct {
		t.Errorf("missing fallback = %+v, want one direct unit", missing)
	}
}
