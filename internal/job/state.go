// Package job implements the job state machine (ARCHITECTURE §8) and its
// SQLite-backed repository.
//
// M3-T1 completes §8.2 exactly: the transitions table below now encodes
// the full graph (planning → diverging → evaluating → reflecting →
// synthesizing → comparing → completed/failed) with the §8.2 branch
// logic — evaluating always runs after diverging (§8.2: "no skipping
// evaluation"); reflecting is reached only when no candidate passed
// evaluation, and loops back to diverging within the M3-T4 budget.
// M6-T4 adds the `awaiting_approval` edges: a job may wait for a HITL
// decision from any active working state and resumes to the state it left.
// The schema (migration 0004, plus awaiting_from in 0018) accepts the full
// §8.1 state set. The only edge still absent here is `queued → paused`.
package job

import "fmt"

// State is one §8.1 job state.
type State string

// The §8.1 states.
const (
	StateQueued           State = "queued"
	StateContextBuilding  State = "context_building"
	StatePlanning         State = "planning"
	StateDiverging        State = "diverging"
	StateEvaluating       State = "evaluating" // reachable from M3 (ADR-0001)
	StateReflecting       State = "reflecting" // reachable from M3
	StateSynthesizing     State = "synthesizing"
	StateComparing        State = "comparing"
	StateAwaitingApproval State = "awaiting_approval" // reachable from M6
	StatePaused           State = "paused"
	StateCompleted        State = "completed"
	StateFailed           State = "failed"
	StateCancelled        State = "cancelled"
)

// Terminal reports whether no further transitions may leave the state.
func (s State) Terminal() bool {
	switch s {
	case StateCompleted, StateFailed, StateCancelled:
		return true
	default:
		return false
	}
}

// Valid reports whether s is one of the §8.1 states.
func (s State) Valid() bool {
	switch s {
	case StateQueued, StateContextBuilding, StatePlanning, StateDiverging,
		StateEvaluating, StateReflecting, StateSynthesizing, StateComparing,
		StateAwaitingApproval, StatePaused, StateCompleted, StateFailed, StateCancelled:
		return true
	default:
		return false
	}
}

// transitions is the full §8.2 legal-edge table (M3-T1: ADR-0001's
// "M3-T1 completes §8 exactly" lands here; Gate G3's
// TestLegalTransitions/IllegalTransitions pin the graph).
//
// §8.2 branch rules are encoded in this table, not in the engine:
//
//   - `diverging → evaluating` is mandatory (no skipping evaluation).
//   - `evaluating → synthesizing` is the happy path (≥1 candidate passed).
//   - `evaluating → reflecting` is the failure path (no candidate passed).
//   - `reflecting → diverging` is the budgeted retry loop.
//   - `reflecting → synthesizing` lets reflection produce a final artifact
//     without another divergence pass (e.g. "hybrid of the two best").
//   - `synthesizing → comparing` is mandatory.
//   - Pausing is legal from every active working state; a paused job
//     resumes to the state recorded in paused_from (enforced by the
//     repository, since the table cannot express data-dependent edges).
//   - queued jobs cannot pause — they simply never start.
//   - `awaiting_approval` (M6-T4) is legal from every active working state
//     and resumes to the state recorded in awaiting_from (enforced by the
//     repository, like paused_from). A rejected or expired request fails
//     the job.
var transitions = map[State][]State{
	StateQueued:           {StateContextBuilding, StateCancelled},
	StateContextBuilding:  {StatePlanning, StatePaused, StateAwaitingApproval, StateFailed, StateCancelled},
	StatePlanning:         {StateDiverging, StatePaused, StateAwaitingApproval, StateFailed, StateCancelled},
	StateDiverging:        {StateEvaluating, StatePaused, StateAwaitingApproval, StateFailed, StateCancelled},
	StateEvaluating:       {StateSynthesizing, StateReflecting, StatePaused, StateAwaitingApproval, StateFailed, StateCancelled},
	StateReflecting:       {StateDiverging, StateSynthesizing, StatePaused, StateAwaitingApproval, StateFailed, StateCancelled},
	StateSynthesizing:     {StateComparing, StatePaused, StateAwaitingApproval, StateFailed, StateCancelled},
	StateComparing:        {StateCompleted, StateFailed, StatePaused, StateAwaitingApproval, StateCancelled},
	StateAwaitingApproval: {StateContextBuilding, StatePlanning, StateDiverging, StateEvaluating, StateReflecting, StateSynthesizing, StateComparing, StateFailed, StateCancelled},
	StatePaused:           {StateContextBuilding, StatePlanning, StateDiverging, StateEvaluating, StateReflecting, StateSynthesizing, StateCancelled},
	// Terminal states have no outgoing edges by construction (absent map
	// entries).
}

// CanTransition reports whether from → to is a legal §8.2 edge. The
// paused-resume edge additionally requires to == paused_from; that rule
// is enforced by the repository because it depends on stored data.
func CanTransition(from, to State) bool {
	if !from.Valid() || !to.Valid() || from == to {
		return false
	}
	for _, candidate := range transitions[from] {
		if candidate == to {
			return true
		}
	}
	return false
}

// IllegalTransitionError names both states (M1-T4 acceptance: illegal
// transitions rejected with a specific error).
type IllegalTransitionError struct {
	From, To State
}

func (e *IllegalTransitionError) Error() string {
	return fmt.Sprintf("illegal job transition %s → %s", e.From, e.To)
}

// ValidateTransition returns a typed error when from → to is illegal.
func ValidateTransition(from, to State) error {
	if !from.Valid() {
		return fmt.Errorf("unknown job state %q", string(from))
	}
	if !to.Valid() {
		return fmt.Errorf("unknown job state %q", string(to))
	}
	if !CanTransition(from, to) {
		return &IllegalTransitionError{From: from, To: to}
	}
	return nil
}
