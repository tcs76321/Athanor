package engine

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/store"
)

// divergencePersonas returns the distinct personas used for a job's
// divergence candidates, from the `divergence_candidate` audit rows.
func divergencePersonas(t *testing.T, e *testEnv, jobID string) map[string]int {
	t.Helper()
	events, err := e.db.QueryEvents(context.Background(), store.EventFilter{JobID: jobID, Category: "jobs"})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, ev := range events {
		var d struct {
			Event   string `json:"event"`
			Persona string `json:"persona"`
		}
		if json.Unmarshal([]byte(ev.DataJSON), &d) != nil {
			continue
		}
		if d.Event == "divergence_candidate" {
			out[d.Persona]++
		}
	}
	return out
}

// TestHeterogeneousDiversity_UsesTwoPersonas is the F4-T5 bar: a non-trivial
// task's candidates come from more than one generator persona.
func TestHeterogeneousDiversity_UsesTwoPersonas(t *testing.T) {
	e := newEnv(t)
	jobID := e.submitWithCriteria(t, "text",
		"Write a short essay about local-first software.",
		[]string{"one", "two", "three"})
	e.eng.Run(context.Background(), jobID)

	if got := divergencePersonas(t, e, jobID); len(got) < 2 {
		t.Fatalf("divergence personas = %v, want >= 2 distinct (heterogeneous diversity)", got)
	}
}

// TestHeterogeneousDiversity_DisabledIsSinglePersona proves the opt-out:
// setting heterogeneous_diversity=false pins divergence to one persona.
func TestHeterogeneousDiversity_DisabledIsSinglePersona(t *testing.T) {
	e := newEnvWithCfg(t, func(cfg *config.Config) {
		off := false
		cfg.Execution.Policy.HeterogeneousDiversity = &off
	})
	jobID := e.submitWithCriteria(t, "text",
		"Write a short essay about local-first software.",
		[]string{"one", "two", "three"})
	e.eng.Run(context.Background(), jobID)

	if got := divergencePersonas(t, e, jobID); len(got) != 1 {
		t.Fatalf("divergence personas = %v, want exactly 1", got)
	}
}

// TestDiversityReroll_DiscardsBelowFloor proves the F4-T5 bounded re-roll:
// a below-floor batch is dismissed (rejected) and a fresh batch generated,
// so only the kept batch remains draft.
func TestDiversityReroll_DiscardsBelowFloor(t *testing.T) {
	one := 1
	e := newEnvWithCfg(t, func(cfg *config.Config) {
		cfg.Execution.Policy.MaxDiversityRerolls = &one
		floor := 0.99 // unreachable for the fake's identical candidates
		cfg.Execution.Policy.JaccardFloor = &floor
	})
	jobID := e.submitWithCriteria(t, "text",
		"Write a short essay about local-first software.",
		[]string{"one", "two", "three"})
	e.eng.Run(context.Background(), jobID)

	if _, ok := eventField(t, e, jobID, "divergence_reroll", "attempt"); !ok {
		t.Fatal("no divergence_reroll audit row")
	}
	arts, err := e.artifacts.ListByJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	draft, rejected := 0, 0
	for _, a := range arts {
		if a.Kind != artifact.KindProposal {
			continue
		}
		switch a.Status {
		case artifact.StatusDraft:
			draft++
		case artifact.StatusRejected:
			rejected++
		}
	}
	if draft != 3 || rejected != 3 {
		t.Fatalf("draft=%d rejected=%d, want 3/3 (one batch kept, one discarded)", draft, rejected)
	}
}

// TestCostTieWins pins the F4-T6 rule: a near-tie on score at strictly lower
// token cost wins; unknown or higher cost does not.
func TestCostTieWins(t *testing.T) {
	if !costTieWins(0.90, 0.91, 0.02, 100, 200) {
		t.Error("near tie at lower cost should win")
	}
	if costTieWins(0.80, 0.91, 0.02, 100, 200) {
		t.Error("a real quality gap must not be won on cost")
	}
	if costTieWins(0.90, 0.91, 0.02, 200, 100) {
		t.Error("higher cost must not win a tie")
	}
	if costTieWins(0.90, 0.91, 0.02, 0, 100) {
		t.Error("unknown new cost must not win")
	}
}
