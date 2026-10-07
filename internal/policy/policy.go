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

import "github.com/tcs76321/athanor/internal/cognitive"

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
	// Operations is the eligible cognitive-operation set for this job, in
	// canonical order (M8-T8; ADR-0064). It is the seam the selection layer
	// learns into; Default returns the baseline set minus reflection when
	// its budget is zero. Execution does not yet vary with it — that is the
	// M8-T8b follow-up once the harder corpus can justify a selection.
	Operations []string
	// Novelty is how far this task class is from history, in [0,1]
	// (M8-T9): 1 with no prior outcome for the class, 0 once it is
	// familiar. The engine resets learned bias when it is high.
	Novelty float64
	// NoveltyReset reports that learned bias was dropped for this plan
	// (set by the engine; the anti-rut reset, ADR-0064 §5).
	NoveltyReset bool
}

// NoveltyFullSamples is the sample count at which a task class counts as
// fully familiar (novelty 0). Below it, novelty rises linearly to 1 at zero
// history.
const NoveltyFullSamples = 5

// NoveltyThreshold is the novelty at or above which selection resets prior
// specialisation (the anti-rut reset, ADR-0064 §5).
const NoveltyThreshold = 0.5

// Novelty scores how far a task class is from history, in [0,1]: 1 when there
// is no prior outcome for the class, falling to 0 once NoveltyFullSamples
// exist. Pure and total — it conditions only on the sample count, with no
// model, embeddings, or extra dependency. A class with history is familiar; a
// class without is novel.
func Novelty(f Features) float64 {
	if f.RecentSamples <= 0 {
		return 1
	}
	if f.RecentSamples >= NoveltyFullSamples {
		return 0
	}
	return 1 - float64(f.RecentSamples)/float64(NoveltyFullSamples)
}

// eligibleOperations derives the eligible operation set from a plan: the
// canonical baseline, minus reflection when the plan grants it no budget.
// Pure and total, so it stays unit-testable in isolation.
func eligibleOperations(p Plan) []string {
	out := make([]string, 0, len(cognitive.BaselineOperations))
	for _, op := range cognitive.BaselineOperations {
		if op == cognitive.OpReflect && p.MaxReflectionLoops == 0 {
			continue
		}
		out = append(out, op)
	}
	return out
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
	p := Plan{
		Candidates:         candidates,
		MaxReflectionLoops: reflection,
		JudgeMode:          JudgeLLM,
		JudgeCount:         1,
		ModelRouting: map[string]string{
			PhaseEvaluating: "security",
			PhaseComparing:  "security",
		},
	}
	p.Operations = eligibleOperations(p)
	p.Novelty = Novelty(in.Features)
	return p
}
