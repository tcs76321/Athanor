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
	// TestCommand is the code goal's real test, run in the Job Pod after the
	// candidate is materialized to /tmp/solution.py (F4-T0). It imports the
	// module and asserts the goal's contract. Empty for non-code goals.
	TestCommand string
	// Fixture is the bench task's starter repo/files path (M8-T16, ADR-0061);
	// empty for the locked M3-T7 set and for tasks with no fixture.
	Fixture string
	// Checks is the bench task's deterministic check map (M8-T18); nil for the
	// locked M3-T7 set and for code tasks (whose test command is the check).
	Checks map[string]any
}

// sampleGoals is the 10-goal set: M1-T8's five then M3-T2's five, with no
// duplicates (M1's code goal is distinct from M3-T2's). Goal 9 is the
// deliberately failure-sensitive "must always succeed" sample. The
// objectives are deliberately small and bounded (explicit counts and
// section names) so a 9B generator's output — and the difference between
// the N=3 and N=1 arms — is visible rather than buried in long prose.
var sampleGoals = []sampleGoal{
	{Number: 1, Name: "local-first-essay", Source: "M1-T8", Archetype: "text",
		Goal:     "Write exactly three short paragraphs arguing why local-first software matters, one reason per paragraph.",
		Criteria: []string{"exactly three paragraphs", "each paragraph names a distinct reason", "a one-sentence conclusion"}},
	{Number: 2, Name: "onboarding-email", Source: "M1-T8", Archetype: "text",
		Goal:     "Draft a short onboarding email (under 120 words) for a new member of a local software club.",
		Criteria: []string{"under 120 words", "one clear call to action"}},
	{Number: 3, Name: "book-collection", Source: "M1-T8", Archetype: "code",
		Goal:        "Write a Python module defining a Book dataclass with title and author fields, and a function summarize(book) that returns 'title by author'.",
		Criteria:    []string{"pure stdlib", "docstrings on every public function", "a usage example"},
		TestCommand: `python -c "import solution; b=solution.Book(title='Dune',author='Herbert'); assert solution.summarize(b)=='Dune by Herbert', solution.summarize(b)"`},
	{Number: 4, Name: "md2html-readme", Source: "M1-T8", Archetype: "document",
		Goal:     "Write a short README with exactly three sections named Install, Usage, and License.",
		Criteria: []string{"sections named Install, Usage, License", "a one-line project description at the top"}},
	{Number: 5, Name: "sunrise-alarm-brief", Source: "M1-T8", Archetype: "document",
		Goal:     "Write a short design brief with exactly three sections: Parts, Steps, and Risks.",
		Criteria: []string{"sections named Parts, Steps, Risks", "at least two risks named"}},
	{Number: 6, Name: "fibonacci", Source: "M3-T2", Archetype: "code",
		Goal:        "Write a Python function fib(n) that returns the n-th Fibonacci number using recursion, with a docstring.",
		Criteria:    []string{"pure stdlib", "a docstring on the function", "a usage example"},
		TestCommand: `python -c "import solution; assert solution.fib(0)==0 and solution.fib(1)==1 and solution.fib(10)==55"`},
	{Number: 7, Name: "stringutils", Source: "M3-T2", Archetype: "code",
		Goal:        "Write a Python module with three one-line functions: trim(s), pad(s, width), and reverse(s).",
		Criteria:    []string{"pure stdlib", "docstrings on every public function"},
		TestCommand: `python -c "import solution; assert solution.trim('  x  ')=='x'; assert solution.pad('x',3).startswith('x'); assert solution.reverse('abc')=='cba'"`},
	{Number: 8, Name: "cache-class", Source: "M3-T2", Archetype: "code",
		Goal:        "Write a Python class Cache with get(key), set(key, value), and evict(key) methods.",
		Criteria:    []string{"pure stdlib", "no TODO or FIXME placeholders"},
		TestCommand: `python -c "import solution; c=solution.Cache(); c.set('a',1); assert c.get('a')==1; c.evict('a'); assert c.get('a') is None"`},
	{Number: 9, Name: "always-reverse", Source: "M3-T2", Archetype: "code",
		Goal:        "Write a Python function reverse(s) that returns its input reversed and must always succeed.",
		Criteria:    []string{"pure stdlib", "tests pass"},
		TestCommand: `python -c "import solution; assert solution.reverse('abc')=='cba'; assert solution.reverse('')==''"`},
	{Number: 10, Name: "todo-list", Source: "M3-T2", Archetype: "code",
		Goal:        "Write a Python class TodoList with add(task), complete(index), and pending() methods.",
		Criteria:    []string{"pure stdlib", "docstrings on every public function"},
		TestCommand: `python -c "import solution; t=solution.TodoList(); t.add('a'); t.add('b'); t.complete(0); assert len(t.pending())==1, t.pending()"`},
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
	Label string // filesystem-safe
	Model string // Ollama tag
	// Family is the declared model lineage written to
	// personas.<role>.family. The engine's cross-family judge guard compares
	// it, and it is NOT always the tag prefix: ornith-1.5 reports family
	// "qwen35", not "ornith". Declaring it makes the guard correct.
	Family        string
	ContextTarget int
}

// probeModels are the generators under test, smallest to largest. Every one
// is also an offline judge (judgeModels): the probe's design is to measure
// each model as a generator AND as a judge across the size/family ladder.
var probeModels = []probeModel{
	{Label: "granite3b", Model: "granite4.2:3b", Family: "granite", ContextTarget: 32768},
	{Label: "gemmae4b", Model: "gemma4:e4b-mlx", Family: "gemma", ContextTarget: 32768},
	{Label: "granite8b", Model: "granite4.2:8b", Family: "granite", ContextTarget: 32768},
	{Label: "ornith9b", Model: "ornith-1.5:9b", Family: "qwen35", ContextTarget: 32768},
	{Label: "gemma12b", Model: "gemma4:12b-mlx", Family: "gemma", ContextTarget: 32768},
	{Label: "qwen27b", Model: "qwen3.8:27b-mlx", Family: "qwen3.8", ContextTarget: 32768},
	{Label: "granite30b", Model: "granite4.2:30b", Family: "granite", ContextTarget: 32768},
	{Label: "nemotron30b", Model: "nemotron-3.5-lightning:30b-mlx", Family: "nemotron", ContextTarget: 32768},
	{Label: "muse30b", Model: "muse-glimmer:30b-mlx", Family: "muse", ContextTarget: 32768},
	{Label: "ornith35b", Model: "ornith-1.5:35b", Family: "qwen35", ContextTarget: 32768},
}

// judgeModels are the offline judges that score every artifact. In this
// design they are the full model roster (each model is judged by all
// others); "independence" is enforced where it matters — the in-engine
// cross-family judge per generator — not by excluding judges from the
// generator set.
var judgeModels = []string{
	"granite4.2:3b", "gemma4:e4b-mlx", "granite4.2:8b", "ornith-1.5:9b",
	"gemma4:12b-mlx", "qwen3.8:27b-mlx", "granite4.2:30b",
	"nemotron-3.5-lightning:30b-mlx", "muse-glimmer:30b-mlx", "ornith-1.5:35b",
}

// familyForTag returns the declared family for a model tag known to the
// matrix, or "" when unknown.
func familyForTag(tag string) string {
	for _, m := range probeModels {
		if m.Model == tag {
			return m.Family
		}
	}
	return ""
}

// modelByLabel resolves a matrix model by its filesystem label.
func modelByLabel(label string) (probeModel, bool) {
	for _, m := range probeModels {
		if m.Label == label {
			return m, true
		}
	}
	return probeModel{}, false
}

// armByName resolves a matrix arm by name.
func armByName(name string) (arm, bool) {
	for _, a := range arms {
		if a.Name == name {
			return a, true
		}
	}
	return arm{}, false
}
