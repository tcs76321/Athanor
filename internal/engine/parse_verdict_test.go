// M3-T2/ADR-0012 follow-up tests for the
// `parseVerdictJSON[T]` generic helper that
// consolidates the two near-clone brace scanners
// in `evaluate.go` and `compare.go`.
//
// The corpus is the five cases in ADR-0012 §D3
// plus the M3-T1-era per-phase round-trip tests
// (already in `compare_test.go` and the evaluate
// test file). These tests pin the helper's
// behavior independently of either call site.
package engine

import (
	"errors"
	"strings"
	"testing"
)

// minimalVerdict is a one-field struct used to
// exercise the generic helper without coupling to
// the evalVerdict or comparisonVerdict types.
type minimalVerdict struct {
	Winner string `json:"winner"`
}

func TestParseVerdictJSON_HappyPath(t *testing.T) {
	v, err := parseVerdictJSON[minimalVerdict](`{"winner":"new"}`)
	if err != nil {
		t.Fatalf("parseVerdictJSON: %v", err)
	}
	if v.Winner != "new" {
		t.Errorf("Winner = %q, want new", v.Winner)
	}
}

// TestParseVerdictJSON_LenientWrapping covers
// ADR-0012 §D3 row 1: the JSON may be inside a
// code fence or preceded by prose.
func TestParseVerdictJSON_LenientWrapping(t *testing.T) {
	cases := []string{
		`Here's the verdict: {"winner":"previous"}`,
		"```json\n{\"winner\":\"none\"}\n```",
		"Some prose before\n{\"winner\":\"new\"}\nsome prose after",
	}
	for _, c := range cases {
		v, err := parseVerdictJSON[minimalVerdict](c)
		if err != nil {
			t.Errorf("parseVerdictJSON(%q): %v", c, err)
			continue
		}
		if v.Winner == "" {
			t.Errorf("parseVerdictJSON(%q): Winner is empty", c)
		}
	}
}

// TestParseVerdictJSON_NoJSON covers ADR-0012
// §D3 row 2: a response with no JSON object at
// all is a hard error.
func TestParseVerdictJSON_NoJSON(t *testing.T) {
	_, err := parseVerdictJSON[minimalVerdict]("no JSON here, just prose")
	if err == nil {
		t.Fatal("parseVerdictJSON: no error for no-JSON input")
	}
	var vpe *VerdictParseError
	if !errors.As(err, &vpe) {
		t.Errorf("error = %v, want errors.As *VerdictParseError", err)
	}
}

// TestParseVerdictJSON_Unterminated covers the
// "open brace but no close" failure mode.
func TestParseVerdictJSON_Unterminated(t *testing.T) {
	_, err := parseVerdictJSON[minimalVerdict](`{"winner":"new"`)
	if err == nil {
		t.Fatal("parseVerdictJSON: no error for unterminated JSON")
	}
	var vpe *VerdictParseError
	if !errors.As(err, &vpe) {
		t.Errorf("error = %v, want errors.As *VerdictParseError", err)
	}
}

// TestParseVerdictJSON_EmbeddedBraceInString
// covers the §D3 row 4 corpus: an embedded '}'
// inside a string must not fool the depth counter.
func TestParseVerdictJSON_EmbeddedBraceInString(t *testing.T) {
	v, err := parseVerdictJSON[minimalVerdict](`{"winner":"contains}brace"}`)
	if err != nil {
		t.Fatalf("parseVerdictJSON: %v", err)
	}
	if v.Winner != "contains}brace" {
		t.Errorf("Winner = %q, want %q", v.Winner, "contains}brace")
	}
}

// TestParseVerdictJSON_Generics: the helper
// works for both evalVerdict and comparisonVerdict
// (the two real call sites). This test pins the
// generic contract; if a future contributor
// changes the helper to be type-specific, this
// test fails.
func TestParseVerdictJSON_Generics(t *testing.T) {
	// evalVerdict: includes a numeric field.
	ev, err := parseVerdictJSON[evalVerdict](`{"passed":true,"score":0.85,"summary":"ok"}`)
	if err != nil {
		t.Fatalf("parseVerdictJSON[evalVerdict]: %v", err)
	}
	if !ev.Passed || ev.Score != 0.85 || ev.Summary != "ok" {
		t.Errorf("evalVerdict = %+v, want passed=true score=0.85 summary=ok", ev)
	}
	// comparisonVerdict: includes a string field.
	cv, err := parseVerdictJSON[comparisonVerdict](`{"winner":"new","confidence":0.9,"reasons":["ok"],"missing_requirements":[]}`)
	if err != nil {
		t.Fatalf("parseVerdictJSON[comparisonVerdict]: %v", err)
	}
	if cv.Winner != "new" || cv.Confidence != 0.9 {
		t.Errorf("comparisonVerdict = %+v, want winner=new confidence=0.9", cv)
	}
}

// TestParseVerdictJSONCoerced_ConfidenceAsString reproduces the first M3-T7
// smoke failure: the judge emitted a string where the struct wants a
// float64. The coercion must recover the value and report the field.
func TestParseVerdictJSONCoerced_ConfidenceAsString(t *testing.T) {
	v, notes, err := parseVerdictJSONCoerced[evalVerdict](
		`{"passed":true,"score":0.9,"confidence":"0.85","summary":"ok"}`)
	if err != nil {
		t.Fatalf("parseVerdictJSONCoerced: %v", err)
	}
	if v.Confidence != 0.85 {
		t.Errorf("Confidence = %v, want 0.85", v.Confidence)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "confidence") {
		t.Errorf("notes = %v, want one naming confidence", notes)
	}
}

// TestParseVerdictJSONCoerced_StyleIssuesAsString reproduces the second
// smoke failure: a string where the struct wants a []string.
func TestParseVerdictJSONCoerced_StyleIssuesAsString(t *testing.T) {
	v, notes, err := parseVerdictJSONCoerced[evalVerdict](
		`{"style_issues":"avoid passive voice","missing_criteria":[]}`)
	if err != nil {
		t.Fatalf("parseVerdictJSONCoerced: %v", err)
	}
	if len(v.StyleIssues) != 1 || v.StyleIssues[0] != "avoid passive voice" {
		t.Errorf("StyleIssues = %v, want one element", v.StyleIssues)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "style_issues") {
		t.Errorf("notes = %v, want one naming style_issues", notes)
	}
}

// TestParseVerdictJSONCoerced_ConformingHasNoNotes: a schema-conforming
// verdict yields zero coercions, so the audit row is not emitted.
func TestParseVerdictJSONCoerced_ConformingHasNoNotes(t *testing.T) {
	_, notes, err := parseVerdictJSONCoerced[evalVerdict](
		`{"passed":true,"score":0.9,"failed_tests":[],"missing_criteria":[],"security_issues":[],"style_issues":[],"better_than_previous":false,"confidence":0.9,"summary":"ok"}`)
	if err != nil {
		t.Fatalf("parseVerdictJSONCoerced: %v", err)
	}
	if len(notes) != 0 {
		t.Errorf("notes = %v, want none for a conforming verdict", notes)
	}
}

// TestParseVerdictJSONCoerced_UnrecoverableStillErrors: a type that cannot
// be coerced (an object where a string is wanted) is still a hard error,
// so coercion does not silently swallow garbage.
func TestParseVerdictJSONCoerced_UnrecoverableStillErrors(t *testing.T) {
	_, _, err := parseVerdictJSONCoerced[minimalVerdict](`{"winner":{"nested":true}}`)
	if err == nil {
		t.Fatal("expected an error for an uncoercible field")
	}
}
