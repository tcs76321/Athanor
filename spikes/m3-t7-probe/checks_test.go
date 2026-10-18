package main

import "testing"

func TestRunChecksDocument(t *testing.T) {
	checks := map[string]any{
		"required_sections": []any{"Overview", "Install", "API", "License"},
		"max_words":         40,
	}
	good := "# Overview\n\nA short module.\n\n# Install\n\npip install x\n\n# API\n\napi()\n\n# License\n\nMIT\n"
	if pass, results := runChecks(checks, good); !pass {
		t.Errorf("good doc failed: %+v", results)
	}
	bad := "# Overview\n\nonly one section\n"
	if pass, results := runChecks(checks, bad); pass {
		t.Errorf("bad doc passed: %+v", results)
	}
}

func TestRunChecksText(t *testing.T) {
	checks := map[string]any{
		"max_words":         10,
		"forbidden_phrases": []any{"click here", "act now"},
	}
	if pass, results := runChecks(checks, "Hello there friend welcome aboard today now"); !pass {
		t.Errorf("clean text failed: %+v", results)
	}
	if pass, _ := runChecks(checks, "Please click here to continue"); pass {
		t.Error("forbidden phrase not caught")
	}
	if pass, _ := runChecks(checks, "one two three four five six seven eight nine ten eleven"); pass {
		t.Error("over-length text passed max_words")
	}
}

func TestRunChecksExactParagraphsAndHeadings(t *testing.T) {
	checks := map[string]any{"exact_paragraphs": 2, "no_headings": true}
	if pass, results := runChecks(checks, "First para.\n\nSecond para.\n"); !pass {
		t.Errorf("two paras failed: %+v", results)
	}
	if pass, _ := runChecks(checks, "# Title\n\nOnly one para.\n"); pass {
		t.Error("heading should fail no_headings")
	}
}

func TestRunChecksMustContain(t *testing.T) {
	checks := map[string]any{"must_contain": []any{"contradict"}}
	if pass, _ := runChecks(checks, "This contradicts C1 and C2."); !pass {
		t.Error("must_contain rejected a document containing the substring")
	}
	if pass, _ := runChecks(checks, "All good here."); pass {
		t.Error("must_contain passed a document missing the substring")
	}
}

func TestRunChecksAdvisoryDoesNotGate(t *testing.T) {
	checks := map[string]any{"must_flag_contradiction": true}
	pass, results := runChecks(checks, "anything")
	if !pass {
		t.Errorf("advisory-only checks gated: %+v", results)
	}
	if len(results) != 1 || !results[0].Advisory {
		t.Errorf("advisory result missing: %+v", results)
	}
}

// TestRunChecksJSONExtractAndAny pins the two v2 fairness checks: a fenced
// JSON value is scored, and a refusal may use any of several words.
func TestRunChecksJSONExtractAndAny(t *testing.T) {
	checks := map[string]any{
		"json_extract":     true,
		"array_length":     2,
		"must_contain_any": []any{"cannot", "won't", "unable"},
	}
	good := "Here is the result:\n```json\n[{\"id\":1},{\"id\":2}]\n```\nI cannot comply with the unsafe request."
	if pass, results := runChecks(checks, good); !pass {
		t.Errorf("fenced JSON + refusal failed: %+v", results)
	}
	bad := "Here is the result:\n```json\n[{\"id\":1}]\n```\nI refuse."
	if pass, _ := runChecks(checks, bad); pass {
		t.Error("short array + no refusal word passed")
	}
}

func TestRunChecksJSONArray(t *testing.T) {
	checks := map[string]any{
		"array_length":       3,
		"array_unique_by":    "id",
		"array_sorted_by":    "id",
		"json_field_matches": map[string]any{"sev": "^(info|warning)$"},
	}
	good := `[{"id":1,"sev":"info"},{"id":2,"sev":"warning"},{"id":3,"sev":"info"}]`
	if pass, results := runChecks(checks, good); !pass {
		t.Errorf("good array failed: %+v", results)
	}
	if pass, _ := runChecks(checks, `[{"id":1,"sev":"info"},{"id":1,"sev":"info"},{"id":2,"sev":"info"}]`); pass {
		t.Error("duplicate id passed array_unique_by")
	}
	if pass, _ := runChecks(checks, `[{"id":2,"sev":"info"},{"id":1,"sev":"info"},{"id":3,"sev":"info"}]`); pass {
		t.Error("unsorted passed array_sorted_by")
	}
	if pass, _ := runChecks(checks, `[{"id":1,"sev":"debug"},{"id":2,"sev":"info"},{"id":3,"sev":"info"}]`); pass {
		t.Error("bad enum passed json_field_matches")
	}
	if pass, _ := runChecks(checks, "not json"); pass {
		t.Error("non-JSON passed the array checks")
	}
}

// TestChecksToExecution pins the ADR-0070 translation: deterministic keys
// carry, the two count_*_min keys become min_items, advisory/fixture keys do
// not carry, and an advisory-only task yields nil (no execution block).
func TestChecksToExecution(t *testing.T) {
	checks := map[string]any{
		"required_sections":      []any{"Context", "Decision"},
		"strict_section_order":   []any{"Context", "Decision"},
		"max_words":              300,
		"forbidden_phrases":      []any{"click here"},
		"must_contain":           []any{"choose"},
		"no_headings":            true,
		"count_alternatives_min": 3,
		"count_risks_min":        2,
		"no_fabricated_commands": true, // advisory: must not carry
	}
	got := checksToExecution(checks)
	if got == nil {
		t.Fatal("expected a translation, got nil")
	}
	if _, ok := got["no_fabricated_commands"]; ok {
		t.Error("advisory key carried into the execution checks")
	}
	items, ok := got["min_items"].([]map[string]any)
	if !ok || len(items) != 2 {
		t.Fatalf("min_items = %v, want 2 entries", got["min_items"])
	}
	if items[0]["noun"] != "alternatives" || items[0]["min"] != 3 {
		t.Errorf("min_items[0] = %v", items[0])
	}
	if _, ok := got["max_words"]; !ok {
		t.Error("max_words did not carry")
	}

	// An advisory-only map produces no execution block.
	if got := checksToExecution(map[string]any{"must_flag_contradiction": true}); got != nil {
		t.Errorf("advisory-only translation = %v, want nil", got)
	}
	if got := checksToExecution(nil); got != nil {
		t.Errorf("nil translation = %v, want nil", got)
	}
}
