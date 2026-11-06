package engine

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/job"
)

type fakeSelector struct {
	choice ModelChoice
	many   []ModelChoice
	seen   []ModelRequest
}

func (f *fakeSelector) Choose(ctx context.Context, req ModelRequest) (ModelChoice, bool) {
	f.seen = append(f.seen, req)
	return f.choice, f.choice.Model != ""
}

func (f *fakeSelector) ChooseMany(ctx context.Context, req ModelRequest, n int) []ModelChoice {
	f.seen = append(f.seen, req)
	if len(f.many) == 0 {
		return nil
	}
	if n > len(f.many) {
		n = len(f.many)
	}
	return f.many[:n]
}

func (f *fakeSelector) Persona(model string) (ModelChoice, bool) {
	if model == "beta" {
		return ModelChoice{Model: "beta", Family: "b", ContextTarget: 65536}, true
	}
	return ModelChoice{}, false
}

func (f *fakeSelector) Observe(ctx context.Context, jobID string, success bool) {}

func (f *fakeSelector) PreferPanel() bool { return false }

// TestSelectorResolvesPhaseAndSyntheticRole proves the composition seam is
// consulted before the legacy persona registry (ADR-0104).
func TestSelectorResolvesPhaseAndSyntheticRole(t *testing.T) {
	e := newEnv(t)
	sel := &fakeSelector{choice: ModelChoice{
		Model: "alpha", Family: "a", ContextTarget: 65536, Temperature: 0.4,
	}}
	e.eng.SetModelSelector(sel)

	p, ok := e.eng.personaFor(context.Background(), "diverging", "main", "text", "project-1", "job-1")
	if !ok || p.Model != "alpha" || p.ContextTarget != 65536 {
		t.Fatalf("phase persona = %+v ok=%v, want alpha", p, ok)
	}
	if len(sel.seen) != 1 || sel.seen[0].Capability != "text" || sel.seen[0].Stance != "generate" {
		t.Fatalf("selector request = %+v", sel.seen)
	}

	// A synthetic role names a model directly and does not re-ask for a phase
	// choice.
	p, ok = e.eng.personaFor(context.Background(), "diverging", PoolModelPrefix+"beta", "text", "", "")
	if !ok || p.Model != "beta" || p.Role != "pool" {
		t.Fatalf("synthetic persona = %+v ok=%v, want beta", p, ok)
	}
}

// TestSelectorCastsDivergenceAcrossModels proves a job with a pool draws its
// candidates from the selector instead of one legacy persona.
func TestSelectorCastsDivergenceAcrossModels(t *testing.T) {
	e := newEnv(t)
	sel := &fakeSelector{
		choice: ModelChoice{Model: "alpha", Family: "a", ContextTarget: 65536},
		many: []ModelChoice{
			{Model: "alpha", Family: "a", ContextTarget: 65536},
			{Model: "beta", Family: "b", ContextTarget: 65536},
			{Model: "gamma", Family: "c", ContextTarget: 65536},
		},
	}
	e.eng.SetModelSelector(sel)

	id := e.submit(t)
	e.eng.Run(context.Background(), id)
	j, err := e.jobs.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateCompleted {
		t.Fatalf("job state = %s, want completed", j.State)
	}
	if len(sel.seen) == 0 {
		t.Fatal("selector was never consulted")
	}
	foundDivergence := false
	for _, req := range sel.seen {
		if req.Phase == "diverging" {
			foundDivergence = true
			if req.Capability == "" {
				t.Errorf("divergence request has no capability: %+v", req)
			}
		}
	}
	if !foundDivergence {
		t.Fatal("divergence did not ask the selector")
	}
}
