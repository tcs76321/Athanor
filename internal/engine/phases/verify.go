package phases

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
func (d *Deps) verifyCandidate(ctx context.Context, j job.Job, p project.Project, t project.Task,
	content string, idx int) (verify.Result, verify.Input, error) {

	capability := project.CapabilityFor(p, t)
	in := verify.Input{
		Capability: capability,
		Archetype:  capability,
		Content:    content,
		Criteria:   t.Criteria,
		DataSchema: p.Execution.DataSchema,
		Checks:     p.Execution.Checks,
	}
	// F5 (ADR-0058): the code acceptance gates. An explicit false restores
	// the pre-F5 behavior; the config resolver applies the true default.
	if d.Cfg != nil {
		in.RequireTests = d.Cfg.Execution.RequireTestsForCodeValue()
		in.RequireDocs = d.Cfg.Execution.RequireDocumentationForCodeValue()
	}
	if capability != project.CapabilityCode || d.runner() == nil {
		return d.verifierRegistry().Run(in), in, nil
	}
	testCommand := p.TestCommand()
	if testCommand == "" {
		// ADR-0093: no repository runner is a visible degradation, not a
		// guessed pytest command and not a hard code gate.
		in.RequireTests = false
		d.audit(ctx, j.ID, map[string]any{
			"event": "tests_skipped", "candidate": idx, "reason": "no test command detected or configured",
		})
		return d.verifierRegistry().Run(in), in, nil
	}

	// Bug fix (probe soak): bound every pod tool call by the evaluating
	// phase's wall-time budget. The per-phase budget was applied only to LLM
	// calls (phases.go), so a hanging command — an infinite loop, or a test
	// blocked on stdin — stalled the job forever. A tool deadline soft-fails
	// the candidate (TestsPassed=false) rather than failing the job, so the
	// loop keeps moving.
	tctx, cancel := d.toolContext(ctx)
	defer cancel()

	// M2-T4b.5 (ADR-0024 §2): make the job's Job Pod exist before the pod
	// sub-steps, so the per-job token the runner presents is available.
	if err := d.ensurePod(tctx, j); err != nil && errors.Is(err, ErrRepositorySnapshot) {
		return verify.Result{}, in, fmt.Errorf("ensuring job pod: %w", err)
	}
	staged, err := d.runCodeInPod(tctx, j, p, t, content)
	if err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			return verify.Result{}, in, err
		}
		d.audit(ctx, j.ID, map[string]any{
			"event": "tool_deadline_exceeded", "tool": "execute_code", "candidate": idx,
		})
	}
	if !staged {
		// The candidate never reached /tmp/work. Running the test command now
		// would test the pristine repository (or the previous candidate's
		// leftovers), which is worse than no signal. Leave TestRan false so
		// require_tests_for_code rejects it deterministically when enabled.
		return d.verifierRegistry().Run(in), in, nil
	}

	in.TestRan = true
	in.TestCommand = testCommand
	// §8.1 "tracked sub-state": running_tests is an event, not a column.
	d.audit(ctx, j.ID, map[string]any{
		"event": "substate_entered", "phase": string(llm.PhaseEvaluating),
		"substate": "running_tests", "candidate": idx,
	})
	res, runErr := d.runner().RunTests(tctx, j.ID, toolenvelope.ExecuteRequest{Command: testCommand})
	exitCode := 0
	switch {
	case runErr == nil:
		exitCode = res.ExitCode
		in.TestsPassed = res.ExitCode == 0
		// M9-T1: keep the real failure output for the repair loop. The judge
		// never sees it (it reads ver.Reasons, which stays short).
		in.TestOutput = boundedTestOutput(res.Stdout, res.Stderr)
	case errors.Is(runErr, context.DeadlineExceeded):
		// The test command hung; reject this candidate, keep the job alive.
		d.audit(ctx, j.ID, map[string]any{
			"event": "tool_deadline_exceeded", "tool": "run_tests", "candidate": idx,
		})
	case !errors.Is(runErr, toolenvelope.ErrToolDisallowed):
		d.audit(ctx, j.ID, map[string]any{
			"event": "tests_run_failed", "candidate": idx, "error": runErr.Error(),
		})
	}
	d.audit(ctx, j.ID, map[string]any{
		"event": "substate_exited", "phase": string(llm.PhaseEvaluating),
		"substate": "running_tests", "candidate": idx, "exit_code": exitCode,
	})

	// F4-T3: the linter runs only when the project declares one. An empty
	// command asks the internal API for the closed-set default. A
	// disallowed `lint` tool (not in the envelope) leaves the verifier
	// unapplied.
	if len(p.Execution.Linters) > 0 {
		lres, lerr := d.runner().RunLint(tctx, j.ID, toolenvelope.ExecuteRequest{})
		switch {
		case lerr == nil:
			in.LintRan = true
			in.LintPassed = lres.ExitCode == 0
		case errors.Is(lerr, context.DeadlineExceeded):
			d.audit(ctx, j.ID, map[string]any{
				"event": "tool_deadline_exceeded", "tool": "lint", "candidate": idx,
			})
		case !errors.Is(lerr, toolenvelope.ErrToolDisallowed):
			d.audit(ctx, j.ID, map[string]any{
				"event": "lint_run_failed", "candidate": idx, "error": lerr.Error(),
			})
		}
	}
	return d.verifierRegistry().Run(in), in, nil
}

// toolContext bounds a pod tool call (execute_code / run_tests / lint). The
// cap is the smaller of 5 minutes and the evaluating phase's wall-time budget:
// no legitimate single-module test takes longer, and a hanging command must be
// cut quickly rather than eating the whole phase budget. The per-phase budget
// was previously applied only to LLM calls, so a hanging command could stall a
// job indefinitely. The returned cancel must be deferred by the caller.
func (d *Deps) toolContext(parent context.Context) (context.Context, context.CancelFunc) {
	budget := 5 * time.Minute
	if d.Cfg != nil {
		if b, ok := d.Cfg.Execution.PhaseBudget(string(llm.PhaseEvaluating)); ok && b > 0 && b < budget {
			budget = b
		}
	}
	return context.WithTimeout(parent, budget)
}

// maxTestOutputChars bounds the test output kept for the repair loop. The tail
// is kept because a traceback ends with the actual error (M9-T1).
const maxTestOutputChars = 2000

// boundedTestOutput returns the tail of a failing run's stderr (or stdout when
// stderr is empty), trimmed and bounded. It drives the repair loop and the
// reflection prompt; it is never shown to the judge.
func boundedTestOutput(stdout, stderr string) string {
	s := strings.TrimSpace(stderr)
	if s == "" {
		s = strings.TrimSpace(stdout)
	}
	if len(s) > maxTestOutputChars {
		s = "…" + s[len(s)-maxTestOutputChars:]
	}
	return s
}
