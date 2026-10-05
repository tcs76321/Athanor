// Package policy is the F4-T1 compute- and model-selection seam (ADR-0044): a
// pure function that decides, for one job, how much compute to spend and which
// model persona serves each phase. It performs no I/O, reads no clock, and uses
// no randomness, so the same inputs always produce the same plan. The engine
// consults it instead of hard-coded constants; a nil policy falls back to
// Default, which reproduces pre-F4 behavior exactly.
//
// The plan is a "river": it allocates compute and selects models, and never
// touches security constraints, containment, the frozen judge, or HITL rules.
package policy

// JudgeMode selects how a job's candidates are adjudicated (F4-T3/T4).
type JudgeMode string

const (
	// JudgeLLM routes acceptance through the LLM judge persona — today's
	// behavior.
	JudgeLLM JudgeMode = "llm"
	// JudgeVerifier routes acceptance through deterministic verifiers, with
	// the LLM judge only on ties (F4-T3).
	JudgeVerifier JudgeMode = "verifier"
)

// Phase keys for ModelRouting. Plain strings keep the policy package free of an
// LLM-registry dependency; the engine maps them to personas.
const (
	PhasePlanning     = "planning"
	PhaseDiverging    = "diverging"
	PhaseEvaluating   = "evaluating"
	PhaseReflecting   = "reflecting"
	PhaseSynthesizing = "synthesizing"
	PhaseComparing    = "comparing"
)

// DefaultReflectionLoops is the reflection ceiling used when configuration is
// absent. It is the engine's historical fallback (formerly the engine-local
// maxReflectionIterations constant), moved here so the engine holds no compute
// constants.
const DefaultReflectionLoops = 2

// Features is what the policy may condition on about the task itself. It is a
// value type so Decide stays pure.
type Features struct {
	Archetype     string
	CriteriaCount int
	// Difficulty is a planning-phase hint ("easy" | "hard" | "", unknown).
	// Default ignores it; Adaptive uses it first.
	Difficulty string
	// RecentSamples and RecentAcceptRate summarize prior StrategyOutcomes for
	// this task class (0 samples = no history). Adaptive's second signal.
	RecentSamples    int
	RecentAcceptRate float64
	// PreferredDivergencePersona is the F4-T7a feedback→policy bias: when an
	// active StrategyInsight names a winning divergence persona for this
	// task class, it leads DivergenceRoles. Empty means no bias.
	PreferredDivergencePersona string
}

// Limits are the operator-configured ceilings (ADR-0044 rule 4). A policy may
// lower compute, never raise it past these.
type Limits struct {
	CandidatesCeiling int
	ReflectionCeiling int
}

// Inputs is the complete, pure input to Decide.
type Inputs struct {
	Features Features
	Limits   Limits
}

// Plan is the resolved compute and model-selection decision for one job.
type Plan struct {
	Candidates         int
	MaxReflectionLoops int
	JudgeMode          JudgeMode
	JudgeCount         int
	// ModelRouting maps a phase (see the Phase* keys) to the persona serving
	// it. An empty map means "engine default": main for generation, security
	// for judgment.
	ModelRouting map[string]string
	// DivergenceRoles is the ordered persona list the divergence phase cycles
	// through (F4-T5 heterogeneous diversity). Empty means "one role for all
	// candidates": the PhaseDiverging route, or main.
	DivergenceRoles []string
	// InsightBias names the F4-T7a active-insight persona that led
	// DivergenceRoles ("" when no insight applied).
	InsightBias string
}

// Policy decides a job's plan. Implementations must be pure and total: no I/O,
// no clock, no randomness, and a defined result for every input.
type Policy interface {
	Decide(Inputs) Plan
}

// Default reproduces the pre-F4 engine: the configured candidate count, the
// configured reflection ceiling, and a single LLM judge on the security persona
// for the evaluation and comparison phases.
type Default struct{}

var _ Policy = Default{}

// Decide implements Policy.
func (Default) Decide(in Inputs) Plan {
	candidates := in.Limits.CandidatesCeiling
	if candidates < 1 {
		candidates = 1
	}
	reflection := in.Limits.ReflectionCeiling
	if reflection < 0 {
		reflection = 0
	}
	return Plan{
		Candidates:         candidates,
		MaxReflectionLoops: reflection,
		JudgeMode:          JudgeLLM,
		JudgeCount:         1,
		ModelRouting: map[string]string{
			PhaseEvaluating: "security",
			PhaseComparing:  "security",
		},
	}
}
