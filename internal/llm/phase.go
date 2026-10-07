package llm

// Phase names (§8.1 job states; §13.1 Dialectical phases). Most Dialectical
// Engine phase names double as job state names. PhaseRefining is the
// exception (M8-T11, ADR-0064): it is a call phase only — the self-refine
// operation runs *within* the evaluating state (repair the best candidate,
// re-verify in place), so no job state is named "refining".
const (
	PhasePlanning     = "planning"
	PhaseDiverging    = "diverging"
	PhaseEvaluating   = "evaluating"
	PhaseRefining     = "refining"
	PhaseReflecting   = "reflecting"
	PhaseSynthesizing = "synthesizing"
	PhaseComparing    = "comparing"
)

// PhaseSpec states a phase's temperature policy (§13.1). Min == Max means
// the phase pins its temperature outright; otherwise the spec is a range
// and a persona default inside the range is honored, while a default
// outside it is clamped to the nearest bound ("the phase value wins").
type PhaseSpec struct {
	Min float64
	Max float64
}

// phaseSpecs is the §13.1 table.
var phaseSpecs = map[string]PhaseSpec{
	PhasePlanning:     {Min: 0.2, Max: 0.2}, // low — deterministic planning
	PhaseDiverging:    {Min: 0.7, Max: 1.1}, // high — explore, avoid convergence
	PhaseEvaluating:   {Min: 0.0, Max: 0.0}, // zero — maximally deterministic
	PhaseRefining:     {Min: 0.6, Max: 0.8}, // moderate-to-high — targeted repair
	PhaseReflecting:   {Min: 0.6, Max: 0.8}, // moderate-to-high
	PhaseSynthesizing: {Min: 0.2, Max: 0.2}, // low
	PhaseComparing:    {Min: 0.0, Max: 0.0}, // zero — deterministic judgment
}

// PhaseSpec returns the temperature policy for a phase. The second return
// is false for phases with no policy (the persona default then applies
// untouched) — e.g. context_building, which §13.1 does not define.
func PhaseSpecFor(phase string) (PhaseSpec, bool) {
	s, ok := phaseSpecs[phase]
	return s, ok
}

// ResolveTemperature applies §13.1 precedence: an attached ExplorationPath
// stage (M3) overrides both inputs; next the phase policy wins over
// the persona default; a phase with no policy leaves the default alone.
//
// Precedence (highest to lowest):
//
//	ExplorationPath stage > phase policy > persona default
//
// `stageOverride` is the hook for an ExplorationPath stage pinned
// temperature (§13.2). A non-nil pointer wins outright — the user
// has explicitly asked for that temperature, so even a 0.0-pinned
// phase (evaluating, comparing) yields to the override. A nil pointer
// preserves the M1 behavior (phase policy → persona default).
//
// ROADMAP §7 ("Deliberately Deferred"): ExplorationPath persistence
// and the stage-resolution table land in a later task. M3-T1 only
// wires the hook so the seam is in place; today every caller passes
// nil and the result matches M1 byte-for-byte.
func ResolveTemperature(phase string, personaDefault float64, stageOverride *float64) float64 {
	if stageOverride != nil {
		return *stageOverride
	}
	spec, ok := phaseSpecs[phase]
	if !ok {
		return personaDefault
	}
	if personaDefault < spec.Min {
		return spec.Min
	}
	if personaDefault > spec.Max {
		return spec.Max
	}
	return personaDefault
}
