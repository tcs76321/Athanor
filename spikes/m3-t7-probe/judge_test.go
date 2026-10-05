package main

import "testing"

func TestParseJudgeContent(t *testing.T) {
	clean, err := parseJudgeContent(`{"score":0.8,"criteria_met":["a","b"],"criteria_missing":[],"notes":"ok"}`)
	if err != nil {
		t.Fatalf("clean: %v", err)
	}
	if clean.Score != 0.8 || len(clean.CriteriaMet) != 2 || clean.Notes != "ok" {
		t.Errorf("clean = %+v", clean)
	}

	// Drift: a string score and a string criteria field, wrapped in prose.
	drift, err := parseJudgeContent("Here is my verdict:\n{\"score\":\"0.7\",\"criteria_met\":\"b\",\"notes\":\"n\"}")
	if err != nil {
		t.Fatalf("drift: %v", err)
	}
	if drift.Score != 0.7 {
		t.Errorf("drift score = %v, want 0.7", drift.Score)
	}
	if len(drift.CriteriaMet) != 1 || drift.CriteriaMet[0] != "b" {
		t.Errorf("drift criteria = %v, want [b]", drift.CriteriaMet)
	}

	if _, err := parseJudgeContent("no JSON here"); err == nil {
		t.Error("expected an error for a response with no JSON object")
	}
}

func TestFirstJSONObject(t *testing.T) {
	obj, ok := firstJSONObject(`pre {"a":1} post`)
	if !ok || obj != `{"a":1}` {
		t.Errorf("got %q ok=%v, want {\"a\":1}", obj, ok)
	}
	// An embedded close brace inside a string must not terminate early.
	obj, ok = firstJSONObject(`{"a":"}"}`)
	if !ok || obj != `{"a":"}"}` {
		t.Errorf("embedded brace: got %q ok=%v", obj, ok)
	}
	if _, ok := firstJSONObject("no object"); ok {
		t.Error("expected ok=false for a string with no brace")
	}
}

func TestJudgeSeedDeterministicAndVaries(t *testing.T) {
	base := judgeSeed("PKT-0001", "gemma4:12b-mlx")
	if base < 0 {
		t.Errorf("judgeSeed returned %d, want non-negative", base)
	}
	if judgeSeed("PKT-0001", "gemma4:12b-mlx") != base {
		t.Error("judgeSeed is not deterministic")
	}
	if judgeSeed("PKT-0001", "granite4.2:3b") == base {
		t.Error("judgeSeed did not vary by judge")
	}
	if judgeSeed("PKT-0002", "gemma4:12b-mlx") == base {
		t.Error("judgeSeed did not vary by packet")
	}
}
