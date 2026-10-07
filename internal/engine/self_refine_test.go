package engine

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/job"
)

// TestSelfRefineRepairsAndPasses is the M8-T11 bar: with self_refine enabled
// and a cycle where the only candidate fails, the best candidate is repaired
// in place and the passing repair carries the job to completion. The operation
// is audited as `self_refine` with its grounded verdict.
func TestSelfRefineRepairsAndPasses(t *testing.T) {
	e := newEnvWithCfg(t, func(c *config.Config) {
		c.Execution.DivergenceCandidates = 1
		zero := 0
		c.Execution.MaxReflectionLoops = &zero
		yes := true
		c.Execution.SelfRefine = &yes
	})
	e.ollama.setGeneric("The improved artifact.")
	e.ollama.evalVerdicts = []string{
		`{"passed":false,"score":0.2,"failed_tests":[],"missing_criteria":["clarity"],"security_issues":[],"style_issues":[],"better_than_previous":false,"confidence":0.9,"summary":"unclear"}`,
		`{"passed":true,"score":0.9,"failed_tests":[],"missing_criteria":[],"security_issues":[],"style_issues":[],"better_than_previous":true,"confidence":0.9,"summary":"now clear"}`,
	}

	jobID := e.submit(t)
	e.eng.Run(context.Background(), jobID)

	if _, ok := eventField(t, e, jobID, "self_refine", "passed"); !ok {
		t.Fatal("no self_refine audit row")
	}
	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateCompleted {
		t.Fatalf("state = %s, want completed (the passing repair should carry the job)", j.State)
	}
}

// TestSelfRefineDisabledByDefault proves the operation is inert unless
// enabled: a failing cycle reflects (and, with no reflection budget, fails)
// without a self_refine row.
func TestSelfRefineDisabledByDefault(t *testing.T) {
	e := newEnvWithCfg(t, func(c *config.Config) {
		c.Execution.DivergenceCandidates = 1
		zero := 0
		c.Execution.MaxReflectionLoops = &zero
	})
	e.ollama.setGeneric("A result that fails.")
	e.ollama.evalVerdicts = []string{
		`{"passed":false,"score":0.2,"failed_tests":[],"missing_criteria":["clarity"],"security_issues":[],"style_issues":[],"better_than_previous":false,"confidence":0.9,"summary":"unclear"}`,
	}

	jobID := e.submit(t)
	e.eng.Run(context.Background(), jobID)

	if _, ok := eventField(t, e, jobID, "self_refine", "passed"); ok {
		t.Error("self_refine ran while disabled")
	}
}
