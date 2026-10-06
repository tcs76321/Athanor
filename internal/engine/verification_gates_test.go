// F5 acceptance-gate tests (ADR-0058): require_tests_for_code and
// require_documentation_for_code are real, decisive gates, not dormant
// config. The default engine test env disables them (see newEnvWithCfg); these
// tests turn them on.
package engine

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
)

// gateConfig returns a default config with both code gates set explicitly.
func gateConfig(t *testing.T, tests, docs bool) *config.Config {
	t.Helper()
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Execution.RequireTestsForCode = &tests
	cfg.Execution.RequireDocumentationForCode = &docs
	return cfg
}

// TestVerifyCandidate_RequireTestsWiring proves the engine resolves
// execution.require_tests_for_code into the verifier input: with no runner,
// a code candidate is a hard failure when the gate is on and non-decisive
// when it is off.
func TestVerifyCandidate_RequireTestsWiring(t *testing.T) {
	ctx := context.Background()
	p := project.Project{Archetype: project.ArchetypeCode}

	on := &Engine{cfg: gateConfig(t, true, false)}
	res, _, err := on.verifyCandidate(ctx, job.Job{}, p, project.Task{}, "print(1)", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !res.HardDecisive() || res.HardPassed {
		t.Errorf("tests required, none ran: got %+v, want hard failure", res)
	}

	off := &Engine{cfg: gateConfig(t, false, false)}
	res, _, err = off.verifyCandidate(ctx, job.Job{}, p, project.Task{}, "print(1)", 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.HardDecisive() {
		t.Errorf("tests not required: got %+v, want non-decisive", res)
	}
}

// TestVerifyCandidate_RequireDocsWiring proves the docs gate is decisive only
// when required and that a documented candidate passes it.
func TestVerifyCandidate_RequireDocsWiring(t *testing.T) {
	ctx := context.Background()
	p := project.Project{Archetype: project.ArchetypeCode}

	on := &Engine{cfg: gateConfig(t, false, true)}
	res, _, err := on.verifyCandidate(ctx, job.Job{}, p, project.Task{}, "print(1)", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !res.HardDecisive() || res.HardPassed {
		t.Errorf("docs required, none present: got %+v, want hard failure", res)
	}

	documented := "def add(a, b):\n    \"\"\"Return the sum of a and b.\"\"\"\n    return a + b\n"
	res, _, err = on.verifyCandidate(ctx, job.Job{}, p, project.Task{}, documented, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !res.HardDecisive() || !res.HardPassed {
		t.Errorf("docs required, present: got %+v, want pass", res)
	}

	off := &Engine{cfg: gateConfig(t, false, false)}
	res, _, err = off.verifyCandidate(ctx, job.Job{}, p, project.Task{}, "print(1)", 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.HardDecisive() {
		t.Errorf("docs not required: got %+v, want non-decisive", res)
	}
}

// TestCodeJob_FailsWhenGatesUnsatisfied is the end-to-end proof: a code job
// with the tests gate on and no runner cannot be accepted, and exhausts
// reflection into `failed`.
func TestCodeJob_FailsWhenGatesUnsatisfied(t *testing.T) {
	zero := 0
	e := newEnvWithCfg(t, func(c *config.Config) {
		on := true
		c.Execution.RequireTestsForCode = &on
		c.Execution.RequireDocumentationForCode = &on
		c.Execution.MaxReflectionLoops = &zero
	})
	// No runner: verifyCandidate's tests gate is decisive-fail for every
	// candidate.
	e.eng.runner = nil

	jobID := e.submitCode(t)
	e.eng.Run(context.Background(), jobID)

	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateFailed {
		t.Fatalf("state = %s, want failed (code gates unsatisfied)", j.State)
	}
}

// TestCodeJob_CompletesWhenGatesSatisfied is the positive control: with both
// gates on, a job whose candidates carry tests (fake exit 0) and docs is
// accepted normally.
func TestCodeJob_CompletesWhenGatesSatisfied(t *testing.T) {
	e := newEnvWithCfg(t, func(c *config.Config) {
		on := true
		c.Execution.RequireTestsForCode = &on
		c.Execution.RequireDocumentationForCode = &on
	})
	e.ollama.setGeneric("def add(a, b):\n    \"\"\"Return the sum of a and b.\"\"\"\n    return a + b\n")

	jobID := e.submitCode(t)
	e.eng.Run(context.Background(), jobID)

	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateCompleted {
		t.Fatalf("state = %s, want completed (gates satisfied)", j.State)
	}
}
