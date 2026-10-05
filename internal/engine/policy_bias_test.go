package engine

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/strategy"
)

// TestActiveInsightBiasesPersonaPlan is the F4-T7a "the loop learns" bar: an
// active winning insight that names `alternative` leads the divergence
// personas, and the bias is audited. The insight is promoted from proposed to
// active, so the inert-proposed contract still holds.
func TestActiveInsightBiasesPersonaPlan(t *testing.T) {
	e := newEnv(t)
	repo := strategy.NewRepo(e.db)
	e.eng.SetStrategyInsightSource(repo)

	ctx := context.Background()
	ins, err := repo.CreateInsight(ctx, strategy.Insight{
		Scope: strategy.ScopeGlobal, Polarity: strategy.PolarityWinning,
		Pattern: strategy.Pattern{
			Feature: "diverging.persona", Value: "alternative", Context: "archetype=text",
		},
		Statement: "Divergence led by 'alternative' was accepted more often.",
		Status:    strategy.InsightProposed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetInsightStatus(ctx, ins.ID, strategy.InsightActive); err != nil {
		t.Fatal(err)
	}

	jobID := e.submitWithCriteria(t, "text",
		"Write a short essay about local-first software.",
		[]string{"one", "two", "three"})
	e.eng.Run(ctx, jobID)

	if _, ok := eventField(t, e, jobID, "policy_biased_from_insight", "persona"); !ok {
		t.Fatal("no policy_biased_from_insight audit row")
	}
	if got := divergencePersonas(t, e, jobID); got["alternative"] == 0 {
		t.Fatalf("divergence personas = %v, want alternative to lead", got)
	}
}

// TestReflectionSkippedOnSecurityFailure is the F4-T7b gate: when every
// candidate fails on a security finding, the loop fails fast instead of
// reflecting, and audits the skip.
func TestReflectionSkippedOnSecurityFailure(t *testing.T) {
	e := newEnv(t)
	// Three candidates, each scored with a security issue.
	e.ollama.evalVerdicts = []string{
		`{"passed":false,"score":0.1,"failed_tests":[],"missing_criteria":[],"security_issues":["hardcoded secret"],"style_issues":[],"better_than_previous":false,"confidence":0.9,"summary":"insecure"}`,
		`{"passed":false,"score":0.1,"failed_tests":[],"missing_criteria":[],"security_issues":["hardcoded secret"],"style_issues":[],"better_than_previous":false,"confidence":0.9,"summary":"insecure"}`,
		`{"passed":false,"score":0.1,"failed_tests":[],"missing_criteria":[],"security_issues":["hardcoded secret"],"style_issues":[],"better_than_previous":false,"confidence":0.9,"summary":"insecure"}`,
	}
	jobID := e.submitWithCriteria(t, "text",
		"Write a short essay about local-first software.",
		[]string{"one", "two", "three"})
	e.eng.Run(context.Background(), jobID)

	if _, ok := eventField(t, e, jobID, "reflection_skipped_security", "candidates"); !ok {
		t.Fatal("no reflection_skipped_security audit row")
	}
	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateFailed {
		t.Fatalf("state = %s, want failed (security failure must not reflect)", j.State)
	}
}
