package engine

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/policy"
	"github.com/tcs76321/athanor/internal/store"
)

// proposalCount returns how many divergence proposal artifacts a job has.
func proposalCount(t *testing.T, e *testEnv, jobID string) int {
	t.Helper()
	arts, err := e.artifacts.ListByJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, a := range arts {
		if a.Kind == artifact.KindProposal {
			n++
		}
	}
	return n
}

// divergencePlanCandidates reads the F4-T1c `compute_planned` audit row
// written at the divergence stage and returns its candidate count.
func divergencePlanCandidates(t *testing.T, e *testEnv, jobID string) (int, bool) {
	t.Helper()
	events, err := e.db.QueryEvents(context.Background(), store.EventFilter{JobID: jobID, Category: "jobs"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		var d struct {
			Event      string `json:"event"`
			Stage      string `json:"stage"`
			Candidates int    `json:"candidates"`
		}
		if json.Unmarshal([]byte(ev.DataJSON), &d) != nil {
			continue
		}
		if d.Event == "compute_planned" && d.Stage == "divergence" {
			return d.Candidates, true
		}
	}
	return 0, false
}

// TestAdaptivePolicy_EasyHintRunsSingleCandidate is the F4-T2 acceptance bar:
// a task the planner marks easy runs N=1 even though the configured ceiling
// is 3 (the criteria count is deliberately 3 so only the hint can make it
// easy).
func TestAdaptivePolicy_EasyHintRunsSingleCandidate(t *testing.T) {
	e := newEnvWithCfg(t, func(cfg *config.Config) {
		cfg.Execution.Policy.ComputePolicy = config.ComputePolicyAdaptive
	})
	e.eng.SetPolicy(policy.Adaptive{})
	e.ollama.setPlanning("A plan.\nDIFFICULTY: easy")

	jobID := e.submitWithCriteria(t, "text",
		"Write a short essay about local-first software.",
		[]string{"one", "two", "three"})
	e.eng.Run(context.Background(), jobID)

	if got := proposalCount(t, e, jobID); got != 1 {
		t.Fatalf("proposal candidates = %d, want 1 (easy hint → N=1)", got)
	}
	cand, ok := divergencePlanCandidates(t, e, jobID)
	if !ok {
		t.Fatal("no compute_planned row at the divergence stage")
	}
	if cand != 1 {
		t.Errorf("compute_planned candidates = %d, want 1", cand)
	}
}

// TestAdaptivePolicy_HardHintKeepsCeiling is the converse: a hard hint leaves
// the configured ceiling intact (Adaptive may only lower compute).
func TestAdaptivePolicy_HardHintKeepsCeiling(t *testing.T) {
	e := newEnvWithCfg(t, func(cfg *config.Config) {
		cfg.Execution.Policy.ComputePolicy = config.ComputePolicyAdaptive
	})
	e.eng.SetPolicy(policy.Adaptive{})
	e.ollama.setPlanning("A plan.\nDIFFICULTY: hard")

	jobID := e.submitWithCriteria(t, "text",
		"Write a short essay about local-first software.",
		[]string{"one", "two", "three"})
	e.eng.Run(context.Background(), jobID)

	if got := proposalCount(t, e, jobID); got != 3 {
		t.Fatalf("proposal candidates = %d, want 3 (hard hint keeps the ceiling)", got)
	}
}

// TestAdaptivePolicy_DefaultReproducesCeiling proves the backward-compat
// contract: with no policy wired (the pre-F4 path) a 3-criteria task still
// runs the full configured candidate count.
func TestAdaptivePolicy_DefaultReproducesCeiling(t *testing.T) {
	e := newEnv(t)
	jobID := e.submitWithCriteria(t, "text",
		"Write a short essay about local-first software.",
		[]string{"one", "two", "three"})
	e.eng.Run(context.Background(), jobID)

	if got := proposalCount(t, e, jobID); got != 3 {
		t.Fatalf("proposal candidates = %d, want 3 (default policy unchanged)", got)
	}
}

// TestParseDifficultyHint pins the parser: the last valid marker wins, case
// is ignored, and an absent or unrecognized marker yields "".
func TestParseDifficultyHint(t *testing.T) {
	cases := []struct{ in, want string }{
		{"blah\nDIFFICULTY: easy", "easy"},
		{"blah\ndifficulty: HARD\n", "hard"},
		{"DIFFICULTY: easy\nrevised\nDIFFICULTY: hard", "hard"},
		{"DIFFICULTY: maybe", ""},
		{"no marker here", ""},
	}
	for _, c := range cases {
		if got := parseDifficultyHint(c.in); got != c.want {
			t.Errorf("parseDifficultyHint(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDecidePlan_JudgePersonaOverride proves F4-T1c: execution.judge_persona
// is a bound on the judgment phases, layered over whatever the policy routed.
func TestDecidePlan_JudgePersonaOverride(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Execution.JudgePersona = "alternative"
	e := &Engine{cfg: cfg, policy: policy.Default{}}
	plan := e.decidePlan(policy.Inputs{})
	if got := e.roleFor(plan, "evaluating", "security"); got != "alternative" {
		t.Errorf("evaluating role = %q, want alternative (judge_persona override)", got)
	}
	if got := e.roleFor(plan, "comparing", "security"); got != "alternative" {
		t.Errorf("comparing role = %q, want alternative (judge_persona override)", got)
	}
	if got := e.roleFor(plan, "planning", "tall"); got != "tall" {
		t.Errorf("planning role = %q, want tall (unrouted phases keep their role)", got)
	}
}
