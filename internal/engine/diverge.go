package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/project"
)

// codeOnlyInstruction is the F4-T0a follow-through: the code-archetype
// artifact must be the raw module, because the Job Pod materializes it as
// `/tmp/solution.py` and imports it. The first post-F4 micro run showed the
// generator emitting prose-and-fences, which imports as invalid Python and
// fails the real test for a packaging reason rather than a logic one.
const codeOnlyInstruction = "Output ONLY the raw source file contents (Python). No markdown code fences, no prose, no commentary, no explanation."

// phaseDivergeN (§13.1 Phase 2): generates N candidate artifacts, each
// persisted as a draft `proposal` artifact (§9.1). The number of
// candidates is `cfg.Execution.DivergenceCandidates`, defaulting to 3.
// The planner's "difficulty_hint" is not yet consumed; wiring it to the
// candidate count is M6 work.
//
// All candidates use the `main` persona at the phase's high temperature
// (0.7–1.1) so they actually differ; the LLM is told to "explore
// orthogonal options" via the prompt. The transition is to
// `evaluating` (§8.2: divergence always feeds evaluation).
//
// Crash safety: every candidate is persisted before the transition.
// A crash mid-divergence resumes into `diverging` (per §23.6) and the
// candidates that already landed in the artifacts table are picked up
// by `phaseEvaluate` via `LatestForJob(KindProposal)` (the table is
// sorted newest-first; the highest version is the one the
// divergence run actually completed last, but every candidate
// between the resume point and the truncation is still evaluable).
func (e *Engine) phaseDivergeN(ctx context.Context, j job.Job) error {
	p, t, err := e.contexts(ctx, j)
	if err != nil {
		return err
	}
	if e.cfg == nil {
		return errors.New("engine: cfg is nil (diverge phase requires config)")
	}
	// F4-T1c/T2/T5: resolve the plan once here (after planning, so the
	// difficulty hint is visible), audit it, then use its candidate count,
	// routed personas, and diversity policy. A nil policy/config reproduces
	// pre-F4 behavior.
	plan := e.planFor(ctx, j, p, t)
	e.auditComputePlan(ctx, j.ID, "divergence", plan)
	n := plan.Candidates
	if max := e.cfg.Execution.MaxHardTaskVariations; max > 0 && n > max {
		n = max
	}
	roles := plan.DivergenceRoles
	if len(roles) == 0 {
		roles = []string{e.roleFor(plan, llm.PhaseDiverging, llm.RoleMain)}
	}

	e.audit(ctx, j.ID, map[string]any{
		"event":      "divergence_start",
		"candidates": n,
		"archetype":  p.Archetype,
		"roles":      roles,
	})

	// M4-T7 research sub-step (ADR-0019 §7): fetch the task's
	// declared source URLs through the gateway and inject the
	// extracted markdown into every candidate's prompt. Soft-fails
	// per source; an empty string means no sources (the sub-step is
	// a silent no-op for tasks without URLs).
	research, err := e.researchContext(ctx, j, p, t)
	if err != nil {
		return fmt.Errorf("research sub-step: %w", err)
	}

	// F4-T5: enforce a Jaccard floor with a bounded re-roll. A below-floor
	// batch is discarded (draft → candidate → rejected) so it is never
	// evaluated, and the next attempt runs with a fresh seed. Diversity is
	// undefined for a single candidate, so n < 2 never re-rolls.
	floor := e.cfg.Execution.JaccardFloorValue()
	maxRerolls := e.cfg.Execution.MaxDiversityRerollsValue()
	for attempt := 0; ; attempt++ {
		candidateTexts := make([]string, 0, n)
		batch := make([]artifact.Artifact, 0, n)
		for i := 0; i < n; i++ {
			// Cycle personas for heterogeneous diversity, and append a
			// per-candidate seed so consecutive same-prompt calls still
			// produce different outputs at the same temperature.
			candRole := roles[i%len(roles)]
			seed := fmt.Sprintf("CANDIDATE %d of %d. Produce a solution that differs from any other candidate you might generate for this task.", i+1, n)
			if p.Archetype == project.ArchetypeCode {
				seed += "\n" + codeOnlyInstruction
			}
			if research != "" {
				seed += "\n\n" + research
			}
			resp, err := e.call(ctx, j, p, t, llm.PhaseDiverging, candRole, seed, nil)
			if err != nil {
				return fmt.Errorf("divergence candidate %d/%d: %w", i+1, n, err)
			}
			// F4 follow-up (A1): persist raw code, not a fenced block, so
			// the stored artifact, the eval/compare prompt, and the eventual
			// git commit are all importable source.
			content := resp.Content
			if p.Archetype == project.ArchetypeCode {
				content = normalizeCode(content)
			}
			art, err := e.artifacts.CreateDraftFor(ctx, p.ID, t.ID, j.ID,
				artifact.KindProposal, []byte(content))
			if err != nil {
				return fmt.Errorf("persisting divergence candidate %d: %w", i+1, err)
			}
			batch = append(batch, art)
			candidateTexts = append(candidateTexts, content)
			e.audit(ctx, j.ID, map[string]any{
				"event": "divergence_candidate", "index": i + 1, "of": n,
				"chars": len(resp.Content), "persona": candRole,
			})
		}
		avgJaccard := averagePairwiseJaccard(candidateTexts)
		e.audit(ctx, j.ID, map[string]any{
			"event":       "divergence_jaccard",
			"candidates":  n,
			"avg_jaccard": avgJaccard,
			"floor":       floor,
			"attempt":     attempt,
			"archetype":   p.Archetype,
			"roles":       roles,
		})
		if n < 2 || avgJaccard >= floor || attempt >= maxRerolls {
			break
		}
		for _, art := range batch {
			_ = e.artifacts.SetStatus(ctx, art.ID, artifact.StatusCandidate)
			_ = e.artifacts.SetStatus(ctx, art.ID, artifact.StatusRejected)
		}
		e.audit(ctx, j.ID, map[string]any{
			"event": "divergence_reroll", "attempt": attempt,
			"avg_jaccard": avgJaccard, "floor": floor,
		})
	}

	_, err = e.jobs.Transition(ctx, j.ID, job.StateEvaluating)
	return err
}

// averagePairwiseJaccard returns the mean pairwise
// Jaccard *distance* (1 - Jaccard similarity) across
// all distinct pairs in `texts`. A distance of 1.0
// means the two sets are disjoint; 0.0 means
// identical. The function is order-insensitive
// (average over (i, j) with i < j).
//
// Tokenization is whitespace + lowercase, the
// simplest split that doesn't conflate "the" with
// "The". For the M3-T7-a measurement this is
// sufficient; a more sophisticated tokenizer is
// follow-up work.
func averagePairwiseJaccard(texts []string) float64 {
	if len(texts) < 2 {
		return 0
	}
	sets := make([]map[string]struct{}, len(texts))
	for i, t := range texts {
		sets[i] = tokenize(t)
	}
	var sum float64
	var pairs int
	for i := 0; i < len(texts); i++ {
		for j := i + 1; j < len(texts); j++ {
			sum += jaccardDistance(sets[i], sets[j])
			pairs++
		}
	}
	if pairs == 0 {
		return 0
	}
	return sum / float64(pairs)
}

// tokenize returns the set of whitespace-split,
// lowercased tokens in `s`. Empty strings and
// strings with no tokens return an empty (not nil)
// map so jaccardDistance handles them uniformly.
func tokenize(s string) map[string]struct{} {
	out := map[string]struct{}{}
	word := make([]rune, 0, 16)
	flush := func() {
		if len(word) == 0 {
			return
		}
		out[string(word)] = struct{}{}
		word = word[:0]
	}
	for _, r := range s {
		if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
			flush()
			continue
		}
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		word = append(word, r)
	}
	flush()
	return out
}

// jaccardDistance returns 1 - |A ∩ B| / |A ∪ B|.
// Two empty sets have distance 0 (they are
// identical under the Jaccard measure; a
// divide-by-zero on |A ∪ B| is avoided by the
// short-circuit).
func jaccardDistance(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if _, ok := b[k]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return 1.0 - float64(inter)/float64(union)
}
