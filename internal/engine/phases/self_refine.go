package phases

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/engine/repair"
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
// It is on by default (execution.self_refine, M9-T1) and bounded by
// execution.max_self_refine_loops. Unlike reflection (which proposes a change
// and re-diverges the whole set), self-refine repairs one candidate in place
// and re-evaluates only the repair. Returns (passed, err): passed is true when
// the repair passed its verifier.
func (d *Deps) selfRefine(ctx context.Context, j job.Job, p project.Project, t project.Task, previousID string) (bool, error) {
	if d.Cfg == nil || !d.Cfg.Execution.SelfRefineEnabled() {
		return false, nil
	}
	max := d.Cfg.Execution.MaxSelfRefineLoopsValue()
	iter := d.State.RefineCount(ctx, j.ID)
	if !(repair.Budget{Enabled: true, Max: max, Used: iter}).Allows() {
		d.audit(ctx, j.ID, map[string]any{
			"event": "self_refine_budget_exhausted", "iter": iter, "max": max,
		})
		return false, nil
	}

	recs, rerr := d.Eval.ListByJob(ctx, j.ID)
	if rerr != nil {
		return false, nil
	}
	cands, cerr := d.listCandidateArtifacts(ctx, j)
	if cerr != nil {
		return false, nil
	}
	cand, rec, ok := repair.BestCandidate(cands, recs)
	if !ok {
		return false, nil
	}
	content, err := d.Artifacts.ReadContent(ctx, cand.ID)
	if err != nil {
		return false, fmt.Errorf("reading candidate for self-refine: %w", err)
	}

	capability := project.CapabilityFor(p, t)
	plan := d.planFor(ctx, j, p, t)
	role := d.roleFor(plan, llm.PhaseRefining, llm.RoleMain)
	if d.Cfg.Execution.ObservationToolsEnabled() && capability == project.CapabilityCode {
		d.observeForRepair(ctx, j, p, t, role)
	}
	instructions := repair.Instructions(rec, capability, d.languageFor(ctx, j, p, t))
	ctxCandidates := []prompt.CandidateArtifact{{Kind: "candidate", Content: string(content)}}
	resp, err := d.call(ctx, j, p, t, llm.PhaseRefining, role, instructions, ctxCandidates)
	if err != nil {
		return false, err
	}
	newContent := resp.Content
	if capability == project.CapabilityCode {
		newContent = normalizeCode(newContent)
	}
	art, err := d.Artifacts.CreateDraftFor(ctx, p.ID, t.ID, j.ID,
		artifact.KindProposal, []byte(newContent))
	if err != nil {
		return false, fmt.Errorf("persisting self-refine artifact: %w", err)
	}
	if serr := d.State.SetRefineCount(ctx, j.ID, iter+1); serr != nil {
		slog.Warn("engine: bumping refine counter", "err", serr)
	}

	// The verification edge: re-run the deterministic verifiers on the repair.
	newRec, err := d.evaluateCandidate(ctx, j, p, t, art, previousID, 0, 0)
	if err != nil {
		return false, fmt.Errorf("evaluating self-refine artifact: %w", err)
	}
	passed := repair.Passed(newRec)
	d.audit(ctx, j.ID, map[string]any{
		"event": "self_refine", "iter": iter + 1, "source_artifact": cand.ID,
		"artifact_id": art.ID, "passed": passed, "score": newRec.Score,
	})
	return passed, nil
}
