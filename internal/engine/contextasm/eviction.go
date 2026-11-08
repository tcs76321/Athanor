package contextasm

import (
	"context"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/prompt"
)

// Eviction is the outcome of one §10.4 force-eviction (ADR-0022 §5).
type Eviction struct {
	// Tiers is what the seam moved to Dormant, lowest priority first. Empty
	// means nothing was evictable, and the §10.4 gate must pause.
	Tiers []prompt.Tier
	// FreedTokens is the summed estimated weight of Tiers.
	FreedTokens int
}

// Evictor is §10.4's force-eviction seam. It receives the tier weights from the
// assembly that triggered it so the ladder can pick tiers without re-deriving
// prices, and it is stateless — the caller persists whatever it returns.
//
// A nil Evictor is valid configuration: critical pressure then treats the call
// as a floor breach and pauses.
type Evictor interface {
	Evict(ctx context.Context, jobID string, weights map[prompt.Tier]int, suppressed []prompt.Tier) (Eviction, error)
}

// ladderEvictor is §10.5's ladder as the §10.4 seam (ADR-0023 §6): exactly one
// step down, lowest priority first. It is stateless.
type ladderEvictor struct{}

// NewLadderEvictor returns the production §10.4 eviction seam.
func NewLadderEvictor() Evictor { return ladderEvictor{} }

func (ladderEvictor) Evict(_ context.Context, _ string,
	weights map[prompt.Tier]int, suppressed []prompt.Tier) (Eviction, error) {

	tier, tokens, ok := prompt.EvictNext(weights, suppressed)
	if !ok {
		// Only tiers 1–3 remain: there is no remedy, and the caller's
		// floor-breach pause is the correct outcome.
		return Eviction{}, nil
	}
	return Eviction{Tiers: []prompt.Tier{tier}, FreedTokens: tokens}, nil
}

// ceilingFor is §10.5's assembly budget (ADR-0023 §3): the §10.4 critical
// threshold applied to the persona's window, leaving the residual as
// generation headroom. A non-positive threshold or window means unbounded —
// the pre-T5 behavior — rather than "evict everything".
func ceilingFor(ce config.ContextEngine, window int) int {
	if window <= 0 || ce.KVCacheCriticalThresh <= 0 {
		return 0
	}
	return int(ce.KVCacheCriticalThresh * float64(window))
}
