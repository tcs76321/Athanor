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
}

// sampleGoals is the 10-goal set: M1-T8's five then M3-T2's five, with no
// duplicates (M1's code goal is distinct from M3-T2's). Goal 9 is the
// deliberately failure-sensitive "must always succeed" sample. The
// objectives are deliberately small and bounded (explicit counts and
// section names) so a 9B generator's output — and the difference between
// the N=3 and N=1 arms — is visible rather than buried in long prose.
var sampleGoals = []sampleGoal{
	{1, "local-first-essay", "M1-T8", "text",
		"Write exactly three short paragraphs arguing why local-first software matters, one reason per paragraph.",
		[]string{"exactly three paragraphs", "each paragraph names a distinct reason", "a one-sentence conclusion"}, ""},
	{2, "onboarding-email", "M1-T8", "text",
		"Draft a short onboarding email (under 120 words) for a new member of a local software club.",
		[]string{"under 120 words", "one clear call to action"}, ""},
	{3, "book-collection", "M1-T8", "code",
		"Write a Python module defining a Book dataclass with title and author fields, and a function summarize(book) that returns 'title by author'.",
		[]string{"pure stdlib", "docstrings on every public function", "a usage example"},
		`python -c "import solution; b=solution.Book(title='Dune',author='Herbert'); assert solution.summarize(b)=='Dune by Herbert', solution.summarize(b)"`},
	{4, "md2html-readme", "M1-T8", "document",
		"Write a short README with exactly three sections named Install, Usage, and License.",
		[]string{"sections named Install, Usage, License", "a one-line project description at the top"}, ""},
	{5, "sunrise-alarm-brief", "M1-T8", "document",
		"Write a short design brief with exactly three sections: Parts, Steps, and Risks.",
		[]string{"sections named Parts, Steps, Risks", "at least two risks named"}, ""},
	{6, "fibonacci", "M3-T2", "code",
		"Write a Python function fib(n) that returns the n-th Fibonacci number using recursion, with a docstring.",
		[]string{"pure stdlib", "a docstring on the function", "a usage example"},
		`python -c "import solution; assert solution.fib(0)==0 and solution.fib(1)==1 and solution.fib(10)==55"`},
	{7, "stringutils", "M3-T2", "code",
		"Write a Python module with three one-line functions: trim(s), pad(s, width), and reverse(s).",
		[]string{"pure stdlib", "docstrings on every public function"},
		`python -c "import solution; assert solution.trim('  x  ')=='x'; assert solution.pad('x',3).startswith('x'); assert solution.reverse('abc')=='cba'"`},
	{8, "cache-class", "M3-T2", "code",
		"Write a Python class Cache with get(key), set(key, value), and evict(key) methods.",
		[]string{"pure stdlib", "no TODO or FIXME placeholders"},
		`python -c "import solution; c=solution.Cache(); c.set('a',1); assert c.get('a')==1; c.evict('a'); assert c.get('a') is None"`},
	{9, "always-reverse", "M3-T2", "code",
		"Write a Python function reverse(s) that returns its input reversed and must always succeed.",
		[]string{"pure stdlib", "tests pass"},
		`python -c "import solution; assert solution.reverse('abc')=='cba'; assert solution.reverse('')==''"`},
	{10, "todo-list", "M3-T2", "code",
		"Write a Python class TodoList with add(task), complete(index), and pending() methods.",
		[]string{"pure stdlib", "docstrings on every public function"},
		`python -c "import solution; t=solution.TodoList(); t.add('a'); t.add('b'); t.complete(0); assert len(t.pending())==1, t.pending()"`},
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

// probeModels are the generator(s) under test. The first full M3-T7 run
// uses the fast local model only; the 27B model is an optional later run
// (see docs/probes/m3-t7-quality-probe.md).
var probeModels = []probeModel{
	{Label: "ornith9b", Model: "ornith-1.5:9b", ContextTarget: 32768},
}

// judgeModels are the independent third-party judges that score every
// artifact offline (the `judge` subcommand). Both differ in family from
// the generator, so their errors are not correlated with it.
var judgeModels = []string{"gemma4:12b-mlx", "granite4.2:3b"}

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
