// The M3-T7 experiment matrix: the locked sample set, the two arms, and
// the models under test. All of it is data; the runner in probe.go is the
// only thing that executes it. The definitions mirror
// docs/probes/m3-t7-quality-probe.md exactly — changing them here without
// updating the protocol is a bug.
package main

// sampleGoal is one probe input. Criteria are the task's acceptance
// criteria (§6.1), submitted with the goal.
type sampleGoal struct {
	Number    int
	Name      string
	Source    string // "M1-T8" or "M3-T2"
	Archetype string // text | code | document
	Goal      string
	Criteria  []string
}

// sampleGoals is the 10-goal set: M1-T8's five then M3-T2's five, with no
// duplicates (M1's code goal is distinct from M3-T2's). Goal 9 is the
// deliberately failure-sensitive "must always succeed" sample.
var sampleGoals = []sampleGoal{
	{1, "local-first-essay", "M1-T8", "text",
		"Write a short essay about why local-first software matters.",
		[]string{"at least three arguments", "a conclusion"}},
	{2, "onboarding-email", "M1-T8", "text",
		"Draft a friendly onboarding email for a new community member of a local software club.",
		[]string{"under 200 words", "one clear call to action"}},
	{3, "book-collection", "M1-T8", "code",
		"Write a Python module that manages a personal book collection with add, list, and search functions.",
		[]string{"pure stdlib", "docstrings on every public function", "a usage example"}},
	{4, "md2html-readme", "M1-T8", "document",
		"Create a README for a small CLI tool that converts Markdown files to HTML.",
		[]string{"installation section", "usage examples", "license section"}},
	{5, "sunrise-alarm-brief", "M1-T8", "document",
		"Write a one-page design brief for a weekend project that builds a sunrise alarm clock from a Raspberry Pi.",
		[]string{"parts list", "build steps", "at least two risks named"}},
	{6, "fibonacci", "M3-T2", "code",
		"Write a Python function that returns the n-th Fibonacci number using recursion.",
		[]string{"pure stdlib", "docstrings on every public function", "a usage example"}},
	{7, "stringutils", "M3-T2", "code",
		"Write a Python module with three utility functions for trimming, padding, and reversing strings.",
		[]string{"pure stdlib", "docstrings on every public function"}},
	{8, "cache-class", "M3-T2", "code",
		"Write a Python cache class with get, set, and evict methods.",
		[]string{"pure stdlib", "no TODO or FIXME placeholders"}},
	{9, "always-reverse", "M3-T2", "code",
		"Write a Python function that returns its input reversed. The function must always succeed.",
		[]string{"pure stdlib", "tests pass"}},
	{10, "todo-list", "M3-T2", "code",
		"Write a Python class for managing a todo list, with add, complete, list_pending, and clear methods.",
		[]string{"pure stdlib", "docstrings on every public function", "no TODO or FIXME placeholders"}},
}

// arm is one experimental condition. Candidates is
// execution.divergence_candidates; Runs is how many times each goal is
// executed under the arm.
type arm struct {
	Name       string
	Candidates int
	Runs       int
}

// arms are the two conditions of the headline experiment. The dialectical
// arm runs three times so T-c stability reads the three runs directly;
// the single-shot arm is a one-run baseline.
var arms = []arm{
	{Name: "dialectical", Candidates: 3, Runs: 3},
	{Name: "single", Candidates: 1, Runs: 1},
}

// probeModel is one model under test. ContextTarget is applied to every
// persona: it must meet §12.6's coding_floor (32768) for the code goals,
// or the engine pauses with a floor violation.
type probeModel struct {
	Label         string // filesystem-safe
	Model         string // Ollama tag
	ContextTarget int
}

// probeModels are Runs A and B. Run C (qwen tall/main + ornith
// alternative), the optional cross-family pairing, is launched by
// passing its model mix explicitly; it is not part of the default matrix.
var probeModels = []probeModel{
	{Label: "qwen27b", Model: "qwen3.8:27b-mlx", ContextTarget: 32768},
	{Label: "ornith9b", Model: "ornith-1.5:9b", ContextTarget: 32768},
}

// neutralJudge is the third-family judge used for the neutral channel.
const neutralJudge = "gemma4:12b-mlx"

// crossJudge returns the cross-model judge for a generator: a different
// model family from the generator's, so the judge and generator do not
// share correlated failure modes. An unknown generator label falls back
// to the neutral judge.
func crossJudge(generatorLabel string) string {
	switch generatorLabel {
	case "qwen27b":
		return "ornith-1.5:9b"
	case "ornith9b":
		return "qwen3.8:27b-mlx"
	default:
		return neutralJudge
	}
}
