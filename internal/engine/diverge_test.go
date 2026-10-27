// M3-T7-a tests: the average pairwise Jaccard
// distance metric the engine emits as the
// `divergence_jaccard` event. The math is
// order-insensitive; the tests pin the boundary
// cases (identical, disjoint, single-candidate).
package engine

import (
	"strings"
	"testing"
)

func TestJaccardDistance_IdenticalSets(t *testing.T) {
	a := map[string]struct{}{"the": {}, "cat": {}, "sat": {}}
	b := map[string]struct{}{"the": {}, "cat": {}, "sat": {}}
	if got := jaccardDistance(a, b); got != 0 {
		t.Errorf("identical sets distance = %f, want 0", got)
	}
}

func TestJaccardDistance_DisjointSets(t *testing.T) {
	a := map[string]struct{}{"alpha": {}, "beta": {}}
	b := map[string]struct{}{"gamma": {}, "delta": {}}
	if got := jaccardDistance(a, b); got != 1 {
		t.Errorf("disjoint sets distance = %f, want 1", got)
	}
}

func TestJaccardDistance_EmptySets(t *testing.T) {
	if got := jaccardDistance(map[string]struct{}{}, map[string]struct{}{}); got != 0 {
		t.Errorf("empty sets distance = %f, want 0", got)
	}
}

func TestJaccardDistance_PartialOverlap(t *testing.T) {
	// a = {the, quick, brown}
	// b = {the, brown, fox}
	// intersection = 2 (the, brown)
	// union = 4 (the, quick, brown, fox)
	// distance = 1 - 2/4 = 0.5
	a := map[string]struct{}{"the": {}, "quick": {}, "brown": {}}
	b := map[string]struct{}{"the": {}, "brown": {}, "fox": {}}
	if got := jaccardDistance(a, b); got != 0.5 {
		t.Errorf("partial overlap distance = %f, want 0.5", got)
	}
}

func TestAveragePairwiseJaccard_SingleCandidate(t *testing.T) {
	// A single candidate has no pairs; the
	// function returns 0 (the convention for
	// "no measurement possible").
	got := averagePairwiseJaccard([]string{"only one"})
	if got != 0 {
		t.Errorf("single-candidate average = %f, want 0", got)
	}
}

func TestAveragePairwiseJaccard_AllIdentical(t *testing.T) {
	// Three identical candidates → 3 pairs, all
	// distance 0 → average 0.
	got := averagePairwiseJaccard([]string{
		"the cat sat on the mat",
		"the cat sat on the mat",
		"the cat sat on the mat",
	})
	if got != 0 {
		t.Errorf("all-identical average = %f, want 0", got)
	}
}

func TestAveragePairwiseJaccard_AllDisjoint(t *testing.T) {
	// Three disjoint candidates → 3 pairs, all
	// distance 1 → average 1.
	got := averagePairwiseJaccard([]string{
		"alpha beta",
		"gamma delta",
		"epsilon zeta",
	})
	if got != 1 {
		t.Errorf("all-disjoint average = %f, want 1", got)
	}
}

func TestAveragePairwiseJaccard_Mixed(t *testing.T) {
	// Three candidates: two similar, one different.
	// Pairs:
	//   (a, b): identical → 0
	//   (a, c): disjoint → 1
	//   (b, c): disjoint → 1
	// Average = (0 + 1 + 1) / 3 = 0.6667
	got := averagePairwiseJaccard([]string{
		"the quick brown fox",
		"the quick brown fox",
		"completely different text",
	})
	if got < 0.66 || got > 0.67 {
		t.Errorf("mixed average = %f, want ≈ 0.667", got)
	}
}

func TestTokenize_LowercasesAndSplits(t *testing.T) {
	got := tokenize("The Quick  Brown\nFox")
	want := map[string]struct{}{"the": {}, "quick": {}, "brown": {}, "fox": {}}
	if len(got) != len(want) {
		t.Errorf("tokenize len = %d, want %d", len(got), len(want))
	}
	for k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("tokenize missing %q", k)
		}
	}
}

func TestTokenize_Empty(t *testing.T) {
	got := tokenize("")
	if len(got) != 0 {
		t.Errorf("tokenize empty = %d tokens, want 0", len(got))
	}
}

// TestCodeOnlyInstructionPreservesExistingCode pins the regression rule added
// after a follow-up job rewrote a file and dropped a previously accepted
// function. The model must be told to preserve unrelated code and tests.
func TestCodeOnlyInstructionPreservesExistingCode(t *testing.T) {
	for _, want := range []string{
		"preserve every existing public function",
		"never drop unrelated code or tests",
	} {
		if !strings.Contains(codeOnlyInstruction, want) {
			t.Errorf("codeOnlyInstruction missing %q; got %q", want, codeOnlyInstruction)
		}
	}
}
