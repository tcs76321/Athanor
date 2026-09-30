// Package prompt assembles LLM prompts deterministically (ARCHITECTURE
// §11): the same inputs always produce a byte-identical prompt, and every
// section carries token accounting for the EventLog.
//
// M1 scope: the §11.2 assembly-order subset that exists today —
//
//  1. Static System Prompt
//  2. Security and Tool Constraints
//  3. Runtime Policy (phase-specific)
//  4. Project Context (goal, archetype)
//  5. Task Context (title, description)
//  6. Acceptance Criteria
//  13. Evaluation Instructions (comparison phases)
//
// Sections 7–12 (MCE chunks, CorrectionRecords, episodic context, dormant
// index, user preferences, candidate artifacts) arrive with the MCE (M5)
// and feedback systems (M6); their positions in the order are already
// reserved by the builder's section list.
package prompt

import (
	"github.com/tcs76321/athanor/internal/llm"
)

// Section names, in §11.2 assembly order. Every §11.2 section now has a
// name; the ones without a producer yet (8, 9, 14, 15 — M6, and 10/7
// before M5-T5's tier integration) are omitted from assembly until their
// input is populated (the `add` helper's empty-section contract).
const (
	SectionSystem                 = "system"
	SectionSecurityAndTools       = "security_and_tools"
	SectionRuntimePolicy          = "runtime_policy"
	SectionProjectContext         = "project_context"
	SectionTaskContext            = "task_context"
	SectionAcceptanceCriteria     = "acceptance_criteria"
	SectionActiveChunk            = "active_chunk"
	SectionCorrections            = "corrections"
	SectionEpisodic               = "episodic_context"
	SectionDormantIndex           = "dormant_index"
	SectionUserPreferences        = "user_preferences"
	SectionCandidateArtifacts     = "candidate_artifacts"
	SectionEvaluationInstructions = "evaluation_instructions"
	SectionStrategyNotes          = "strategy_notes"
	SectionInterruptionNotes      = "interruption_notes"
)

// staticSystem is tier 1 (§11.1): immutable core identity and safety
// constraints owned by Athanor Core. User configuration can never
// override any part of it.
const staticSystem = `You are Athanor, a local-first semi-autonomous agent.
You work toward explicitly stated goals, produce concrete artifacts, and
treat every stated acceptance criterion as a hard requirement.
You are honest about uncertainty: when information is missing, you say so
instead of inventing it.`

// securityAndTools is tier 2 (§11.1): containment rules. In M1 there are
// provably no tools (Gate G1) — the prompt states this so the model never
// hallucinates tool access.
const securityAndTools = `CONTAINMENT RULES (non-negotiable):
- You have NO tools in this phase. You cannot execute code, run commands,
  read or write files, or access the network.
- If a task cannot be completed by reasoning and writing alone, state what
  is missing instead of pretending to act.
- Never claim to have taken an action you did not take.`

// Project is the §11.2 section-4 input (project context).
type Project struct {
	Name      string
	Archetype string
	Goal      string
}

// Task is the §11.2 section-5 input (task context).
type Task struct {
	Title       string
	Description string
}

// Input is everything a prompt can depend on. Zero-valued optional
// parts are omitted from assembly; Project name, Task title, and Phase
// are required.
//
// M5-T5 (§10.5, ADR-0023): the tier payloads below are the priority
// queue's inputs. A zero-valued tier renders nothing, which is how a
// tier is "absent" — the eviction ladder only ever walks tiers that
// would actually contribute tokens.
type Input struct {
	Phase   string
	Project Project
	Task    Task
	// Criteria are the acceptance criteria, in given order.
	Criteria []string
	// EvaluationInstructions replaces the default comparison instructions
	// when the caller needs phase-specific evaluation detail (§11.2 §13).
	EvaluationInstructions string

	// Tools is the job's resolved §25 tool envelope, in the closed-set
	// order. Empty means "no tools": §11.2 §2 keeps its tool-less text
	// verbatim. Non-empty renders the actual manifest so the prompt can
	// never promise a tool the server-side envelope would refuse
	// (ADR-0023 §8).
	Tools []string
	// UserPreferences is §11.2 §11 (tier 2: operator configuration).
	UserPreferences []string

	// ActiveChunk is the §10.1 active division chunk — §11.2 §7, tier 3
	// (never evicted). Nil when no working set exists.
	ActiveChunk *ChunkText
	// Candidates are the prior outputs a phase works on (synthesize,
	// compare) — §11.2 §12, tier 3 (never evicted). Replaces the
	// pre-T5 practice of concatenating candidate bytes into the §13
	// evaluation instructions.
	Candidates []CandidateArtifact

	// Corrections are §11.2 §8, tier 4 (M6 producer).
	Corrections []string
	// Episodic is §11.2 §9, tier 5 (M6 producer).
	Episodic []string
	// DormantIndex is §11.2 §10, tier 6: the §10.1 table of contents the
	// model uses to request a dormant chunk by ID.
	DormantIndex []IndexLine
	// StrategyNotes is §11.2 §14, tier 7 (active StrategyInsights, §13.4).
	StrategyNotes []string
	// InterruptionNotes is §11.2 §15, tier 7 (M6 watch mode).
	InterruptionNotes []string

	// Ceiling is the §10.5 assembly budget in estimated tokens
	// (ADR-0023 §3: kv_cache_critical_threshold × the persona window).
	// Zero or negative means unbounded — the pre-T5 behavior, which
	// keeps existing callers and tests byte-identical.
	Ceiling int
	// Suppressed names tiers already moved to Dormant for this job
	// (ADR-0023 §5). Pinned tiers in this list are ignored: eviction can
	// never reach tiers 1–3, even from corrupt persisted state.
	Suppressed []Tier
}

// Section is one assembled prompt section with its token accounting.
type Section struct {
	Name   string `json:"name"`
	Text   string `json:"-"`
	Tokens int    `json:"tokens"`
}

// Result is an assembled prompt: the full text, the section accounting,
// the §10.5 eviction outcome, and the chat messages derived from it (the
// system message carries §11.2 sections 1–3; the user message carries the
// rest).
type Result struct {
	Text       string
	Sections   []Section
	TotalToken int
	// Eviction reports the §10.5 ladder pass (M5-T5). With no ceiling it
	// is the zero-value-with-Fits-true case: nothing suppressed.
	Eviction EvictionReport
	// TierWeights is each present tier's estimated token weight — the
	// price the ladder used (M5-T5). Exposed so the engine can record
	// per-tier accounting and hand the weights to the §10.4 eviction seam
	// without re-deriving them.
	TierWeights map[Tier]int
	Messages    []llm.Message
}

// EstimateTokens is the M1 token accounting approximation: ~4 bytes per
// token, deterministic and dependency-free. Exact tokenizer fidelity
// arrives with the MCE (M5); §28.2 only requires per-section accounting
// to be logged consistently.
func EstimateTokens(s string) int {
	return (len(s) + 3) / 4
}
