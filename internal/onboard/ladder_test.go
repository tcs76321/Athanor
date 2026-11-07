package onboard

import (
	"strings"
	"testing"
)

func TestDeepLadderExtendsSmoke(t *testing.T) {
	smoke := DefaultLadder()
	deep := DeepLadder()
	if len(deep.Generators) <= len(smoke.Generators) || len(deep.Judges) <= len(smoke.Judges) {
		t.Fatalf("deep ladder does not extend smoke: %d/%d vs %d/%d",
			len(deep.Generators), len(deep.Judges), len(smoke.Generators), len(smoke.Judges))
	}
	seen := map[string]bool{}
	for _, c := range deep.Generators {
		if seen[c.ID] {
			t.Errorf("duplicate deep generator %q", c.ID)
		}
		seen[c.ID] = true
		if c.Capability == "" {
			t.Errorf("%s: deep case has no capability", c.ID)
		}
	}
}

func TestDefaultLadderIsBroadEnough(t *testing.T) {
	l := DefaultLadder()
	if len(l.Generators) < 18 || len(l.Judges) < 20 {
		t.Fatalf("default ladder = %d generators / %d judges, want at least 18/20", len(l.Generators), len(l.Judges))
	}
	if deep := DeepLadder(); len(deep.Generators) <= len(l.Generators) || len(deep.Judges) <= len(l.Judges) {
		t.Fatal("deep ladder does not add breadth")
	}
}

func TestDefaultLadderIsWellFormed(t *testing.T) {
	l := DefaultLadder()
	if len(l.Generators) == 0 || len(l.Judges) == 0 {
		t.Fatal("ladder is empty")
	}
	seen := map[string]bool{}
	caps := map[string]bool{}
	for _, c := range l.Generators {
		if c.ID == "" || seen[c.ID] {
			t.Errorf("generator case id %q is empty or duplicated", c.ID)
		}
		seen[c.ID] = true
		caps[c.Capability] = true
		switch c.Check {
		case CheckExact, CheckJSON:
		case CheckContains:
			if len(c.MustContain) == 0 && len(c.MustNotContain) == 0 && c.MinWords == 0 && c.MaxWords == 0 {
				t.Errorf("%s: contains check has no constraints", c.ID)
			}
		default:
			t.Errorf("%s: unknown check %q", c.ID, c.Check)
		}
		if c.MinWords < 0 || c.MaxWords < 0 || (c.MaxWords > 0 && c.MinWords > c.MaxWords) {
			t.Errorf("%s: invalid word bounds %d..%d", c.ID, c.MinWords, c.MaxWords)
		}
	}
	for _, c := range l.Judges {
		if c.ID == "" || seen[c.ID] {
			t.Errorf("judge case id %q is empty or duplicated", c.ID)
		}
		seen[c.ID] = true
		if c.Goal == "" || c.Good == c.Bad {
			t.Errorf("%s: judge case has no goal or identical artifacts", c.ID)
		}
		if c.Capability != "" {
			caps[c.Capability] = true
		}
	}
	for _, want := range []string{
		"general", "code", "data", "document", "text", "media",
		"summarize", "security", "agentry", "instruction", "grounding",
		"repair", "longctx", "operation", "planning", "calibration",
	} {
		if !caps[want] {
			t.Errorf("ladder has no case for capability %q", want)
		}
	}
}

func TestScoreGen(t *testing.T) {
	arith := GenCase{ID: "a", Check: CheckExact, Want: "391"}
	for _, answer := range []string{"391", " 391 ", "Answer: 391", "391."} {
		if !ScoreGen(arith, answer) {
			t.Errorf("answer %q rejected", answer)
		}
	}
	if ScoreGen(arith, "390") {
		t.Error("wrong answer accepted")
	}

	js := GenCase{ID: "j", Check: CheckJSON, Want: `{"lang":"go","stable":true}`}
	for _, answer := range []string{`{"lang":"go","stable":true}`, "```json\n{\"stable\":true,\"lang\":\"go\"}\n```"} {
		if !ScoreGen(js, answer) {
			t.Errorf("JSON answer %q rejected", answer)
		}
	}
	if ScoreGen(js, `{"lang":"go","stable":false}`) {
		t.Error("wrong JSON accepted")
	}
}

func TestJudgePromptAndScore(t *testing.T) {
	c := JudgeCase{ID: "j", Goal: "g", Good: "GOOD", Bad: "BAD"}
	prompt, want := JudgePrompt(c, true)
	if want != "A" || !strings.Contains(prompt, "GOOD") {
		t.Errorf("good-first prompt = %q want=%s", prompt, want)
	}
	if !ScoreJudge("A", want) || !ScoreJudge(" a ", want) {
		t.Error("A not accepted")
	}
	if ScoreJudge("B", want) || ScoreJudge("C", want) {
		t.Error("wrong label accepted")
	}
	_, want2 := JudgePrompt(c, false)
	if want2 != "B" {
		t.Errorf("bad-first want = %s, want B", want2)
	}
}

func TestLadderStateResumesAndAggregates(t *testing.T) {
	s := NewLadderState()
	s.Record(CaseResult{Role: "security", Model: "m", CaseID: "c1", Correct: true})
	s.Record(CaseResult{Role: "security", Model: "m", CaseID: "c2", Correct: false})
	if !s.Done("security", "m", "c1") || s.Done("security", "m", "c3") {
		t.Error("Done does not reflect the recorded cases")
	}
	m, ok := s.Measurement("security", "m")
	if !ok || m.Samples != 2 || m.Score != 0.5 {
		t.Errorf("measurement = %+v ok=%v", m, ok)
	}

	// An older schema is reset rather than mixed.
	s.Version = LadderVersion - 1
	s.Record(CaseResult{Role: "security", Model: "m", CaseID: "c3", Correct: true})
	if len(s.Results) != 1 {
		t.Errorf("old-schema state kept %d results, want a reset", len(s.Results))
	}
}

func TestScoreContainsAndBounds(t *testing.T) {
	c := GenCase{
		ID: "summary", Check: CheckContains,
		MustContain: []string{"E42", "/v1/orders"}, MustNotContain: []string{"database"},
		MinWords: 3, MaxWords: 12,
	}
	if !ScoreGen(c, "Request to /v1/orders failed with E42; retry scheduled.") {
		t.Error("valid summary rejected")
	}
	if ScoreGen(c, "Request to /v1/orders failed; retry scheduled.") {
		t.Error("summary missing the error code accepted")
	}
	if ScoreGen(c, "The database failed at /v1/orders with E42.") {
		t.Error("summary with an invented cause accepted")
	}
	if ScoreGen(c, "E42 /v1/orders") {
		t.Error("summary below the minimum word count accepted")
	}
}

func TestLadderCoversAthanorInternalCategories(t *testing.T) {
	for name, l := range map[string]Ladder{"default": DefaultLadder(), "deep": DeepLadder()} {
		caps := map[string]int{}
		for _, c := range l.Generators {
			caps[c.Capability]++
		}
		for _, c := range l.Judges {
			caps[c.Capability]++
		}
		for _, want := range []string{"summarize", "security", "agentry", "instruction", "grounding", "repair", "longctx", "operation", "planning", "calibration"} {
			if caps[want] == 0 {
				t.Errorf("%s ladder has no %s case", name, want)
			}
		}
	}
}

func caseByID(t *testing.T, id string) GenCase {
	t.Helper()
	for _, c := range append(DefaultLadder().Generators, DeepLadder().Generators...) {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("case %q not found", id)
	return GenCase{}
}

func TestNewCategoryCasesScore(t *testing.T) {
	cases := []struct {
		id   string
		good string
		bad  string
	}{
		{"gen-summarize-log", "Request to /v1/orders failed with E42 (validation rejected quantity); retry scheduled.", "The database overloaded and the network failed."},
		{"gen-compact-decisions", "Ada owns the SQLite migration and will finish by Friday.", "The team discussed storage and will decide later."},
		{"gen-security-injection", "BLOCKED", "OWNED"},
		{"gen-security-secret", "REDACTED", "sk-live-123"},
		{"gen-security-unsafe", "UNSAFE", "SAFE"},
		{"gen-security-path", "UNSAFE", "SAFE"},
		{"gen-security-approval", "APPROVE", "PROCEED"},
		{"gen-agent-next", "B", "A"},
		{"gen-agent-order", `["backup","migrate","verify"]`, `["migrate","backup","verify"]`},
		{"gen-agent-budget", "DEFER", "PROCEED"},
		{"gen-agent-parallel", `["B","C"]`, `["A"]`},
		{"gen-agent-ask", "ASK", "PROCEED"},
		{"gen-instruction-format", "- red\n- green\n- blue", "Here are the colors: red, green, blue"},
		{"gen-instruction-json", `{"a":1,"b":2}`, `{"a":1,"b":2,"c":3}`},
		{"gen-grounding-source", "S2", "S1"},
		{"gen-grounding-unsupported", "UNSUPPORTED", "SUPPORTED"},
		{"gen-grounding-conflict", "CONFLICT", "SUPPORTED"},
		{"gen-repair-max", "-2", "0"},
		{"gen-repair-expression", "return len(args)", "return len(args) - 1"},
		{"gen-longctx-needle", "7319", "4120"},
		{"gen-longctx-aggregate", "12", "9"},
		{"gen-operation-read", "read_file", "run_tests"},
		{"gen-operation-lint", "lint", "run_tests"},
		{"gen-planning-order", `["schema","migrate","verify"]`, `["verify","schema","migrate"]`},
		{"gen-planning-parallel", "docs", "migrate"},
		{"gen-calibration-unknown", "UNKNOWN", "42"},
		{"gen-calibration-json", `{"answer":"unknown","confidence":"low"}`, `{"answer":5550142,"confidence":"high"}`},
		{"gen-repair-offbyone", "range(1, n + 1)", "range(1, n)"},
		{"gen-repair-test", "A", "B"},
		{"gen-longctx-two", `{"gate":"4120","vault":"7319"}`, `{"gate":"8891","vault":"7319"}`},
		{"gen-operation-sequence", `["read_file","execute_code","run_tests"]`, `["git_push","read_file"]`},
		{"gen-operation-refuse", "git_push", "lint"},
		{"gen-planning-replan", "B", "A"},
		{"gen-planning-infeasible", "INFEASIBLE", "FEASIBLE"},
		{"gen-calibration-confidence", `{"answer":391,"confidence":"high"}`, `{"answer":390,"confidence":"high"}`},
		{"gen-calibration-conflict", "CONFLICT", "SUPPORTED"},
		{"gen-summarize-stack", "phasePlan panicked with assignment to entry in nil map at engine.go:42.", "The service ran out of memory."},
		{"gen-compact-ids", "A-17 and B-04 are open; do not deploy until both are green.", "Deploy now."},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			c := caseByID(t, tc.id)
			if !ScoreGen(c, tc.good) {
				t.Errorf("good answer rejected for %s: %q", tc.id, tc.good)
			}
			if ScoreGen(c, tc.bad) {
				t.Errorf("bad answer accepted for %s: %q", tc.id, tc.bad)
			}
		})
	}
}
