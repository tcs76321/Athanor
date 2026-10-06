// Package verify is F4-T3's deterministic verification layer. It answers a
// narrow question: does a candidate pass the checks a *parser* can apply,
// with no LLM in the loop? Code candidates are checked by the Job Pod's
// real test run (and lint), and — when execution.require_*_for_code demand
// it (F5) — by the tests-required and documentation-presence gates;
// text/document candidates by structural constraints parsed from the
// acceptance criteria. Anything the library
// cannot parse is "not applicable" — the caller then consults the LLM
// judge, so a verifier can only ever make acceptance more deterministic,
// never weaker.
//
// The package is pure: no I/O, no clock, no randomness, no imports beyond
// the standard library, so every verifier is exhaustively testable and the
// policy seam stays unit-testable in isolation.
package verify

import "github.com/tcs76321/athanor/internal/project"

// Input is everything a verifier may inspect about one candidate.
type Input struct {
	Archetype string
	// Content is the candidate's bytes.
	Content string
	// Criteria are the task's acceptance criteria (free text).
	Criteria []string
	// TestRan/TestCommand/TestsPassed describe the Job Pod's test run for
	// this candidate. TestRan is false when no runner was wired.
	TestRan     bool
	TestCommand string
	TestsPassed bool
	// LintRan/LintPassed describe the linter run (optional).
	LintRan    bool
	LintPassed bool
	// RequireTests and RequireDocs are the execution.require_*_for_code
	// gates (F5). RequireTests: a code candidate fails unless the test
	// command actually ran and passed. RequireDocs: a code candidate fails
	// unless it carries a documentation construct. Both default false at the
	// Input level; the engine resolves the config defaults (true).
	RequireTests bool
	RequireDocs  bool
}

// Verdict is one verifier's decision. Applied reports whether the verifier
// could evaluate this input at all; an unapplied verdict carries no signal.
// Hard marks a verifier whose result is objective enough to decide acceptance
// outright (code tests/lint). A non-hard verifier (a heuristic parse of free
// text) advises but never overrides the LLM judge.
type Verdict struct {
	Verifier string
	Applied  bool
	Hard     bool
	Pass     bool
	Score    float64
	Reasons  []string
}

// Verifier applies one deterministic check.
type Verifier interface {
	Name() string
	Verify(Input) Verdict
}

// Registry maps an archetype to the ordered verifiers that apply to it.
type Registry struct {
	byArchetype map[string][]Verifier
}

// NewRegistry builds a registry from explicit verifiers. Use Default for
// the in-tree set.
func NewRegistry(vs ...Verifier) *Registry {
	r := &Registry{byArchetype: map[string][]Verifier{}}
	for _, v := range vs {
		// A verifier declares its archetype via the Archetypes interface
		// below when it is archetype-specific; the default registry wires
		// them explicitly, so this constructor only groups by that.
		if av, ok := v.(archetypeVerifier); ok {
			for _, a := range av.Archetypes() {
				r.byArchetype[a] = append(r.byArchetype[a], v)
			}
			continue
		}
		r.byArchetype[""] = append(r.byArchetype[""], v)
	}
	return r
}

// archetypeVerifier is an optional interface a Verifier may implement to
// declare the archetypes it serves.
type archetypeVerifier interface {
	Archetypes() []string
}

// Default is the in-tree verifier set: code gets the real test run, the
// linter, and (when required) the documentation-presence gate; text/document
// get structural checks; data/media have no deterministic verifier yet
// (deferred), so their candidates are decided by the LLM judge.
func Default() *Registry {
	return NewRegistry(
		testsVerifier{},
		lintVerifier{},
		docsVerifier{},
		structureVerifier{},
	)
}

// Result is the aggregate of every applicable verifier for one candidate.
type Result struct {
	Applied   int
	Passed    bool
	Score     float64
	Verifiers []string
	Reasons   []string
	// HardApplied/HardPassed track the objective subset (code tests/lint),
	// which may decide acceptance on its own.
	HardApplied int
	HardPassed  bool
}

// Decisive reports whether at least one verifier could decide. A
// non-decisive result must fall through to the LLM judge.
func (r Result) Decisive() bool { return r.Applied > 0 }

// HardDecisive reports whether an objective verifier applied, so the result
// may decide acceptance (not merely advise). HardPassed is meaningful only
// when HardDecisive is true.
func (r Result) HardDecisive() bool { return r.HardApplied > 0 }

// For returns the verifiers registered for an archetype.
func (r *Registry) For(archetype string) []Verifier {
	if r == nil {
		return nil
	}
	return r.byArchetype[archetype]
}

// Run applies every verifier for the archetype and aggregates the
// applicable verdicts. Only applicable verdicts count: an unparsed
// constraint is silence, not a pass.
func (r *Registry) Run(in Input) Result {
	var out Result
	out.HardPassed = true
	var sum float64
	passed := true
	for _, v := range r.For(in.Archetype) {
		vd := v.Verify(in)
		if !vd.Applied {
			continue
		}
		out.Applied++
		out.Verifiers = append(out.Verifiers, vd.Verifier)
		out.Reasons = append(out.Reasons, vd.Reasons...)
		sum += vd.Score
		if !vd.Pass {
			passed = false
		}
		if vd.Hard {
			out.HardApplied++
			if !vd.Pass {
				out.HardPassed = false
			}
		}
	}
	out.Passed = passed
	if out.Applied > 0 {
		out.Score = sum / float64(out.Applied)
	}
	return out
}

// testsVerifier folds the Job Pod's real test result (F4-T0a) into a
// deterministic verdict. It applies only when the test command actually
// ran, so a missing runner degrades to the LLM judge rather than a free
// pass.
type testsVerifier struct{}

func (testsVerifier) Archetypes() []string {
	return []string{project.ArchetypeCode}
}
func (testsVerifier) Name() string { return "tests" }
func (testsVerifier) Verify(in Input) Verdict {
	if !in.TestRan {
		// F5: when the operator requires tests for code, a candidate with no
		// test run fails deterministically instead of falling through to the
		// LLM judge. Without the requirement, a missing run stays
		// non-decisive (the pre-F5 behavior).
		if in.RequireTests {
			return Verdict{Verifier: "tests", Applied: true, Hard: true, Pass: false, Score: 0,
				Reasons: []string{"tests required for code but no test run occurred"}}
		}
		return Verdict{Verifier: "tests", Applied: false, Hard: true}
	}
	if in.TestsPassed {
		return Verdict{Verifier: "tests", Applied: true, Hard: true, Pass: true, Score: 1,
			Reasons: []string{"test command exited 0"}}
	}
	return Verdict{Verifier: "tests", Applied: true, Hard: true, Pass: false, Score: 0,
		Reasons: []string{"test command failed: " + in.TestCommand}}
}

// lintVerifier folds a linter run into a verdict. It applies only when the
// linter ran.
type lintVerifier struct{}

func (lintVerifier) Archetypes() []string {
	return []string{project.ArchetypeCode}
}
func (lintVerifier) Name() string { return "lint" }
func (lintVerifier) Verify(in Input) Verdict {
	if !in.LintRan {
		return Verdict{Verifier: "lint", Applied: false, Hard: true}
	}
	if in.LintPassed {
		return Verdict{Verifier: "lint", Applied: true, Hard: true, Pass: true, Score: 1,
			Reasons: []string{"linter exited 0"}}
	}
	return Verdict{Verifier: "lint", Applied: true, Hard: true, Pass: false, Score: 0,
		Reasons: []string{"linter reported findings"}}
}
