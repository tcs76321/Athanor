package prompt

import "fmt"

// The §10.5 priority queue (M5-T5, ADR-0023).
//
// §10.5 defines two separate orderings and this file keeps them
// separate: *eviction* priority (the tiers below, highest number evicted
// first) and *construction* order (§11.2, see assemble.go). Nothing here
// reorders text; it only decides which tiers contribute tokens.
//
// A tier is "present" when its input renders non-empty text. The ladder
// therefore never walks a tier that could not be dropped, which is what
// makes the eviction report honest: every entry in `Evicted` gave back
// real tokens.

// Tier is one §10.5 priority tier.
type Tier int

const (
	// TierStaticSystem is §10.5 tier 1: static system prompt, security
	// and tool constraints, runtime policy (§11.2 §1–3). Never evicted.
	TierStaticSystem Tier = 1
	// TierTaskCriteria is §10.5 tier 2: project context, task context,
	// acceptance criteria, user preferences (§11.2 §4–6, §11). Never
	// evicted.
	TierTaskCriteria Tier = 2
	// TierWorkingSet is §10.5 tier 3: the active division chunk and the
	// candidate artifacts the phase works on (§11.2 §7, §12). Never
	// evicted.
	TierWorkingSet Tier = 3
	// TierCorrections is §10.5 tier 4: CorrectionRecords relevant to the
	// task (§11.2 §8). Evicted last (producer arrives in M6).
	TierCorrections Tier = 4
	// TierEpisodic is §10.5 tier 5: compacted episodic context
	// (§11.2 §9). Producer arrives in M6.
	TierEpisodic Tier = 5
	// TierDormantIndex is §10.5 tier 6: the Dormant Index table of
	// contents (§11.2 §10).
	TierDormantIndex Tier = 6
	// TierInstructions is §10.5 tier 7: evaluation instructions and
	// strategy notes (§11.2 §13–15). Evicted first.
	TierInstructions Tier = 7
)

// ladderOrder is §10.5's eviction ladder: strictly bottom-up, highest
// tier number first. Tiers 1–3 are deliberately absent — they are
// pinned (Tier.Pinned), so no code path can list them here.
var ladderOrder = []Tier{
	TierInstructions, TierDormantIndex, TierEpisodic, TierCorrections,
}

// Pinned reports whether a tier is protected from eviction (§10.5:
// "tiers are evicted strictly bottom-up (7 → 6 → 5 → 4) before any
// full-fidelity content (tiers 1–3) is touched").
func (t Tier) Pinned() bool {
	return t >= TierStaticSystem && t <= TierWorkingSet
}

// Evictable is the inverse of Pinned.
func (t Tier) Evictable() bool { return !t.Pinned() }

// String names the tier for audit rows (the EventLog carries tier names,
// not integers, so a post-mortem reads without the ADR open).
func (t Tier) String() string {
	switch t {
	case TierStaticSystem:
		return "static_system"
	case TierTaskCriteria:
		return "task_criteria"
	case TierWorkingSet:
		return "working_set"
	case TierCorrections:
		return "corrections"
	case TierEpisodic:
		return "episodic"
	case TierDormantIndex:
		return "dormant_index"
	case TierInstructions:
		return "instructions"
	default:
		return fmt.Sprintf("tier(%d)", int(t))
	}
}

// ChunkText is one §10.1 active division chunk as rendered into §11.2 §7.
type ChunkText struct {
	// ID is the deterministic chunk handle from the Dormant Index; the
	// model can pass it back to context_swap.
	ID string
	// RelPath, LineStart, LineEnd, and Kind are the §10.1 metadata that
	// make the chunk self-describing.
	RelPath   string
	LineStart int
	LineEnd   int
	Kind      string
	// Text is the chunk's bytes, verbatim (division is lossless; the
	// assembler never truncates §11.2 §7).
	Text string
}

// IndexLine is one Dormant Index entry as rendered into §11.2 §10: the
// table of contents that lets the model request a dormant chunk by ID.
type IndexLine struct {
	ChunkID   string
	Summary   string // 1-line summary; empty while summary_status != ready
	RelPath   string
	LineStart int
	LineEnd   int
	Kind      string
}

// CandidateArtifact is one prior output a phase works on (§11.2 §12,
// tier 3). Kind is the artifact kind ("proposal", "draft", …); Content is
// the artifact's bytes verbatim.
type CandidateArtifact struct {
	Kind    string
	Content string
}

// EvictionReport is the outcome of one §10.5 ladder pass.
type EvictionReport struct {
	// Fits reports whether the kept tiers are within the ceiling. False
	// means the pinned tiers alone exceed it: the assembler still
	// renders them (never truncate) and the engine's §10.4 gate decides
	// what happens next (ADR-0023 §4).
	Fits bool
	// Evicted lists the tiers this pass moved to Dormant, in ladder
	// order (tier 7 first).
	Evicted []Tier
	// Suppressed is the full set of evictable tiers absent from the
	// prompt after this pass — the input's suppression plus Evicted, in
	// ascending tier order. Renderers consult this set; it is the single
	// source of truth for "what did the ladder remove", so a caller
	// never recomputes it.
	Suppressed []Tier
	// DroppedTokens is the summed estimated weight of Evicted.
	DroppedTokens int
	// KeptTokens is the summed estimated weight of the tiers that
	// remain, including any that were already suppressed.
	KeptTokens int
}

// Suppresses reports whether the ladder pass removed tier t from the
// prompt. Pinned tiers always return false.
func (r EvictionReport) Suppresses(t Tier) bool {
	for _, s := range r.Suppressed {
		if s == t {
			return true
		}
	}
	return false
}

// applyLadder is §10.5's eviction rule as a pure function: given each
// present tier's estimated weight, a ceiling, and the tiers already
// suppressed for this job, drop evictable tiers bottom-up until the kept
// total fits — or until nothing evictable remains.
//
// Semantics (ADR-0023 §4):
//   - weights holds only *present* tiers; a zero or negative weight is
//     treated as absent (a tier that renders nothing cannot be dropped).
//   - ceiling <= 0 means unbounded (the pre-T5 behavior): nothing is
//     evicted and Fits is true.
//   - suppressed tiers are already gone; their weight is excluded from
//     the total and they are not re-reported as evicted.
//   - pinned tiers named in `suppressed` are ignored: a corrupt or
//     legacy state row can never strip tiers 1–3.
//   - eviction stops at the first total that fits, so a tier is dropped
//     only when the tiers below it were insufficient.
func applyLadder(weights map[Tier]int, ceiling int, suppressed []Tier) EvictionReport {
	gone := make(map[Tier]bool, len(suppressed))
	for _, t := range suppressed {
		if t.Evictable() {
			gone[t] = true
		}
	}

	total := 0
	present := make(map[Tier]int, len(weights))
	for t, w := range weights {
		if w <= 0 {
			continue
		}
		present[t] = w
		if !gone[t] {
			total += w
		}
	}

	rep := EvictionReport{}
	if ceiling <= 0 {
		rep.Fits, rep.KeptTokens = true, total
		rep.Suppressed = sortedSuppressed(gone)
		return rep
	}
	for _, t := range ladderOrder {
		if total <= ceiling {
			break
		}
		w, ok := present[t]
		if !ok || gone[t] {
			continue
		}
		total -= w
		gone[t] = true
		rep.Evicted = append(rep.Evicted, t)
		rep.DroppedTokens += w
	}
	rep.KeptTokens = total
	rep.Fits = total <= ceiling
	rep.Suppressed = sortedSuppressed(gone)
	return rep
}

// EvictNext returns the lowest-priority tier that is present and not
// already suppressed, with its estimated weight — §10.4's "force-evict
// lowest-priority tier to Dormant" as a single §10.5 ladder step.
//
// The bool is false when nothing evictable remains (only tiers 1–3 are
// left). That is exactly when the §10.4 gate must pause instead of sending,
// so the caller's `false` branch is the floor-breach path, not an error.
//
// Pinned tiers are unreachable by construction: ladderOrder contains only
// tiers 4–7, so a weights map naming a pinned tier cannot produce one.
func EvictNext(weights map[Tier]int, suppressed []Tier) (Tier, int, bool) {
	gone := make(map[Tier]bool, len(suppressed))
	for _, t := range suppressed {
		if t.Evictable() {
			gone[t] = true
		}
	}
	for _, t := range ladderOrder {
		if w, ok := weights[t]; ok && w > 0 && !gone[t] {
			return t, w, true
		}
	}
	return 0, 0, false
}

// sortedSuppressed flattens the gone-set into ascending tier order so the
// report is stable for byte-comparison and audit rows (map iteration is
// not).
func sortedSuppressed(gone map[Tier]bool) []Tier {
	if len(gone) == 0 {
		return nil
	}
	out := make([]Tier, 0, len(gone))
	for t := TierCorrections; t <= TierInstructions; t++ {
		if gone[t] {
			out = append(out, t)
		}
	}
	return out
}
