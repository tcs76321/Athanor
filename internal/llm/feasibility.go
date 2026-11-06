package llm

import (
	"fmt"

	"github.com/tcs76321/athanor/internal/config"
)

// Archetype names (§6.2). The set is closed per the projects table CHECK.
const (
	ArchetypeText     = "text"
	ArchetypeCode     = "code"
	ArchetypeDocument = "document"
	ArchetypeData     = "data"
	ArchetypeMedia    = "media"
)

// FloorFor maps a project archetype to its context floor (§12.6).
//
// M1 mapping (documented simplification): code → coding_floor, document →
// document_floor, and text/data/media → simple_floor. research_floor
// belongs to the research *workflow* (M4) rather than an archetype and is
// consulted once that workflow exists. Data/media depth is deliberately
// deferred (ROADMAP backlog).
func FloorFor(archetype string, ce config.ContextEngine) (int, bool) {
	switch archetype {
	case ArchetypeCode:
		return ce.CodingFloor, true
	case ArchetypeDocument:
		return ce.DocumentFloor, true
	case ArchetypeText, ArchetypeData, ArchetypeMedia:
		return ce.SimpleFloor, true
	default:
		return 0, false
	}
}

// floorApplies implements the §12.6 rule: floors bind to the personas
// whose phases consume full-fidelity content chunks (main, alternative,
// security, wide). tall is exempt during Planning and DAG Decomposition
// only — those phases work from task descriptions, acceptance criteria,
// and Dormant Index metadata, not raw code (ADR-0002).
func floorApplies(role, phase string) bool {
	if role == RoleTall && phase == PhasePlanning {
		return false
	}
	switch role {
	case RoleMain, RoleAlternative, RoleSecurity, RoleWide, RoleTall:
		return true
	default:
		// ADR-0104: a pool-selected model has no legacy role name; it still
		// consumes full-fidelity content, so the floor applies (fail closed).
		return true
	}
}

// Verdict is the outcome of a feasibility check (§12.3 / §12.6).
type Verdict struct {
	// Feasible is true when Available >= Required.
	Feasible bool
	// Required is max(persona_context_target, role_applicable_floor) —
	// context is never silently reduced below either bound.
	Required int
	// Available is what the caller can actually give this call. In M1 the
	// executor passes the persona's effective context; later milestones
	// derive it from hardware (§12.3 formula).
	Available int
	// Recommendation is the §12.3 escalation path, human-readable, for
	// logging to the EventLog and pausing the job. Empty when feasible.
	Recommendation string
}

// Check computes context feasibility for one (persona, phase) pair under
// a project archetype. Pure function: pausing the job and logging the
// recommendation is the executor's job, so the escalation path stays
// testable without a running daemon.
//
// unknown archetype → infeasible with an explicit recommendation (fail
// closed rather than pretend a floor of 0 is fine).
func Check(p Persona, phase, archetype string, available int, ce config.ContextEngine) Verdict {
	v := Verdict{Available: available, Required: p.ContextTarget}
	if floor, ok := FloorFor(archetype, ce); ok && floorApplies(p.Role, phase) {
		if floor > v.Required {
			v.Required = floor
		}
	} else if !ok {
		v.Recommendation = fmt.Sprintf(
			"unknown archetype %q: no context floor defined (ARCHITECTURE §6.2)", archetype)
		return v
	}
	if available >= v.Required {
		v.Feasible = true
		return v
	}
	v.Recommendation = fmt.Sprintf(
		"context floor violated for persona %q in phase %q: %d available < %d required "+
			"(persona target %d, archetype floor applies: %t). Per §12.3: try another persona, "+
			"a smaller model with larger context, or reduce task scope explicitly; escalate to HITL "+
			"if quality may be compromised. Athanor never silently truncates context.",
		p.Role, phase, available, v.Required, p.ContextTarget, floorApplies(p.Role, phase))
	return v
}
