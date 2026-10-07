package engine

import (
	"context"
	"errors"
	"time"

	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/toolenvelope"
	"github.com/tcs76321/athanor/internal/verify"
)

// verifyCandidate runs the F4-T3 deterministic verifiers against one
// candidate. For code it materializes the candidate in the Job Pod and runs
// the project's real test command — moved here (from M2-T4b) to a
// per-candidate call so every candidate is tested, not just the latest
// proposal. Other archetypes are checked structurally. idx is the candidate
// index for the running_tests substate.
//
// The returned Result is non-decisive when no verifier could evaluate the
// input; the caller then falls through to the LLM judge.
func (e *Engine) verifyCandidate(ctx context.Context, j job.Job, p project.Project, t project.Task,
	content string, idx int) (verify.Result, verify.Input, error) {

	in := verify.Input{
		Archetype: p.Archetype,
		Content:   content,
		Criteria:  t.Criteria,
	}
	// F5 (ADR-0058): the code acceptance gates. An explicit false restores
	// the pre-F5 behavior; the config resolver applies the true default.
	if e.cfg != nil {
		in.RequireTests = e.cfg.Execution.RequireTestsForCodeValue()
		in.RequireDocs = e.cfg.Execution.RequireDocumentationForCodeValue()
	}
	if p.Archetype != project.ArchetypeCode || e.runner == nil {
		return e.verifierRegistry().Run(in), in, nil
	}

	// Bug fix (probe soak): bound every pod tool call by the evaluating
	// phase's wall-time budget. The per-phase budget was applied only to LLM
	// calls (phases.go), so a hanging command — an infinite loop, or a test
	// blocked on stdin — stalled the job forever. A tool deadline soft-fails
	// the candidate (TestsPassed=false) rather than failing the job, so the
	// loop keeps moving.
	tctx, cancel := e.toolContext(ctx)
	defer cancel()

	// M2-T4b.5 (ADR-0024 §2): make the job's Job Pod exist before the pod
	// sub-steps, so the per-job token the runner presents is available.
	e.ensurePod(tctx, j)
	if err := e.runCodeInPod(tctx, j, p, t, content); err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			return verify.Result{}, in, err
		}
		e.audit(ctx, j.ID, map[string]any{
			"event": "tool_deadline_exceeded", "tool": "execute_code", "candidate": idx,
		})
	}

	testCommand := p.TestCommand()
	in.TestRan = true
	in.TestCommand = testCommand
	// §8.1 "tracked sub-state": running_tests is an event, not a column.
	e.audit(ctx, j.ID, map[string]any{
		"event": "substate_entered", "phase": string(llm.PhaseEvaluating),
		"substate": "running_tests", "candidate": idx,
	})
	res, runErr := e.runner.RunTests(tctx, j.ID, toolenvelope.ExecuteRequest{Command: testCommand})
	exitCode := 0
	switch {
	case runErr == nil:
		exitCode = res.ExitCode
		in.TestsPassed = res.ExitCode == 0
	case errors.Is(runErr, context.DeadlineExceeded):
		// The test command hung; reject this candidate, keep the job alive.
		e.audit(ctx, j.ID, map[string]any{
			"event": "tool_deadline_exceeded", "tool": "run_tests", "candidate": idx,
		})
	case !errors.Is(runErr, toolenvelope.ErrToolDisallowed):
		e.audit(ctx, j.ID, map[string]any{
			"event": "tests_run_failed", "candidate": idx, "error": runErr.Error(),
		})
	}
	e.audit(ctx, j.ID, map[string]any{
		"event": "substate_exited", "phase": string(llm.PhaseEvaluating),
		"substate": "running_tests", "candidate": idx, "exit_code": exitCode,
	})

	// F4-T3: the linter runs only when the project declares one. An empty
	// command asks the internal API for the closed-set default. A
	// disallowed `lint` tool (not in the envelope) leaves the verifier
	// unapplied.
	if len(p.Execution.Linters) > 0 {
		lres, lerr := e.runner.RunLint(tctx, j.ID, toolenvelope.ExecuteRequest{})
		switch {
		case lerr == nil:
			in.LintRan = true
			in.LintPassed = lres.ExitCode == 0
		case errors.Is(lerr, context.DeadlineExceeded):
			e.audit(ctx, j.ID, map[string]any{
				"event": "tool_deadline_exceeded", "tool": "lint", "candidate": idx,
			})
		case !errors.Is(lerr, toolenvelope.ErrToolDisallowed):
			e.audit(ctx, j.ID, map[string]any{
				"event": "lint_run_failed", "candidate": idx, "error": lerr.Error(),
			})
		}
	}
	return e.verifierRegistry().Run(in), in, nil
}

// toolContext bounds a pod tool call (execute_code / run_tests / lint). The
// cap is the smaller of 5 minutes and the evaluating phase's wall-time budget:
// no legitimate single-module test takes longer, and a hanging command must be
// cut quickly rather than eating the whole phase budget. The per-phase budget
// was previously applied only to LLM calls, so a hanging command could stall a
// job indefinitely. The returned cancel must be deferred by the caller.
func (e *Engine) toolContext(parent context.Context) (context.Context, context.CancelFunc) {
	budget := 5 * time.Minute
	if e.cfg != nil {
		if b, ok := e.cfg.Execution.PhaseBudget(string(llm.PhaseEvaluating)); ok && b > 0 && b < budget {
			budget = b
		}
	}
	return context.WithTimeout(parent, budget)
}

// auditVerification records one deterministic verification result. `where`
// names the call site ("candidate", "final") so a post-mortem can tell a
// per-candidate check from the acceptance check.
func (e *Engine) auditVerification(ctx context.Context, jobID, where string, r verify.Result) {
	e.audit(ctx, jobID, map[string]any{
		"event":     "verification",
		"where":     where,
		"applied":   r.Applied,
		"passed":    r.Passed,
		"score":     r.Score,
		"verifiers": r.Verifiers,
		"reasons":   r.Reasons,
	})
}
