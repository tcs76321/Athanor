package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
)

// phaseDivergeN (§13.1 Phase 2): generates N candidate artifacts, each
// persisted as a draft `proposal` artifact (§9.1). The number of
// candidates is `cfg.Execution.DivergenceCandidates`, defaulting to 3
// per the M3-T1 simplification that "difficulty_hint" from the planner
// is not yet consumed; M3-T2 may feed it in.
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
	n := e.cfg.Execution.DivergenceCandidates
	if n < 1 {
		n = 1
	}
	if max := e.cfg.Execution.MaxHardTaskVariations; max > 0 && n > max {
		n = max
	}

	e.audit(ctx, j.ID, map[string]any{
		"event":      "divergence_start",
		"candidates": n,
		"archetype":  p.Archetype,
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

	// M3-T7-a: keep each candidate's text in memory so we can
	// compute pairwise Jaccard distance after the loop. The
	// set is bounded by `n` (default 3, hard-capped by
	// MaxHardTaskVariations) so the memory cost is trivial.
	candidateTexts := make([]string, 0, n)

	for i := 0; i < n; i++ {
		// The instruction appends a per-candidate seed so consecutive
		// calls with the same prompt still produce different outputs
		// at the same temperature. Without the seed the LLM has
		// nothing to anchor the variation; with the seed the
		// evaluator can tell candidates apart by their index.
		seed := fmt.Sprintf("CANDIDATE %d of %d. Produce a solution that differs from any other candidate you might generate for this task.", i+1, n)
		if research != "" {
			seed += "\n\n" + research
		}
		resp, err := e.call(ctx, j, p, t, llm.PhaseDiverging, llm.RoleMain, seed)
		if err != nil {
			return fmt.Errorf("divergence candidate %d/%d: %w", i+1, n, err)
		}
		if _, err := e.artifacts.CreateDraftFor(ctx, p.ID, t.ID, j.ID,
			artifact.KindProposal, []byte(resp.Content)); err != nil {
			return fmt.Errorf("persisting divergence candidate %d: %w", i+1, err)
		}
		candidateTexts = append(candidateTexts, resp.Content)
		e.audit(ctx, j.ID, map[string]any{
			"event": "divergence_candidate",
			"index": i + 1,
			"of":    n,
			"chars": len(resp.Content),
		})
	}

	// M3-T7-a: emit the average pairwise Jaccard distance
	// across the candidate set as a `divergence_jaccard`
	// event. The metric is the empirical case for the
	// "is divergence doing useful work" question; a low
	// Jaccard (<0.3) on >20% of tasks is the trigger for
	// the re-roll policy (ROADMAP §7, M3-T7-a).
	avgJaccard := averagePairwiseJaccard(candidateTexts)
	e.audit(ctx, j.ID, map[string]any{
		"event":        "divergence_jaccard",
		"candidates":   n,
		"avg_jaccard":  avgJaccard,
		"archetype":    p.Archetype,
	})

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
