package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/prompt"
)

// selfRefine is the M8-T11 self-refine operation (ADR-0064; the catalog in
// docs/cognitive-operations.md). When an evaluation cycle yields no passing
// candidate, it takes the best-scoring candidate, asks the LLM to critique and
// rewrite *that* candidate (the §11.2 refinement prompt under the refining
// phase), persists the rewrite as a new candidate, and re-verifies it through
// the ordinary deterministic verifier — the operation's verification edge.
//
// It is opt-in (execution.self_refine, default false) and bounded by
// execution.max_self_refine_loops. Unlike reflection (which proposes a change
// and re-diverges the whole set), self-refine repairs one candidate in place
// and re-evaluates only the repair. Returns (passed, err): passed is true when
// the repair passed its verifier.
func (e *Engine) selfRefine(ctx context.Context, j job.Job, p project.Project, t project.Task, previousID string) (bool, error) {
	if e.cfg == nil || !e.cfg.Execution.SelfRefineEnabled() {
		return false, nil
	}
	max := e.cfg.Execution.MaxSelfRefineLoopsValue()
	iter := e.getRefineCounter(ctx, j.ID)
	if max <= 0 || iter >= max {
		e.audit(ctx, j.ID, map[string]any{
			"event": "self_refine_budget_exhausted", "iter": iter, "max": max,
		})
		return false, nil
	}

	cand, rec, ok := e.bestCandidate(ctx, j)
	if !ok {
		return false, nil
	}
	content, err := e.artifacts.ReadContent(ctx, cand.ID)
	if err != nil {
		return false, fmt.Errorf("reading candidate for self-refine: %w", err)
	}

	instructions := buildSelfRefineInstructions(rec, p.Archetype)
	plan := e.planFor(ctx, j, p, t)
	role := e.roleFor(plan, llm.PhaseRefining, llm.RoleMain)
	ctxCandidates := []prompt.CandidateArtifact{{Kind: "candidate", Content: string(content)}}
	resp, err := e.call(ctx, j, p, t, llm.PhaseRefining, role, instructions, ctxCandidates)
	if err != nil {
		return false, err
	}
	newContent := resp.Content
	if p.Archetype == project.ArchetypeCode {
		newContent = normalizeCode(newContent)
	}
	art, err := e.artifacts.CreateDraftFor(ctx, p.ID, t.ID, j.ID,
		artifact.KindProposal, []byte(newContent))
	if err != nil {
		return false, fmt.Errorf("persisting self-refine artifact: %w", err)
	}
	if serr := e.setRefineCounter(ctx, j.ID, iter+1); serr != nil {
		slog.Warn("engine: bumping refine counter", "err", serr)
	}

	// The verification edge: re-run the deterministic verifiers on the repair.
	newRec, err := e.evaluateCandidate(ctx, j, p, t, art, previousID, 0, 0)
	if err != nil {
		return false, fmt.Errorf("evaluating self-refine artifact: %w", err)
	}
	passed := newRec.PassedTests && len(newRec.MissingCriteria) == 0 && len(newRec.SecurityIssues) == 0
	e.audit(ctx, j.ID, map[string]any{
		"event": "self_refine", "iter": iter + 1, "source_artifact": cand.ID,
		"artifact_id": art.ID, "passed": passed, "score": newRec.Score,
	})
	return passed, nil
}

// bestCandidate returns the job's highest-scoring evaluated proposal. Among
// multiple records for one artifact it takes the highest score. ok is false
// when there is nothing evaluated to repair.
func (e *Engine) bestCandidate(ctx context.Context, j job.Job) (artifact.Artifact, evaluation.Record, bool) {
	recs, err := e.eval.ListByJob(ctx, j.ID)
	if err != nil || len(recs) == 0 {
		return artifact.Artifact{}, evaluation.Record{}, false
	}
	bestByArtifact := map[string]evaluation.Record{}
	for _, r := range recs {
		if cur, ok := bestByArtifact[r.ArtifactID]; !ok || r.Score > cur.Score {
			bestByArtifact[r.ArtifactID] = r
		}
	}
	cands, err := e.listCandidateArtifacts(ctx, j)
	if err != nil {
		return artifact.Artifact{}, evaluation.Record{}, false
	}
	var best artifact.Artifact
	var bestRec evaluation.Record
	found := false
	for _, c := range cands {
		r, ok := bestByArtifact[c.ID]
		if !ok {
			continue
		}
		if !found || r.Score > bestRec.Score {
			best, bestRec, found = c, r, true
		}
	}
	return best, bestRec, found
}

// buildSelfRefineInstructions composes the targeted-repair prompt: the
// evaluator's recorded failures plus an instruction to change only what is
// necessary and return the full artifact.
func buildSelfRefineInstructions(rec evaluation.Record, archetype string) string {
	var b strings.Builder
	b.WriteString("SELF-REFINE. The candidate above failed acceptance. Rewrite it to fix exactly these failures:\n")
	if len(rec.MissingCriteria) > 0 {
		fmt.Fprintf(&b, "  missing criteria: %v\n", rec.MissingCriteria)
	}
	if len(rec.FailedTests) > 0 {
		fmt.Fprintf(&b, "  failed tests: %v\n", rec.FailedTests)
	}
	if len(rec.SecurityIssues) > 0 {
		fmt.Fprintf(&b, "  security issues: %v\n", rec.SecurityIssues)
	}
	if rec.Summary != "" {
		fmt.Fprintf(&b, "  evaluator summary: %q\n", rec.Summary)
	}
	b.WriteString("\nKeep what already works; change only what is necessary. Output the full corrected artifact.")
	if archetype == project.ArchetypeCode {
		b.WriteString(" " + codeOnlyInstruction)
	}
	return b.String()
}
