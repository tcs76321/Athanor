package engine

import (
	"context"
	"log/slog"
	"strings"

	"github.com/tcs76321/athanor/internal/corrections"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/regulator"
	"github.com/tcs76321/athanor/internal/verify"
)

// captureEvaluationCorrection turns a rejected candidate into the §18.1
// CorrectionRecord it implies. Test failures are `test_failure`, security
// findings are `security_scan`, and every other rejection is
// `evaluator_failure`. The source table in internal/corrections supplies the
// category/severity/scope/rule defaults; this function supplies the concrete
// evidence.
func (e *Engine) captureEvaluationCorrection(ctx context.Context, projectID, jobID, artifactID string,
	rec evaluation.Record, in verify.Input) {
	if e.corrections == nil {
		return
	}
	if rec.PassedTests && len(rec.MissingCriteria) == 0 && len(rec.SecurityIssues) == 0 {
		return
	}
	source := corrections.SourceEvaluatorFailure
	detail := "candidate rejected by evaluation: " + rec.Summary
	switch {
	case len(rec.SecurityIssues) > 0:
		source = corrections.SourceSecurityScan
		detail = "security issues: " + strings.Join(rec.SecurityIssues, "; ")
	case in.TestRan && !in.TestsPassed:
		source = corrections.SourceTestFailure
		detail = "test command failed: " + in.TestCommand
		if out := strings.TrimSpace(in.TestOutput); out != "" {
			detail += ": " + out
		}
	}
	// Preserve the evaluator's missing-criteria evidence even when the primary
	// source is a test/security failure; it is the most useful repair hint.
	if len(rec.MissingCriteria) > 0 {
		detail += " (missing criteria: " + strings.Join(rec.MissingCriteria, "; ") + ")"
	}
	if len(detail) > 4000 {
		detail = detail[:4000] + "…"
	}
	if _, err := e.corrections.Capture(ctx, corrections.CaptureInput{
		Source: source, ProjectID: projectID, JobID: jobID, ArtifactID: artifactID,
		Detail: detail,
	}); err != nil {
		slog.Error("engine: recording evaluation correction", "job", jobID, "artifact", artifactID, "err", err)
	}
}

// captureRegulatorCorrection records the feedback implied by a regulator
// decision: a repeated failure signature is a loop, and a budget stop is a
// budget exhaustion. Other decisions have no §18.1 source.
func (e *Engine) captureRegulatorCorrection(ctx context.Context, projectID, jobID string, r regulator.Result) {
	if e.corrections == nil {
		return
	}
	var source corrections.Source
	switch {
	case r.Decision == regulator.Defer:
		source = corrections.SourceBudgetExhaustion
	case r.Decision == regulator.Abandon && strings.Contains(r.Reason, "budget"):
		source = corrections.SourceBudgetExhaustion
	case r.Decision == regulator.Reframe && strings.Contains(r.Reason, "same failure repeated"):
		source = corrections.SourceLoopDetection
	default:
		return
	}
	if _, err := e.corrections.Capture(ctx, corrections.CaptureInput{
		Source: source, ProjectID: projectID, JobID: jobID,
		Detail: "regulator decision " + string(r.Decision) + ": " + r.Reason,
	}); err != nil {
		slog.Error("engine: recording regulator correction", "job", jobID, "err", err)
	}
}

// captureComparisonCorrection records a final artifact the comparison rejected.
// It complements the per-candidate captures: the synthesized final is its own
// evaluated artifact and may fail even when a proposal passed.
func (e *Engine) captureComparisonCorrection(ctx context.Context, projectID, jobID, artifactID string,
	verdict comparisonVerdict) {
	if e.corrections == nil || verdict.Winner == "new" {
		return
	}
	detail := "final artifact rejected by comparison: winner=" + verdict.Winner
	if len(verdict.Reasons) > 0 {
		detail += ": " + strings.Join(verdict.Reasons, "; ")
	}
	if len(verdict.MissingRequirements) > 0 {
		detail += " (missing: " + strings.Join(verdict.MissingRequirements, "; ") + ")"
	}
	if len(detail) > 4000 {
		detail = detail[:4000] + "…"
	}
	if _, err := e.corrections.Capture(ctx, corrections.CaptureInput{
		Source: corrections.SourceEvaluatorFailure, ProjectID: projectID, JobID: jobID,
		ArtifactID: artifactID, Detail: detail,
	}); err != nil {
		slog.Error("engine: recording comparison correction", "job", jobID, "artifact", artifactID, "err", err)
	}
}
