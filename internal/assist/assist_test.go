package assist

import (
	"context"
	"strings"
	"testing"
)

type fakeGen struct {
	system  string
	systems []string
	user    string
	users   []string
	out     string
	outs    []string
	err     error
	called  bool
}

func (f *fakeGen) Generate(ctx context.Context, system, user string) (string, error) {
	f.called = true
	f.system, f.user = system, user
	f.systems = append(f.systems, system)
	f.users = append(f.users, user)
	if len(f.outs) > 0 {
		out := f.outs[0]
		f.outs = f.outs[1:]
		return out, f.err
	}
	return f.out, f.err
}

func TestRefineParsesStructuredProposal(t *testing.T) {
	gen := &fakeGen{out: `{"text":"Build a tested CSV parser that handles empty rows and reports each failure with the row number.","notes":["made the outcome explicit"],"added_assumptions":["empty rows are in scope"]}`}
	s := New(gen)
	res, err := s.Refine(context.Background(), "project.goal", "  make a csv parser  ", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "tested CSV parser") {
		t.Fatalf("text = %q", res.Text)
	}
	if len(res.Notes) != 1 || len(res.AddedAssumptions) != 1 {
		t.Fatalf("result = %+v", res)
	}
	if res.Field != "project.goal" || res.Mode != "improve" {
		t.Fatalf("metadata = %+v", res)
	}
	if !strings.Contains(gen.users[0], "PURPOSE:") || !strings.Contains(gen.users[0], "make a csv parser") {
		t.Fatalf("prompt = %q", gen.users[0])
	}
	if !strings.Contains(gen.systems[0], "Preserve the user's facts") || !strings.Contains(gen.systems[0], "Do not invent URLs") {
		t.Fatalf("concise system prompt lost its safety rules: %q", gen.systems[0])
	}
}

func TestSystemPromptMakesImprovementDefault(t *testing.T) {
	for _, want := range []string{"Never invent facts", "generic adjectives", "Improvement is the default"} {
		if !strings.Contains(systemPrompt, want) {
			t.Errorf("system prompt missing %q", want)
		}
	}
}

func TestRefineAcceptsStringNotesAndAssumptions(t *testing.T) {
	// Small local models often return notes as one string rather than an array.
	gen := &fakeGen{out: `{"text":"The project goal is to continuously search for news, translate it, and produce daily English reports.","notes":"Restructured around the outcome and scope.","added_assumptions":["The audience is not stated."],"unchanged":false,"needs_clarification":false,"questions":[]}`}
	res, err := New(gen).Refine(context.Background(), "project.goal", "search news, translate, report", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(strings.TrimSpace(res.Text), "{") {
		t.Fatalf("JSON leaked into the field text: %q", res.Text)
	}
	if !strings.Contains(res.Text, "daily English reports") {
		t.Fatalf("text = %q", res.Text)
	}
	if len(res.Notes) != 1 || len(res.AddedAssumptions) != 1 {
		t.Fatalf("result = %+v", res)
	}
}

func TestRefineUnwrapsNestedJSONText(t *testing.T) {
	gen := &fakeGen{out: `{"text":"{\"text\":\"A clear goal.\",\"notes\":[\"restructured\"]}","notes":[],"added_assumptions":[],"unchanged":false}`}
	res, err := New(gen).Refine(context.Background(), "project.criteria", "make a clear goal", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "A clear goal." {
		t.Fatalf("nested text = %q", res.Text)
	}
	if len(res.Notes) != 1 || res.Notes[0] != "restructured" {
		t.Fatalf("nested notes = %+v", res.Notes)
	}
}

func TestRefineAcceptsPlainTextFallback(t *testing.T) {
	s := New(&fakeGen{out: "A clearer goal."})
	res, err := s.Refine(context.Background(), "project.criteria", "make a clear goal", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "A clearer goal." || len(res.Notes) == 0 {
		t.Fatalf("fallback = %+v", res)
	}
}

func TestRefineMarksIdealTextUnchanged(t *testing.T) {
	text := "Build a tested CSV parser that handles empty rows, reports each failure with the row number, and keeps the original column order."
	gen := &fakeGen{out: `{"text":"` + text + `","notes":["already complete"],"unchanged":true}`}
	res, err := New(gen).Refine(context.Background(), "project.goal", text, "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Unchanged {
		t.Fatalf("result = %+v, want unchanged", res)
	}
	if res.Text != text {
		t.Fatalf("unchanged text = %q, want the original", res.Text)
	}
}

func TestRefineDetectsIdenticalTextWithoutFlag(t *testing.T) {
	text := "Build a tested CSV parser that handles empty rows, reports each failure with the row number, and keeps the original column order."
	res, err := New(&fakeGen{out: `{"text":"` + text + `","unchanged":false}`}).Refine(context.Background(), "project.goal", text, "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Unchanged {
		t.Fatalf("identical text was not marked unchanged: %+v", res)
	}
}

func TestRefineAsksForClarificationWhenEmpty(t *testing.T) {
	gen := &fakeGen{out: `{"text":"should be ignored"}`}
	res, err := New(gen).Refine(context.Background(), "project.goal", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.NeedsClarification || res.Text != "" || len(res.Questions) < 2 {
		t.Fatalf("clarification = %+v", res)
	}
	if gen.called {
		t.Fatal("blank field should not spend a model call")
	}
}

func TestRefineAsksForClarificationWhenVague(t *testing.T) {
	gen := &fakeGen{out: `{"needs_clarification":true}`}
	res, err := New(gen).Refine(context.Background(), "project.goal", "do the thing", "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.NeedsClarification || len(res.Questions) < 2 {
		t.Fatalf("clarification = %+v", res)
	}
}

func TestRefineEmptyModelLeavesUsableTextUnchanged(t *testing.T) {
	const text = "Build a small local todo app with add, complete, and remove flows and a passing test for each."
	res, err := New(&fakeGen{out: `{"text":""}`}).Refine(context.Background(), "project.goal", text, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.NeedsClarification || !res.Unchanged || res.Text != text {
		t.Fatalf("empty model fallback = %+v", res)
	}
}

func TestRefineRetriesOverEagerClarification(t *testing.T) {
	gen := &fakeGen{outs: []string{
		`{"needs_clarification":true,"questions":["What is the outcome?"]}`,
		`{"text":"Build a small todo app with a tested add-and-remove flow.","notes":["made the outcome explicit"]}`,
	}}
	res, err := New(gen).Refine(context.Background(), "project.criteria", "Build a small todo app", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.NeedsClarification || !strings.Contains(res.Text, "tested add-and-remove") {
		t.Fatalf("retry result = %+v", res)
	}
	if !strings.Contains(gen.user, "Do not set needs_clarification") {
		t.Fatalf("retry prompt did not forbid clarification: %q", gen.user)
	}
}

func TestRefineLeavesUsableTextUnchangedWhenRetryStillClarifies(t *testing.T) {
	gen := &fakeGen{outs: []string{
		`{"needs_clarification":true}`,
		`{"needs_clarification":true}`,
	}}
	const text = "Build a small todo app"
	res, err := New(gen).Refine(context.Background(), "project.criteria", text, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.NeedsClarification || !res.Unchanged || res.Text != text {
		t.Fatalf("usable text was replaced by questions: %+v", res)
	}
	if len(res.Notes) == 0 || !strings.Contains(res.Notes[0], "unchanged") {
		t.Fatalf("missing unchanged note: %+v", res)
	}
}

func TestRefineRejectsUnknownField(t *testing.T) {
	s := New(&fakeGen{out: "x"})
	if _, err := s.Refine(context.Background(), "not.a.field", "text", ""); err == nil {
		t.Fatal("unknown field was accepted")
	}
	if _, err := s.Refine(context.Background(), "project.goal", strings.Repeat("x", 20001), ""); err == nil {
		t.Fatal("oversize field was accepted")
	}
}

func TestProofreadModeHasStrictContract(t *testing.T) {
	gen := &fakeGen{out: `{"text":"A clearer goal."}`}
	s := New(gen)
	if _, err := s.Refine(context.Background(), "project.goal", "make a rough goal", "proofread"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gen.user, "Fix grammar, spelling, and punctuation only") {
		t.Fatalf("proofread prompt = %q", gen.user)
	}
}

func TestRefineForcesImprovementWhenFirstPassEchoes(t *testing.T) {
	const text = "Build a small todo app"
	gen := &fakeGen{outs: []string{
		`{"text":"Build a small todo app","unchanged":true}`,
		`{"text":"Build a small local todo app with add, complete, and remove flows, and a passing test for each.","notes":["made the outcome and scope concrete"],"added_assumptions":["the first version is local and single-user"]}`,
	}}
	res, err := New(gen).Refine(context.Background(), "project.criteria", text, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Unchanged || !strings.Contains(res.Text, "add, complete, and remove") {
		t.Fatalf("forced improvement = %+v", res)
	}
	if !strings.Contains(gen.user, "OPERATOR ACTION") {
		t.Fatalf("forced prompt missing operator action: %q", gen.user)
	}
}

func TestRefineContextReachesPrompt(t *testing.T) {
	gen := &fakeGen{out: `{"text":"Build a tested CSV parser for the importer that reports each failing row and keeps the original column order.","notes":["used the repository context"]}`}
	res, err := New(gen).RefineContext(context.Background(), "project.goal", "build a csv parser", "", Context{
		ProjectName:    "importer",
		RepositoryPath: "/Users/me/athanor/repos/importer",
		Runtime:        "Core Pod",
		Version:        "0.0.0-dev",
		Fields:         map[string]string{"criteria": "pytest passes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text == "" {
		t.Fatal("empty result")
	}
	for _, want := range []string{"ATHANOR CONTEXT", "project name: importer", "form field criteria: pytest passes"} {
		if !strings.Contains(gen.user, want) {
			t.Errorf("prompt missing %q: %q", want, gen.user)
		}
	}
	for _, unwanted := range []string{"runtime: Core Pod", "version: 0.0.0-dev"} {
		if strings.Contains(gen.user, unwanted) {
			t.Errorf("layered prompt leaked internal context %q: %q", unwanted, gen.user)
		}
	}
}

func TestRefineBlankExplainsWhyItNeedsInput(t *testing.T) {
	gen := &fakeGen{out: `{"text":"ignored"}`}
	res, err := New(gen).Refine(context.Background(), "project.goal", "   ", "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.NeedsClarification || len(res.Questions) == 0 || len(res.Notes) == 0 {
		t.Fatalf("blank result = %+v", res)
	}
	if gen.called {
		t.Fatal("blank field spent a model call")
	}
}

func TestRefineForcesCosmeticOnlyChange(t *testing.T) {
	const text = "build a todo app"
	gen := &fakeGen{outs: []string{
		`{"text":"Build a todo app.","notes":["capitalized"]}`,
		`{"text":"Build a small todo app with an add-and-complete flow and a passing test.","notes":["made the scope concrete"]}`,
	}}
	res, err := New(gen).Refine(context.Background(), "project.goal", text, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Unchanged || !strings.Contains(res.Text, "add-and-complete") {
		t.Fatalf("cosmetic-only result = %+v", res)
	}
}

func TestRefineRecoversMalformedJSONText(t *testing.T) {
	gen := &fakeGen{out: `{" \t,"text": "Build a todo app.", "notes": ["restructured"]}`}
	res, err := New(gen).Refine(context.Background(), "project.criteria", "build a todo app", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Text, `"text"`) || strings.Contains(res.Text, "{") {
		t.Fatalf("broken JSON leaked into the field: %q", res.Text)
	}
}

func TestRefineRejectsRunawayOutput(t *testing.T) {
	s := New(&fakeGen{out: strings.Repeat("long ", 6000)})
	if _, err := s.Refine(context.Background(), "project.goal", "make a short goal", ""); err == nil {
		t.Fatal("runaway output was accepted")
	}
}

const (
	passOneGoal = "Create daily and hourly global news reports based on information gathered from the internet."
	passTwoGoal = "Search the internet for reliable sources of information and regularly pull new content from them. Use this information to compile daily and hourly news reports."
)

func TestRefinePassTwoAddsOperationalLayer(t *testing.T) {
	gen := &fakeGen{out: `{"text":"` + passTwoGoal + `","notes":["made the method explicit"],"added_assumptions":["pull new content on a regular cadence"]}`}
	res, err := New(gen).RefineContext(context.Background(), "project.goal", passOneGoal, "", Context{
		Pass:             2,
		Origin:           "Daily+Hourly Global News Reports etc",
		PriorAssumptions: []string{"the internet is the input source"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != passTwoGoal {
		t.Fatalf("text = %q, want the operational-method pass", res.Text)
	}
	if res.Pass != 2 || res.MaxPass != 6 {
		t.Fatalf("pass metadata = %+v, want 2 of 6", res)
	}
	for _, want := range []string{
		"PASS: 2 of 6.",
		"MINIMUM PASSES BEFORE IDEALIZED: 2.",
		"SUFFICIENCY",
		"Make the operational method explicit",
		"PRIOR ASSUMPTIONS",
		"the internet is the input source",
	} {
		if !strings.Contains(gen.user, want) {
			t.Errorf("pass 2 prompt missing %q:\n%s", want, gen.user)
		}
	}
}

func TestRefineTelegraphicOriginUsesLayeredPass(t *testing.T) {
	gen := &fakeGen{out: `{"text":"` + passOneGoal + `","notes":["made the outcome and source explicit"],"added_assumptions":["the internet is the input source"]}`}
	res, err := New(gen).RefineContext(context.Background(), "project.goal", "Daily+Hourly Global News Reports etc", "", Context{
		Pass:   1,
		Origin: "Daily+Hourly Global News Reports etc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != passOneGoal || res.Unchanged {
		t.Fatalf("layered pass = %+v, want the first complete sentence", res)
	}
	if len(gen.users) != 1 {
		t.Fatalf("calls = %d, want one layered pass", len(gen.users))
	}
	for _, want := range []string{"PASS: 1 of 6.", "SUFFICIENCY", "Make the outcome, input source, and output form/cadence explicit"} {
		if !strings.Contains(gen.users[0], want) {
			t.Errorf("layered prompt missing %q:\n%s", want, gen.users[0])
		}
	}
}

func TestRefineTelegraphicOriginDoesNotIdealizeOnFirstPass(t *testing.T) {
	const draft = "Daily+Hourly Global News Reports etc"
	gen := &fakeGen{out: `{"text":"` + draft + `","unchanged":true}`}
	_, err := New(gen).RefineContext(context.Background(), "project.goal", draft, "", Context{
		Pass:   1,
		Origin: draft,
	})
	if err == nil {
		t.Fatal("a telegraphic first pass was allowed to become Idealized")
	}
}

func TestRefineTelegraphicOriginCanBeIdealizedAfterMinimumPass(t *testing.T) {
	gen := &fakeGen{outs: []string{
		`{"text":"` + passOneGoal + `","unchanged":true}`,
		`{"text":"` + passOneGoal + `","unchanged":true}`,
	}}
	res, err := New(gen).RefineContext(context.Background(), "project.goal", passOneGoal, "", Context{
		Pass:   2,
		Origin: "Daily+Hourly Global News Reports etc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Unchanged || res.Text != passOneGoal {
		t.Fatalf("ideal result = %+v, want the original unchanged", res)
	}
	if len(res.Notes) == 0 || !strings.Contains(res.Notes[0], "Idealized") {
		t.Fatalf("ideal notes = %+v", res.Notes)
	}
	if res.Pass != 2 || res.MaxPass != 6 {
		t.Fatalf("pass metadata = %+v, want 2 of 6", res)
	}
}

func TestRefineSufficientLongOriginCanBeIdealOnFirstPass(t *testing.T) {
	const sufficient = "Build a local CSV parser that reads two exports, compares rows field by field, and writes a report; the build and tests must pass."
	gen := &fakeGen{outs: []string{
		`{"text":"` + sufficient + `","unchanged":true}`,
		`{"text":"` + sufficient + `","unchanged":true}`,
	}}
	res, err := New(gen).RefineContext(context.Background(), "project.goal", sufficient, "", Context{
		Pass:   1,
		Origin: sufficient,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Unchanged {
		t.Fatalf("sufficient first pass = %+v, want unchanged", res)
	}
	if !strings.Contains(strings.Join(gen.users, "\n"), "MINIMUM PASSES BEFORE IDEALIZED: 1.") {
		t.Fatalf("long origin did not relax the minimum pass:\n%s", strings.Join(gen.users, "\n---\n"))
	}
}

func TestRefineNonLayeredFieldHasNoInferenceContract(t *testing.T) {
	gen := &fakeGen{out: `{"text":"The build must pass cleanly and produce no new warnings.","notes":["made the check observable"]}`}
	res, err := New(gen).RefineContext(context.Background(), "project.criteria", "the build passes cleanly", "", Context{Pass: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.MaxPass != 3 {
		t.Fatalf("max pass = %d, want the non-layered polish cap of 3", res.MaxPass)
	}
	for _, unwanted := range []string{"SUFFICIENCY", "THIS PASS:", "MINIMUM PASSES BEFORE IDEALIZED"} {
		if strings.Contains(gen.user, unwanted) {
			t.Errorf("non-layered prompt contains %q:\n%s", unwanted, gen.user)
		}
	}
}

func TestRefineClampsPassToFieldMaximum(t *testing.T) {
	gen := &fakeGen{out: `{"text":"Build a tested CSV parser that handles empty rows and reports each failure.","notes":["tightened"]}`}
	res, err := New(gen).RefineContext(context.Background(), "project.goal", "Build a CSV parser with tests", "", Context{Pass: 99})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pass != 6 || res.MaxPass != 6 {
		t.Fatalf("pass metadata = %+v, want the field maximum", res)
	}
	if !strings.Contains(gen.user, "PASS: 6 of up to 6.") {
		t.Fatalf("clamped prompt missing pass 6:\n%s", gen.user)
	}
}

func TestRefineRejectsDroppedCadence(t *testing.T) {
	const before = "Compile daily and hourly global news reports from public internet sources into concise summaries."
	gen := &fakeGen{out: `{"text":"Gather news from public internet sources and compile concise summaries.","notes":["added the method"]}`}
	_, err := New(gen).RefineContext(context.Background(), "project.goal", before, "", Context{
		Pass:   2,
		Origin: "Daily+Hourly Global News Reports etc",
	})
	if err == nil {
		t.Fatal("a rewrite that dropped daily/hourly was accepted")
	}
}

func TestLostKeyFacts(t *testing.T) {
	cases := []struct {
		before, after string
		want          bool
	}{
		{"Compile daily reports.", "Compile daily reports from public sources.", false},
		{"Compile daily reports.", "Compile reports from public sources.", true},
		{"Run 3 checks.", "Run three checks.", true},
		{"Compile hourly and daily reports.", "Compile every hour and every day.", true},
		{"Daily+Hourly reports.", "Daily and hourly reports.", false},
		{"No cadence here.", "No cadence here at all.", false},
	}
	for _, c := range cases {
		if got := lostKeyFacts(c.before, c.after); got != c.want {
			t.Errorf("lostKeyFacts(%q, %q) = %v, want %v", c.before, c.after, got, c.want)
		}
	}
}

func TestTelegraphicOrigin(t *testing.T) {
	cases := map[string]bool{
		"":                                     true,
		"Daily+Hourly Global News Reports etc": true,
		"one two three four five six seven eight nine":                               true,
		"one two three four five six seven eight nine ten eleven twelve":             false,
		"Build a tested CSV parser that handles empty rows without losing a column.": false,
		"one two three four five six seven eight nine ten eleven etc":                true,
	}
	for text, want := range cases {
		if got := telegraphicOrigin(text); got != want {
			t.Errorf("telegraphicOrigin(%q) = %v, want %v", text, got, want)
		}
	}
}
