package engine

import (
	"testing"

	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/prompt"
)

// TestJudgmentSeed covers M3-T7.1's derived temperature-0 seed. The seed
// must be a deterministic function of its inputs (so a crash-recovery
// re-entry samples the same point) and must vary with each input (so
// different jobs, phases, and candidates get independent samples).
func TestJudgmentSeed(t *testing.T) {
	cands := []prompt.CandidateArtifact{{Kind: "candidate", Content: "print('hi')"}}

	base := judgmentSeed("job-1", llm.PhaseEvaluating, cands)
	if base < 0 {
		t.Fatalf("judgmentSeed returned a negative value %d; the sign bit must be masked", base)
	}
	if again := judgmentSeed("job-1", llm.PhaseEvaluating, cands); again != base {
		t.Errorf("judgmentSeed is not deterministic: %d then %d", base, again)
	}

	cases := []struct {
		name string
		got  int64
	}{
		{"different job", judgmentSeed("job-2", llm.PhaseEvaluating, cands)},
		{"different phase", judgmentSeed("job-1", llm.PhaseComparing, cands)},
		{"different candidate", judgmentSeed("job-1", llm.PhaseEvaluating,
			[]prompt.CandidateArtifact{{Kind: "candidate", Content: "print('bye')"}})},
		{"no candidates", judgmentSeed("job-1", llm.PhaseEvaluating, nil)},
	}
	for _, c := range cases {
		if c.got == base {
			t.Errorf("%s collided with the baseline seed %d", c.name, base)
		}
	}
}

// TestPhaseProducesJSON pins the ADR-0012 wire-format decision: only the
// two security-persona judgment phases request JSON mode.
func TestPhaseProducesJSON(t *testing.T) {
	for _, p := range []string{llm.PhaseEvaluating, llm.PhaseComparing} {
		if !phaseProducesJSON(p) {
			t.Errorf("phaseProducesJSON(%q) = false, want true", p)
		}
	}
	for _, p := range []string{
		llm.PhasePlanning, llm.PhaseDiverging, llm.PhaseReflecting,
		llm.PhaseSynthesizing, "context_building",
	} {
		if phaseProducesJSON(p) {
			t.Errorf("phaseProducesJSON(%q) = true, want false", p)
		}
	}
}
