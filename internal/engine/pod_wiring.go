package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// M2-T4 sub-step methods for the code archetype. These run
// inside the existing `synthesizing` phase, after the LLM has
// produced the proposal and the engine has persisted the final
// artifact. They are gated on `code` archetype: text, document,
// data, and media skip them and the M1 walking skeleton takes
// over (compare → complete).
//
// runCodeInPod dispatches the LLM-generated code to the Job
// Pod's execute_code route. Steps:
//  1. Load the latest divergence proposal (the code the LLM
//     wrote). This is what gets executed.
//  2. Call e.runner.RunCode with language=python and the
//     proposal content. A nil runner short-circuits to a
//     recorded-but-skipped sub-step (M1 dev mode, unit tests).
//  3. Persist the result as a new code artifact (the §9.1
//     artifact kind table already includes `code`). The
//     artifact is the audit log of "what the pod did" — exit
//     code, stdout, stderr, duration.
//  4. Append an EventLog entry `code_executed` with the
//     duration and exit code.
func (e *Engine) runCodeInPod(ctx context.Context, j job.Job, p project.Project, t project.Task) error {
	if e.runner == nil {
		e.audit(ctx, j.ID, map[string]any{
			"event":     "code_executed",
			"skipped":   true,
			"reason":    "no ToolRunner wired (M1 dev mode)",
			"archetype": p.Archetype,
		})
		return nil
	}

	proposal, err := e.artifacts.LatestForJob(ctx, j.ID, artifact.KindProposal)
	if err != nil {
		return fmt.Errorf("loading divergence proposal for execution: %w", err)
	}
	code, err := e.artifacts.ReadContent(ctx, proposal.ID)
	if err != nil {
		return fmt.Errorf("reading proposal content: %w", err)
	}

	req := toolenvelope.ExecuteRequest{
		Language: "python",
		Code:     string(code),
	}
	start := time.Now()
	res, err := e.runner.RunCode(ctx, j.ID, req)
	if err != nil {
		if errors.Is(err, toolenvelope.ErrToolDisallowed) {
			e.audit(ctx, j.ID, map[string]any{
				"event":     "code_executed",
				"skipped":   true,
				"reason":    "execute_code not in job envelope",
				"archetype": p.Archetype,
			})
			return nil
		}
		e.audit(ctx, j.ID, map[string]any{
			"event":  "code_executed",
			"error":  err.Error(),
			"detail": "runner returned non-disallowed error",
		})
		return fmt.Errorf("executing code in pod: %w", err)
	}
	_ = start

	if _, err := e.artifacts.CreateDraftFor(ctx, p.ID, t.ID, j.ID, artifact.KindCode, jsonMarshalExecuteResult(res)); err != nil {
		return fmt.Errorf("persisting code execution artifact: %w", err)
	}
	e.audit(ctx, j.ID, map[string]any{
		"event":       "code_executed",
		"exit_code":   res.ExitCode,
		"duration_ms": res.DurationMS,
		"stdout_len":  len(res.Stdout),
		"stderr_len":  len(res.Stderr),
		"archetype":   p.Archetype,
	})
	slog.Debug("engine: code executed in pod", "job", j.ID, "exit_code", res.ExitCode, "duration_ms", res.DurationMS)
	return nil
}

// The sub-step is intentionally a *sub-state* (logged in the
// EventLog, not the jobs.state column) so we do not have to
// modify the §8.1 state machine and its tests. M3-T2 (ADR-0014)
// lives in `phaseEvaluate.evaluateCandidate` — the §13.1
// Phase 3 sequence is now: per-candidate code-exec + test-run
// + LLM verdict, all in `evaluating`.
//
// jsonMarshalExecuteResult serializes an ExecuteResult to JSON
// bytes by hand. The struct is four primitive fields; pulling
// in encoding/json for this one callsite would inflate the
// import graph. The encoding is unambiguous JSON; if the
// ExecuteResult shape gains a field, this function must be
// updated.
func jsonMarshalExecuteResult(r toolenvelope.ExecuteResult) []byte {
	return fmt.Appendf(nil,
		`{"exit_code":%d,"stdout":%s,"stderr":%s,"duration_ms":%d}`,
		r.ExitCode, jsonString(r.Stdout), jsonString(r.Stderr), r.DurationMS)
}

// jsonString returns a JSON string literal (including the
// surrounding quotes) for s. The implementation escapes the
// characters that JSON requires: backslash, double quote, and
// the C0 control bytes. Other characters are passed through
// verbatim — ExecuteResult's strings are user-controlled (the
// LLM's output and the pod's stdout/stderr) so a malformed
// JSON would surface in the artifact's binary content, not
// corrupt the daemon.
func jsonString(s string) string {
	var b []byte
	b = append(b, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\', '"':
			b = append(b, '\\', c)
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		case '\t':
			b = append(b, '\\', 't')
		default:
			if c < 0x20 {
				// C0 control byte: emit \u00XX.
				const hex = "0123456789abcdef"
				b = append(b, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xF])
			} else {
				b = append(b, c)
			}
		}
	}
	b = append(b, '"')
	return string(b)
}
