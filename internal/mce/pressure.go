package mce

import (
	"fmt"

	"github.com/tcs76321/athanor/internal/config"
)

// Pressure assessment (ARCHITECTURE §10.4; ROADMAP M5-T4; ADR-0022).
//
// Assess is the per-call KV-cache pressure gate: it decides what the
// engine must do with an assembled prompt *before* the LLM request is
// sent. It is a pure function — no LLM, no I/O — mirroring the
// DecideWinner pattern: deciding is testable in isolation; acting on the
// decision (auditing, evicting, pausing) is the engine's job.
//
// It is complementary to llm.Check (§12.6, ADR-0002): Check is the
// pre-assembly feasibility gate (persona target vs archetype floor —
// what the *window* must be); Assess is the per-call pressure gate
// (estimated tokens vs the resolved window — what goes *in* it). Both
// pause; neither truncates.

// Action is the §10.4 trigger outcome for one call.
type Action string

const (
	// ActionNone: pressure is below the warning threshold; proceed.
	ActionNone Action = "none"
	// ActionWarn: pressure exceeded kv_cache_warning_threshold (§10.4
	// 85% arm). The engine audits the row; from M5-T5 the prompt also
	// carries the context_swap suggestion and oldest chunks move to
	// Dormant.
	ActionWarn Action = "warn"
	// ActionCritical: pressure exceeded kv_cache_critical_threshold
	// (§10.4 95% arm). The engine force-evicts the lowest-priority
	// §10.5 tier through the Evictor seam.
	ActionCritical Action = "critical"
	// ActionFloorBreach: the request cannot fit the window (or the
	// window itself is nonsensical). The engine pauses the job and
	// escalates per §12.3 — the request is never sent, because Ollama
	// would silently truncate it at num_ctx.
	ActionFloorBreach Action = "floor_breach"
)

// Assessment is the outcome of one pressure check.
type Assessment struct {
	// Pressure is activeTokens / maxContext. 0 when the window is
	// nonsensical (maxContext <= 0).
	Pressure float64
	// Action is the §10.4 trigger outcome.
	Action Action
	// Recommendation is the human-readable §12.3/§10.4 action for the
	// EventLog. Empty only for ActionNone.
	Recommendation string
}

// Assess evaluates KV-cache pressure for one assembled prompt.
//
// Semantics (ADR-0022 §1–2): strictly `>` against the configured
// thresholds (§10.4's own wording "active_tokens > 85% of
// max_context"); `>=` for the floor breach — a window filled to exactly
// 100% has no room for even one completion token, and sending would
// silently truncate. maxContext <= 0 fails closed: an unset or
// nonsensical window is a configuration error, never an excuse to send
// an unbounded prompt.
//
// maxContext is the persona's ContextTarget — the num_ctx actually
// loaded (§12.3) — so the decision is deterministic and testable
// offline; Ollama-reported actuals are calibration evidence, not input.
func Assess(activeTokens, maxContext int, ce config.ContextEngine) Assessment {
	if maxContext <= 0 {
		return Assessment{
			Action: ActionFloorBreach,
			Recommendation: fmt.Sprintf(
				"kv-cache window nonsensical: max_context=%d (persona ContextTarget). "+
					"Fix the persona configuration; Athanor never silently truncates context.",
				maxContext),
		}
	}
	pressure := float64(activeTokens) / float64(maxContext)
	if activeTokens >= maxContext {
		return Assessment{
			Pressure: pressure,
			Action:   ActionFloorBreach,
			Recommendation: fmt.Sprintf(
				"kv-cache floor breached: %d active tokens >= %d window (%.1f%%). "+
					"Per §12.3: try another persona, a smaller model with larger context, "+
					"or reduce task scope explicitly; escalate to HITL if quality may be "+
					"compromised. Athanor never silently truncates context.",
				activeTokens, maxContext, pressure*100),
		}
	}
	switch {
	case pressure > ce.KVCacheCriticalThresh:
		return Assessment{
			Pressure: pressure,
			Action:   ActionCritical,
			Recommendation: fmt.Sprintf(
				"kv-cache pressure %.1f%% exceeds critical threshold %.0f%%: "+
					"force-evict lowest-priority §10.5 tier to Dormant (§10.4)",
				pressure*100, ce.KVCacheCriticalThresh*100),
		}
	case pressure > ce.KVCacheWarningThresh:
		return Assessment{
			Pressure: pressure,
			Action:   ActionWarn,
			Recommendation: fmt.Sprintf(
				"kv-cache pressure %.1f%% exceeds warning threshold %.0f%%: "+
					"issue context_swap suggestion and move oldest non-pinned, "+
					"non-critical chunks to Dormant (§10.4)",
				pressure*100, ce.KVCacheWarningThresh*100),
		}
	}
	return Assessment{Pressure: pressure, Action: ActionNone}
}
