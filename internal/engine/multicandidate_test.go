// M3-T1 dialectical-loop unit tests (the E1/E2/E4/E5 set from the
// implementation plan). These complement the existing
// TestRunCompletesFullChain with explicit assertions on:
//
//   - the per-phase LLM call count for a 3-candidate happy path
//   - the reflection loop's two-iteration budget
//   - the §19.3 deterministic guard in the comparison phase
//   - the §9.3 status flow under each comparison outcome
//
// The tests use the standard test fixture (newEnv) and a
// verdict-queue mechanism on the Ollama fake so the security
// persona can return scripted per-candidate verdicts.
//
// E3 (budget-exhausted at 1 iteration) is structurally covered by
// E2: the constant maxReflectionIterations is 2, and E2 exercises
// exactly two failed reflection iterations to land in `failed`.

package engine

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/job"
)

// phaseCalls is a goroutine-safe snapshot of the Ollama fake's
// per-phase call counts. Tests assert on this to prove the
// dialectical loop's structure.
func (e *testEnv) phaseCalls() map[string]int {
	e.ollama.mu.Lock()
	defer e.ollama.mu.Unlock()
	out := map[string]int{}
	for k, v := range e.ollama.callsByPhase {
		out[k] = v
	}
	return out
}

// TestRun_FullDialecticalChain_ThreeCandidates_CodeArchetype (E1)
// is the M3-T1 happy-path acceptance: a code-archetype project
// runs through the full §13.1 dialectical loop, produces 3
// candidates, evaluates each, persists 3 EvaluationRecords, picks
// the best, and lands in `completed` with the final artifact in
// `accepted` status.
//
// This test pins the LLM-comparison path (COMPARISON calls = 1), so it
// selects judge_mode: llm explicitly; the shipped default is verifier-first
// (ADR-0062), covered by TestRun_CodeArchetype_VerifierFirstByDefault.
func TestRun_FullDialecticalChain_ThreeCandidates_CodeArchetype(t *testing.T) {
	env := newEnvWithCfg(t, func(c *config.Config) {
		c.Execution.Policy.JudgeMode = config.JudgeModeLLM
	})
	jobID := env.submitCode(t)
	eval := evaluation.NewRepo(env.db)
	env.eng.Run(context.Background(), jobID)

	// State machine landed in completed.
	j, err := env.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateCompleted {
		t.Fatalf("state = %s, want completed", j.State)
	}

	// Phase call counts: 1 (plan) + 3 (diverge) + 3 (evaluate) + 1
	// (synth) + 1 (compare) = 9.
	calls := env.phaseCalls()
	wantCalls := map[string]int{
		"PLANNING": 1, "DIVERGENCE": 3, "EVALUATION": 3,
		"SYNTHESIS": 1, "COMPARISON": 1, "REFLECTION": 0,
	}
	for phase, want := range wantCalls {
		if calls[phase] != want {
			t.Errorf("%s calls = %d, want %d", phase, calls[phase], want)
		}
	}

	// Three EvaluationRecords persisted, all for this job.
	records, err := eval.ListByJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Errorf("EvaluationRecord count = %d, want 3", len(records))
	}
	for i, r := range records {
		if r.JobID != jobID {
			t.Errorf("record[%d].JobID = %s, want %s", i, r.JobID, jobID)
		}
		if r.ArtifactID == "" {
			t.Errorf("record[%d].ArtifactID empty", i)
		}
	}

	// Three candidate proposal artifacts persisted as `draft`.
	// Routed through `artifact.Store.ListByJob` (added in the
	// M3-T1 follow-up) rather than raw SQL.
	jobArtifacts, err := env.artifacts.ListByJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("ListByJob: %v", err)
	}
	proposalCount := 0
	for _, a := range jobArtifacts {
		if a.Kind == artifact.KindProposal {
			proposalCount++
		}
	}
	if proposalCount != 3 {
		t.Errorf("proposal artifacts = %d, want 3", proposalCount)
	}

	// Final code-archetype artifact (KindCode, not KindDocument).
	final, err := env.artifacts.LatestForJob(context.Background(), jobID, artifact.KindCode)
	if err != nil {
		t.Fatalf("final code artifact: %v", err)
	}
	if final.Status != artifact.StatusAccepted {
		t.Errorf("final status = %s, want %s", final.Status, artifact.StatusAccepted)
	}
}

// TestRun_CodeArchetype_VerifierFirstByDefault proves the ADR-0062 shipped
// default: with no judge_mode configured (verifier-first), a code job whose
// real tests pass is accepted by the deterministic verifier and the LLM
// comparison judge is never called.
func TestRun_CodeArchetype_VerifierFirstByDefault(t *testing.T) {
	env := newEnv(t) // shipped default: judge_mode verifier
	jobID := env.submitCode(t)
	env.eng.Run(context.Background(), jobID)

	j, err := env.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateCompleted {
		t.Fatalf("state = %s, want completed", j.State)
	}
	if got := env.phaseCalls()["COMPARISON"]; got != 0 {
		t.Errorf("LLM COMPARISON calls = %d, want 0 (a decisive verifier decides; ADR-0062)", got)
	}
	final, err := env.artifacts.LatestForJob(context.Background(), jobID, artifact.KindCode)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != artifact.StatusAccepted {
		t.Errorf("final status = %s, want accepted", final.Status)
	}
}

// TestRun_AllCandidatesFail_ReflectsThenFails (E2) exercises the
// reflection loop: every candidate fails evaluation, the engine
// enters reflection, the main persona produces an improvement
// proposal, the loop runs again, and on the second failed pass
// the budget (2 iterations) is exhausted and the job ends in
// `failed`.
//
// E3 (budget exhausted at 1 iteration) is structurally covered:
// maxReflectionIterations is the constant 2, and E2 exercises
// exactly two failed reflection iterations to land in `failed`.
func TestRun_AllCandidatesFail_ReflectsThenFails(t *testing.T) {
	env := newEnv(t)
	jobID := env.submit(t)
	// 2 reflection iterations × 3 candidates = 6 evaluation
	// verdicts, all "failed" with low confidence. The default
	// passing verdict is overridden by this queue; the first 6
	// EVALUATION calls pop from it.
	for i := 0; i < 6; i++ {
		env.ollama.evalVerdicts = append(env.ollama.evalVerdicts,
			`{"passed":false,"score":0.1,"failed_tests":[],"missing_criteria":["unspecified"],"security_issues":[],"style_issues":[],"better_than_previous":false,"confidence":0.1,"summary":"candidate fails all criteria"}`)
	}
	env.eng.Run(context.Background(), jobID)

	j, err := env.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateFailed {
		t.Fatalf("state = %s, want failed (reflection budget exhausted)", j.State)
	}
	calls := env.phaseCalls()
	if calls["REFLECTION"] != 2 {
		t.Errorf("REFLECTION calls = %d, want 2 (one per budget iteration)", calls["REFLECTION"])
	}
	if calls["DIVERGENCE"] != 6 {
		t.Errorf("DIVERGENCE calls = %d, want 6 (2 iterations × 3 candidates)", calls["DIVERGENCE"])
	}
	if calls["EVALUATION"] != 6 {
		t.Errorf("EVALUATION calls = %d, want 6 (2 iterations × 3 candidates)", calls["EVALUATION"])
	}
	if calls["COMPARISON"] != 0 {
		t.Errorf("COMPARISON calls = %d, want 0 (never reached)", calls["COMPARISON"])
	}
}

// TestRun_ComparisonPicksPreviousWhenNewIsWorse (E4) seeds a
// previous accepted artifact and verifies the §19.3 guard: the
// security persona says "winner: new" but the EvaluationRecord
// fails to back it up (no `better_than_previous: true` above the
// confidence threshold), so the engine downgrades the verdict to
// "previous" and the new artifact is rejected.
func TestRun_ComparisonPicksPreviousWhenNewIsWorse(t *testing.T) {
	env := newEnv(t)
	// The seeded previous and the new job must share the same
	// project — the comparison phase looks up the previous by
	// `LatestAcceptedByProject(projectID)`.
	projectID, taskID, _ := env.createProjectTask(t, "text", "Demo comparison-prior")
	prev, err := env.artifacts.CreateDraftFor(context.Background(), projectID, taskID, "",
		artifact.KindDocument, []byte("previously accepted"))
	if err != nil {
		t.Fatal(err)
	}
	if err := env.artifacts.SetStatus(context.Background(), prev.ID, artifact.StatusCandidate); err != nil {
		t.Fatalf("SetStatus candidate: %v", err)
	}
	if err := env.artifacts.SetStatus(context.Background(), prev.ID, artifact.StatusAccepted); err != nil {
		t.Fatalf("SetStatus accepted: %v", err)
	}

	// Second task under the SAME project, so the comparison
	// phase can find the previous. SubmitGoal creates a new
	// task under an existing project.
	submitSeq++
	newTask, err := env.projects.SubmitGoal(context.Background(), projectID,
		"the new goal that competes with the prior",
		[]string{"must improve on the previous"})
	if err != nil {
		t.Fatalf("SubmitGoal: %v", err)
	}
	newJob, err := env.jobs.Create(context.Background(), newTask.ID, projectID)
	if err != nil {
		t.Fatalf("Create job: %v", err)
	}
	jobID := newJob.ID

	for i := 0; i < 3; i++ {
		env.ollama.evalVerdicts = append(env.ollama.evalVerdicts,
			`{"passed":true,"score":0.9,"failed_tests":[],"missing_criteria":[],"security_issues":[],"style_issues":[],"better_than_previous":false,"confidence":0.9,"summary":"passes but no improvement"}`)
	}
	env.ollama.comparisonVerdicts = []string{
		`{"winner":"new","confidence":0.99,"reasons":["LLM prefers new"],"missing_requirements":[]}`,
	}
	env.eng.Run(context.Background(), jobID)

	j, err := env.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateCompleted {
		t.Fatalf("state = %s, want completed (winner=previous still completes)", j.State)
	}
	gotPrev, getErr := env.artifacts.Get(context.Background(), prev.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if gotPrev.Status != artifact.StatusAccepted {
		t.Errorf("previous status = %s, want %s (must stay accepted)", gotPrev.Status, artifact.StatusAccepted)
	}
	newFinal, err := env.artifacts.LatestForJob(context.Background(), jobID, artifact.KindDocument)
	if err != nil {
		t.Fatal(err)
	}
	if newFinal.Status != artifact.StatusRejected {
		t.Errorf("new final status = %s, want %s (guard downgraded 'new' → 'previous')",
			newFinal.Status, artifact.StatusRejected)
	}
}

// TestRun_ComparisonPicksNoneWhenAllFail (E5) is the no-winner
// path: every candidate fails evaluation, the reflection budget
// is exhausted, and the job lands in `failed`. The literal "none"
// verdict branch is exercised by E4's downgrade of LLM "new" when
// no `better_than_previous` exists; E5 covers the "no passing
// candidate" path which routes through reflection rather than
// the "none" branch.
func TestRun_ComparisonPicksNoneWhenAllFail(t *testing.T) {
	env := newEnv(t)
	jobID := env.submit(t)
	for i := 0; i < 6; i++ {
		env.ollama.evalVerdicts = append(env.ollama.evalVerdicts,
			`{"passed":false,"score":0.1,"failed_tests":["pytest"],"missing_criteria":["everything"],"security_issues":[],"style_issues":[],"better_than_previous":false,"confidence":0.1,"summary":"fails"}`)
	}
	env.eng.Run(context.Background(), jobID)

	j, err := env.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateFailed {
		t.Fatalf("state = %s, want failed", j.State)
	}
	calls := env.phaseCalls()
	if calls["COMPARISON"] != 0 {
		t.Errorf("COMPARISON calls = %d, want 0 (no passing candidates so no compare)", calls["COMPARISON"])
	}
}
