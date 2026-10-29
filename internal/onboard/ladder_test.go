package onboard

import (
	"strings"
	"testing"
)

func TestDefaultLadderIsWellFormed(t *testing.T) {
	l := DefaultLadder()
	if len(l.Generators) == 0 || len(l.Judges) == 0 {
		t.Fatal("ladder is empty")
	}
	seen := map[string]bool{}
	for _, c := range l.Generators {
		if c.ID == "" || seen[c.ID] {
			t.Errorf("generator case id %q is empty or duplicated", c.ID)
		}
		seen[c.ID] = true
		if c.Check != CheckExact && c.Check != CheckJSON {
			t.Errorf("%s: unknown check %q", c.ID, c.Check)
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
