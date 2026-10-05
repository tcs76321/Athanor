package job

import (
	"errors"
	"testing"
)

// TestLegalTransitions pins the full §8.2 edge set (M3-T1: ADR-0001's
// "M3-T1 completes §8 exactly" lands here). Every legal edge in
// `transitions` is enumerated; if the table grows, this test will fail
// and force the new edge to be added consciously.
func TestLegalTransitions(t *testing.T) {
	legal := []struct{ from, to State }{
		{StateQueued, StateContextBuilding},
		{StateQueued, StateCancelled},
		{StateContextBuilding, StatePlanning},
		{StateContextBuilding, StatePaused},
		{StateContextBuilding, StateFailed},
		{StateContextBuilding, StateCancelled},
		{StatePlanning, StateDiverging},
		{StatePlanning, StatePaused},
		{StatePlanning, StateFailed},
		{StatePlanning, StateCancelled},
		{StateDiverging, StateEvaluating}, // §8.2: evaluating is mandatory
		{StateDiverging, StatePaused},
		{StateDiverging, StateFailed},
		{StateDiverging, StateCancelled},
		{StateEvaluating, StateSynthesizing}, // happy path: ≥1 candidate passed
		{StateEvaluating, StateReflecting},   // failure path: 0 passed
		{StateEvaluating, StatePaused},
		{StateEvaluating, StateFailed},
		{StateEvaluating, StateCancelled},
		{StateReflecting, StateDiverging}, // §13.1 budgeted retry loop
		{StateReflecting, StateSynthesizing},
		{StateReflecting, StatePaused},
		{StateReflecting, StateFailed},
		{StateReflecting, StateCancelled},
		{StateSynthesizing, StateComparing},
		{StateSynthesizing, StatePaused},
		{StateSynthesizing, StateFailed},
		{StateSynthesizing, StateCancelled},
		{StateComparing, StateCompleted},
		{StateComparing, StateFailed},
		{StateComparing, StatePaused},
		{StateComparing, StateCancelled},
		{StatePaused, StateContextBuilding},
		{StatePaused, StatePlanning},
		{StatePaused, StateDiverging},
		{StatePaused, StateEvaluating},
		{StatePaused, StateReflecting},
		{StatePaused, StateSynthesizing},
		{StatePaused, StateCancelled},
		// M6-T4: awaiting_approval is reachable from every active working
		// state and resumes to the state it left (awaiting_from).
		{StateContextBuilding, StateAwaitingApproval},
		{StatePlanning, StateAwaitingApproval},
		{StateDiverging, StateAwaitingApproval},
		{StateEvaluating, StateAwaitingApproval},
		{StateReflecting, StateAwaitingApproval},
		{StateSynthesizing, StateAwaitingApproval},
		{StateComparing, StateAwaitingApproval},
		{StateAwaitingApproval, StateContextBuilding},
		{StateAwaitingApproval, StatePlanning},
		{StateAwaitingApproval, StateDiverging},
		{StateAwaitingApproval, StateEvaluating},
		{StateAwaitingApproval, StateReflecting},
		{StateAwaitingApproval, StateSynthesizing},
		{StateAwaitingApproval, StateComparing},
		{StateAwaitingApproval, StateFailed},
		{StateAwaitingApproval, StateCancelled},
	}
	for _, tc := range legal {
		if !CanTransition(tc.from, tc.to) {
			t.Errorf("CanTransition(%s → %s) = false, want legal §8.2 edge", tc.from, tc.to)
		}
		if err := ValidateTransition(tc.from, tc.to); err != nil {
			t.Errorf("ValidateTransition(%s → %s) = %v, want nil", tc.from, tc.to, err)
		}
	}
}

// TestIllegalTransitions covers the non-edges: skipped phases, backwards
// moves, edges into `awaiting_approval` (M6 does not exist yet), and
// self-loops. Every §8.2-mandated adjacency is in the legal table; this
// list is the explicit "and these adjacencies are NOT legal" guard.
func TestIllegalTransitions(t *testing.T) {
	illegal := []struct{ from, to State }{
		{StateQueued, StatePlanning},  // skip context_building
		{StateQueued, StateDiverging}, // skip two phases
		{StateQueued, StateCompleted}, // work cannot teleport
		{StateQueued, StatePaused},    // queued work just never starts
		{StateQueued, StateFailed},
		{StateContextBuilding, StateDiverging}, // skip planning
		{StatePlanning, StateSynthesizing},     // skip diverging
		{StatePlanning, StateComparing},
		{StatePlanning, StateEvaluating},     // skip diverging
		{StateDiverging, StateComparing},     // skip evaluating + synthesizing
		{StateDiverging, StateSynthesizing},  // skip evaluating
		{StateDiverging, StateReflecting},    // reflection is post-evaluating
		{StateEvaluating, StateDiverging},    // no backwards edges
		{StateEvaluating, StateComparing},    // skip synthesizing
		{StateSynthesizing, StateCompleted},  // comparing is mandatory
		{StateSynthesizing, StateDiverging},  // no backwards edges
		{StateSynthesizing, StateEvaluating}, // no backwards edges
		{StateComparing, StateSynthesizing},  // no backwards edges
		{StateComparing, StateReflecting},    // reflection is pre-comparing
		{StateCompleted, StateQueued},        // terminal states are final
		{StateFailed, StateQueued},
		{StateFailed, StateComparing},
		{StateCancelled, StateQueued},
		{StateCompleted, StateCompleted}, // self-loops
		{StatePaused, StatePaused},
		{StatePlanning, StatePlanning},
		{StateEvaluating, StateEvaluating},
		{StateDiverging, StateDiverging},
		{StatePaused, StateAwaitingApproval},    // pausing and awaiting are distinct
		{StateQueued, StateAwaitingApproval},    // queued work never awaited
		{StateCompleted, StateAwaitingApproval}, // terminal states are final
		{"bogus", StatePlanning},                // unknown states
		{StatePlanning, "bogus"},
	}
	for _, tc := range illegal {
		if CanTransition(tc.from, tc.to) {
			t.Errorf("CanTransition(%s → %s) = true, want illegal", tc.from, tc.to)
		}
		err := ValidateTransition(tc.from, tc.to)
		if err == nil {
			t.Errorf("ValidateTransition(%s → %s) = nil, want error", tc.from, tc.to)
			continue
		}
		// Unknown states get a different error shape than illegal edges.
		if !tc.from.Valid() || !tc.to.Valid() {
			var ite *IllegalTransitionError
			if errors.As(err, &ite) {
				t.Errorf("ValidateTransition(%s → %s) = illegal-edge error, want unknown-state error", tc.from, tc.to)
			}
		}
	}
}

// TestIllegalTransitionErrorNamesStates is the M1-T4 acceptance clause:
// illegal transitions are rejected *by tests* naming both states.
func TestIllegalTransitionErrorNamesStates(t *testing.T) {
	err := ValidateTransition(StateCompleted, StateQueued)
	var ite *IllegalTransitionError
	if !errors.As(err, &ite) {
		t.Fatalf("err = %v (%T), want IllegalTransitionError", err, err)
	}
	if ite.From != StateCompleted || ite.To != StateQueued {
		t.Errorf("error states = %s → %s, want completed → queued", ite.From, ite.To)
	}
	want := "illegal job transition completed → queued"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}

func TestStateTerminal(t *testing.T) {
	for s, want := range map[State]bool{
		StateQueued: false, StateContextBuilding: false, StatePlanning: false,
		StateDiverging: false, StateEvaluating: false, StateReflecting: false,
		StateSynthesizing: false, StateComparing: false, StateAwaitingApproval: false,
		StatePaused: false, StateCompleted: true, StateFailed: true, StateCancelled: true,
	} {
		if got := s.Terminal(); got != want {
			t.Errorf("State(%q).Terminal() = %v, want %v", s, got, want)
		}
	}
}

func TestStateValid(t *testing.T) {
	for _, s := range []State{StateQueued, StateContextBuilding, StatePlanning, StateDiverging,
		StateEvaluating, StateReflecting, StateSynthesizing, StateComparing,
		StateAwaitingApproval, StatePaused, StateCompleted, StateFailed, StateCancelled} {
		if !s.Valid() {
			t.Errorf("State(%q).Valid() = false, want true", s)
		}
	}
	for _, s := range []State{"", "running", "PAUSED", "queued "} {
		if s.Valid() {
			t.Errorf("State(%q).Valid() = true, want false", s)
		}
	}
}
