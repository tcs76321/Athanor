package phases

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tcs76321/athanor/internal/analysis"
	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/engine/acceptance"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/notebook"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/prompt"
)

// finalKindFor maps a task capability to the artifact kind its final output
// takes (§9.1).
func finalKindFor(capability string) artifact.Kind {
	switch capability {
	case project.CapabilityCode:
		return artifact.KindCode
	case project.CapabilityData:
		return artifact.KindDataset
	case project.CapabilityMedia:
		return artifact.KindMedia
	default: // text, document
		return artifact.KindDocument
	}
}

// bestPassingCandidate selects the job's evaluated candidate to synthesize
// from: the highest-scoring candidate that passed deterministic verification
// (real tests, no missing criteria, no security issues). When no candidate
// passed — or the evaluation repo is not wired — it falls back to the latest
// proposal and reports ok=false. This prevents synthesis from refining an
// arbitrary (possibly failing) proposal and discarding a candidate that
// already passed the real tests (M8-T26b).
func (d *Deps) bestPassingCandidate(ctx context.Context, j job.Job) (artifact.Artifact, bool, error) {
	candidates, err := d.listCandidateArtifacts(ctx, j)
	if err != nil {
		return artifact.Artifact{}, false, fmt.Errorf("listing candidates: %w", err)
	}
	if len(candidates) == 0 {
		return artifact.Artifact{}, false, fmt.Errorf("no divergence candidates for job %s", j.ID)
	}
	if d.Eval == nil {
		return candidates[len(candidates)-1], false, nil
	}
	records, err := d.Eval.ListByJob(ctx, j.ID)
	if err != nil {
		return artifact.Artifact{}, false, fmt.Errorf("listing evaluation records: %w", err)
	}
	passing := map[string]bool{}
	score := map[string]float64{}
	for _, r := range records {
		if r.PassedTests && len(r.MissingCriteria) == 0 && len(r.SecurityIssues) == 0 {
			passing[r.ArtifactID] = true
		}
		if cur, seen := score[r.ArtifactID]; !seen || r.Score > cur {
			score[r.ArtifactID] = r.Score
		}
	}
	var chosen *artifact.Artifact
	var chosenScore float64
	for i := range candidates {
		if !passing[candidates[i].ID] {
			continue
		}
		if chosen == nil || score[candidates[i].ID] > chosenScore {
			c := candidates[i]
			chosen = &c
			chosenScore = score[candidates[i].ID]
		}
	}
	if chosen != nil {
		return *chosen, true, nil
	}
	// No candidate passed. Start synthesis from the BEST-scoring candidate,
	// not the last one: with multiple candidates the last is arbitrary, and
	// letting it seed synthesis can make the full loop deliver a worse
	// artifact than a one-shot arm (ADR-0117).
	var best *artifact.Artifact
	bestScore := 0.0
	for i := range candidates {
		candidateScore, ok := score[candidates[i].ID]
		if !ok {
			continue
		}
		if best == nil || candidateScore > bestScore {
			c := candidates[i]
			best = &c
			bestScore = candidateScore
		}
	}
	if best != nil {
		return *best, false, nil
	}
	return candidates[len(candidates)-1], false, nil
}

// phaseSynthesize (§13.1 Phase 5): main persona refines the persisted
// proposal into the final draft artifact. Re-runs after a crash version
// the existing artifact instead of piling up drafts.
//
// M3-T2 (ADR-0014): the M2-T4 sub-steps (`runCodeInPod` and
// `runTestsInPod`) used to live here. They now live in
// `phaseEvaluate.evaluateCandidate` (per candidate, before the
// security-persona verdict) so the pod's exit code is part of the
// evidence the LLM judge sees. This function is archetype-agnostic;
// the per-archetype artifact kind is the only divergence
// (`finalKindFor` below).
func (d *Deps) phaseSynthesize(ctx context.Context, j job.Job) error {
	p, t, err := d.contexts(ctx, j)
	if err != nil {
		return err
	}
	candidate, passed, err := d.bestPassingCandidate(ctx, j)
	if err != nil {
		return err
	}
	candidateContent, err := d.Artifacts.ReadContent(ctx, candidate.ID)
	if err != nil {
		return fmt.Errorf("reading divergence candidate: %w", err)
	}

	// M5-T5: the proposal bytes ride §11.2 §12 (tier 3, never evicted)
	// instead of being concatenated into the §13 instructions.
	plan := d.planFor(ctx, j, p, t)
	capability := project.CapabilityFor(p, t)
	role := d.roleFor(plan, llm.PhaseSynthesizing, llm.RoleMain)
	instructions := "Refine the divergence proposal below into the final artifact for this task."
	if capability == project.CapabilityCode {
		instructions += " " + codeInstruction(d.languageFor(ctx, j, p, t))
	}
	resp, err := d.call(ctx, j, p, t, llm.PhaseSynthesizing, role, instructions,
		[]prompt.CandidateArtifact{{Kind: "proposal", Content: string(candidateContent)}})
	if err != nil {
		return err
	}

	// F4 follow-up (A1): the synthesized final must also be raw source for a
	// code archetype, so the accepted artifact and its git commit are clean.
	content := resp.Content
	if capability == project.ArchetypeCode {
		content = normalizeCode(content)
	}
	// M8-T26b / P0: never discard a verified candidate. If the rewrite fails
	// the deterministic verifier while the source candidate passed it, keep
	// the candidate's bytes as the final artifact. This applies to every
	// archetype with a hard verifier (code tests, declared text/document
	// checks, data schema), not only code: the v2 trace showed a text job
	// whose passing candidate was rewritten into a final that failed a
	// declared check, ending `failed` while the external check passed.
	if ver, _, verr := d.verifyCandidate(ctx, j, p, t, content, 0); verr == nil {
		d.auditVerification(ctx, j.ID, "final", ver)
		if ver.HardDecisive() && !ver.HardPassed {
			fallback := passed
			reason := "source candidate passed the verifier"
			if !fallback {
				if candVer, _, cerr := d.verifyCandidate(ctx, j, p, t, string(candidateContent), 0); cerr == nil {
					fallback = acceptance.ShouldFallbackToCandidate(false, ver, candVer)
					reason = "source candidate scored better than the synthesis"
				}
			}
			if fallback {
				content = string(candidateContent)
				d.audit(ctx, j.ID, map[string]any{
					"event": "synthesis_fallback_to_candidate", "candidate_id": candidate.ID,
					"archetype": capability, "reason": reason,
				})
			}
		}
	}

	d.note(ctx, j.ID, p.ID, notebook.KindDecision, "synthesizing",
		fmt.Sprintf("final %s artifact synthesized (%d bytes)", capability, len(content)))
	kind := finalKindFor(capability)
	if prev, err := d.Artifacts.LatestForJob(ctx, j.ID, kind); err == nil {
		if _, err := d.Artifacts.NewVersion(ctx, prev.ID, []byte(content)); err != nil {
			return fmt.Errorf("versioning final artifact: %w", err)
		}
	} else {
		if _, err := d.Artifacts.CreateDraftFor(ctx, p.ID, t.ID, j.ID, kind, []byte(content)); err != nil {
			return fmt.Errorf("persisting final artifact: %w", err)
		}
	}

	_, err = d.Jobs.Transition(ctx, j.ID, job.StateComparing)
	return err
}

// phasePlan (§13.1 Phase 1): tall persona, low temperature. The plan
// guides divergence; in M1 it is advisory context, not persisted. F4-T2
// adds one thing: the planner is asked to end with a `DIFFICULTY:` hint,
// which is parsed and persisted so the compute policy can scale to the
// task (an unparseable or absent hint leaves the policy on its other
// signals).
//
// The request rides the task description (a pinned, tier-2 §11.2 section)
// rather than the phase's tier-7 instructions, so the planning call stays
// pinned-only — the property the §10.4 critical-pressure tests key on.
func (d *Deps) phasePlan(ctx context.Context, j job.Job) error {
	p, t, err := d.contexts(ctx, j)
	if err != nil {
		return err
	}
	plan := d.planFor(ctx, j, p, t)
	role := d.roleFor(plan, llm.PhasePlanning, llm.RoleTall)
	planTask := t
	planTask.Description = strings.TrimRight(t.Description, "\n") + "\n\n" + difficultyRequest + "\n\n" + analysisRequest + "\n\n" + languageRequest
	resp, err := d.call(ctx, j, p, planTask, llm.PhasePlanning, role, "", nil)
	if err != nil {
		return err
	}
	if d.askForClarification(ctx, j.ID, p.ID, "planning", resp.Content) {
		return ErrPaused
	}
	d.note(ctx, j.ID, p.ID, notebook.KindPlan, "planning", resp.Content)
	if hint := parseDifficultyHint(resp.Content); hint != "" {
		if err := d.State.SetDifficulty(ctx, j.ID, hint); err != nil {
			slog.Warn("engine: persisting difficulty hint", "job", j.ID, "err", err)
		}
		d.audit(ctx, j.ID, map[string]any{"event": "difficulty_hint", "difficulty": hint})
	}
	if l := parseLanguageHint(resp.Content); l != "" {
		if err := d.State.SetLanguage(ctx, j.ID, l); err != nil {
			slog.Warn("engine: persisting language hint", "job", j.ID, "err", err)
		}
		d.audit(ctx, j.ID, map[string]any{"event": "language_hint", "language": l})
	}
	a := analysis.Parse(resp.Content)
	if err := d.State.SetAnalysis(ctx, j.ID, a); err != nil {
		slog.Error("engine: storing task analysis", "job", j.ID, "err", err)
	}
	d.audit(ctx, j.ID, map[string]any{
		"event": "task_analysis", "parsed": a.Parsed,
		"unit_count": len(a.Units), "deliberate": len(a.Deliberate()),
		"units_detail": a.Units,
	})
	_, err = d.Jobs.Transition(ctx, j.ID, job.StateDiverging)
	return err
}

// difficultyRequest is the F4-T2 planning instruction. It is a constant so
// the parser and the prompt cannot drift.
const difficultyRequest = "End your plan with a final line exactly of the form `DIFFICULTY: easy` or `DIFFICULTY: hard`, judging whether this task is routine or genuinely hard. Keep it to one line."

// analysisRequest is the ADR-0090 planning instruction. It is a constant so
// the parser and the prompt cannot drift. The model is asked for a bounded
// decomposition; a malformed or absent block degrades to one direct unit.
const analysisRequest = "Then emit exactly one fenced ```analysis block containing JSON of the form " +
	`{"units":[{"goal":"...","mode":"direct|deliberate","reason":"...","falsifier":"..."}]}. ` +
	"Use at most five units. Default every unit to \"direct\"; use \"deliberate\" only for a part " +
	"you expect to be wrong or that has hidden constraints. The falsifier is the cheapest check " +
	"that would reveal that unit is wrong. No prose inside the block."

// parseDifficultyHint extracts the F4-T2 `DIFFICULTY: easy|hard` marker from
// the planning output. It scans lines case-insensitively and returns the
// last valid marker (a model that revises its estimate should win). An
// absent or unrecognized marker returns "" — the policy then falls back to
// history and criteria rather than guessing.
func parseDifficultyHint(content string) string {
	hint := ""
	for _, line := range strings.Split(content, "\n") {
		l := strings.ToUpper(strings.TrimSpace(line))
		const prefix = "DIFFICULTY:"
		if !strings.HasPrefix(l, prefix) {
			continue
		}
		v := strings.TrimSpace(l[len(prefix):])
		switch {
		case strings.HasPrefix(v, "EASY"):
			hint = "easy"
		case strings.HasPrefix(v, "HARD"):
			hint = "hard"
		}
	}
	return hint
}
