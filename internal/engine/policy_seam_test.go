package engine

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/policy"
	"github.com/tcs76321/athanor/internal/project"
)

// stubPolicy returns a fixed plan and records the inputs it saw.
type stubPolicy struct {
	plan policy.Plan
	seen policy.Inputs
}

func (s *stubPolicy) Decide(in policy.Inputs) policy.Plan {
	s.seen = in
	return s.plan
}

func TestPlanFor_UsesPolicySeam(t *testing.T) {
	stub := &stubPolicy{plan: policy.Plan{
		Candidates: 1, MaxReflectionLoops: 0, JudgeMode: policy.JudgeVerifier, JudgeCount: 3,
	}}
	e := &Engine{policy: stub}
	got := e.planFor(context.Background(), job.Job{}, project.Project{Archetype: "code"}, project.Task{})
	if got.Candidates != 1 || got.MaxReflectionLoops != 0 ||
		got.JudgeMode != policy.JudgeVerifier || got.JudgeCount != 3 {
		t.Fatalf("planFor = %+v, want the stub's plan", got)
	}
	if stub.seen.Features.Archetype != "code" {
		t.Errorf("policy saw archetype %q, want code", stub.seen.Features.Archetype)
	}
}

func TestPlanFor_NilPolicyUsesDefault(t *testing.T) {
	e := &Engine{}
	got := e.planFor(context.Background(), job.Job{}, project.Project{Archetype: "text"}, project.Task{})
	if got.Candidates != 1 {
		t.Errorf("Candidates = %d, want 1 (default floors at one)", got.Candidates)
	}
	if got.MaxReflectionLoops != policy.DefaultReflectionLoops {
		t.Errorf("MaxReflectionLoops = %d, want %d", got.MaxReflectionLoops, policy.DefaultReflectionLoops)
	}
	if got.JudgeMode != policy.JudgeLLM || got.JudgeCount != 1 {
		t.Errorf("judge = %s/%d, want llm/1", got.JudgeMode, got.JudgeCount)
	}
}
