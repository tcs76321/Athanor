// Package main is the M1-T8 quality probe helper. It runs the five sample
// goals through the live daemon, computes per-phase timing and token totals
// from the EventLog, and writes per-sample results to a markdown table.
//
// Pure aggregation functions are unit-tested here; HTTP plumbing is
// exercised end-to-end when the probe runs.
package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseTransitionSequence(t *testing.T) {
	events := []transitionEvent{
		{DataJSON: `{"event":"transition","from":"queued","to":"context_building"}`},
		{DataJSON: `{"event":"transition","from":"context_building","to":"planning"}`},
		{DataJSON: `{"event":"llm_call","phase":"planning"}`},
		{DataJSON: `{"event":"transition","from":"planning","to":"diverging"}`},
		{DataJSON: `{"event":"transition","from":"diverging","to":"synthesizing"}`},
		{DataJSON: `{"event":"transition","from":"synthesizing","to":"comparing"}`},
		{DataJSON: `{"event":"transition","from":"comparing","to":"completed"}`},
	}
	got := parseTransitionSequence(events)
	want := []string{
		"context_building", "planning", "diverging",
		"synthesizing", "comparing", "completed",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("parseTransitionSequence = %v, want %v", got, want)
	}
}

func TestParseTransitionSequenceEmptyOnNoTransitions(t *testing.T) {
	events := []transitionEvent{
		{DataJSON: `{"event":"llm_call","phase":"planning"}`},
		{DataJSON: `{"event":"comparison","winner":"new"}`},
	}
	got := parseTransitionSequence(events)
	if len(got) != 0 {
		t.Errorf("got %d transitions, want 0", len(got))
	}
}

func TestComputePhaseDurations(t *testing.T) {
	// Each phase's duration = (next transition's ts) - (this transition's ts).
	// The "completed" phase is the final state with no successor, so it
	// is not measured (this matches the M1 engine, where "completed" is
	// not a phase but a terminal state).
	//   t=0  created (no phase)
	//   t=1  context_building  -> out at 4, duration = 3s
	//   t=4  planning          -> out at 11, duration = 7s
	//   t=11 diverging         -> out at 23, duration = 12s
	//   t=23 synthesizing      -> out at 58, duration = 35s
	//   t=58 comparing         -> out at 59, duration = 1s
	//   t=59 completed         -> terminal, no duration
	events := []transitionEvent{
		{TS: time.Unix(0, 0), DataJSON: `{"event":"created"}`},
		{TS: time.Unix(1, 0), DataJSON: `{"event":"transition","from":"queued","to":"context_building"}`},
		{TS: time.Unix(4, 0), DataJSON: `{"event":"transition","from":"context_building","to":"planning"}`},
		{TS: time.Unix(11, 0), DataJSON: `{"event":"transition","from":"planning","to":"diverging"}`},
		{TS: time.Unix(23, 0), DataJSON: `{"event":"transition","from":"diverging","to":"synthesizing"}`},
		{TS: time.Unix(58, 0), DataJSON: `{"event":"transition","from":"synthesizing","to":"comparing"}`},
		{TS: time.Unix(59, 0), DataJSON: `{"event":"transition","from":"comparing","to":"completed"}`},
	}
	got := computePhaseDurations(events)
	cases := []struct {
		phase string
		want  time.Duration
	}{
		{"context_building", 3 * time.Second},
		{"planning", 7 * time.Second},
		{"diverging", 12 * time.Second},
		{"synthesizing", 35 * time.Second},
		{"comparing", 1 * time.Second},
	}
	for _, c := range cases {
		if got[c.phase] != c.want {
			t.Errorf("phase %q duration = %v, want %v", c.phase, got[c.phase], c.want)
		}
	}
	if _, ok := got["completed"]; ok {
		t.Errorf("expected no 'completed' entry, got %v", got["completed"])
	}
	if _, ok := got["queued"]; ok {
		t.Errorf("expected no 'queued' entry, got %v", got["queued"])
	}
}

func TestComputePhaseDurationsEmpty(t *testing.T) {
	got := computePhaseDurations(nil)
	if len(got) != 0 {
		t.Errorf("got %d entries, want 0", len(got))
	}
}

func TestSummarizeLLMCalls(t *testing.T) {
	events := []transitionEvent{
		{DataJSON: `{"event":"transition","from":"queued","to":"context_building"}`},
		{DataJSON: `{"event":"llm_call","phase":"planning","persona":"tall","model":"qwen3.8:27b-mlx","prompt_tokens":100,"completion_tokens":50}`},
		{DataJSON: `{"event":"llm_call","phase":"diverging","persona":"main","model":"gemma4:12b-mlx","prompt_tokens":500,"completion_tokens":300}`},
		{DataJSON: `{"event":"llm_call","phase":"synthesizing","persona":"main","model":"gemma4:12b-mlx","prompt_tokens":800,"completion_tokens":400}`},
		{DataJSON: `{"event":"transition","from":"synthesizing","to":"completed"}`},
	}
	got := summarizeLLMCalls(events)
	if got.TotalCalls != 3 {
		t.Errorf("TotalCalls = %d, want 3", got.TotalCalls)
	}
	if got.TotalPromptTok != 1400 {
		t.Errorf("TotalPromptTok = %d, want 1400", got.TotalPromptTok)
	}
	if got.TotalCompletionTok != 750 {
		t.Errorf("TotalCompletionTok = %d, want 750", got.TotalCompletionTok)
	}
	if got.PerPhase["planning"].Calls != 1 || got.PerPhase["planning"].PromptTok != 100 {
		t.Errorf("planning phase summary = %+v, want 1 call / 100 pt", got.PerPhase["planning"])
	}
}

func TestSummarizeLLMCallsEmpty(t *testing.T) {
	got := summarizeLLMCalls([]transitionEvent{
		{DataJSON: `{"event":"transition","from":"queued","to":"context_building"}`},
	})
	if got.TotalCalls != 0 {
		t.Errorf("TotalCalls = %d, want 0", got.TotalCalls)
	}
	if len(got.PerPhase) != 0 {
		t.Errorf("PerPhase = %+v, want empty", got.PerPhase)
	}
}

func TestDetectContextFloorViolation(t *testing.T) {
	v, rec := detectContextFloorViolation([]transitionEvent{
		{DataJSON: `{"event":"transition","from":"queued","to":"context_building"}`},
		{DataJSON: `{"event":"context_floor_violation","phase":"diverging","recommendation":"raise main to 16384"}`},
		{DataJSON: `{"event":"transition","to":"paused"}`},
	})
	if !v {
		t.Error("expected violation to be detected")
	}
	if rec != "raise main to 16384" {
		t.Errorf("recommendation = %q, want %q", rec, "raise main to 16384")
	}
}

func TestDetectContextFloorViolationNone(t *testing.T) {
	v, _ := detectContextFloorViolation([]transitionEvent{
		{DataJSON: `{"event":"transition","to":"completed"}`},
	})
	if v {
		t.Error("expected no violation")
	}
}

func TestRenderMarkdownRow(t *testing.T) {
	got := renderMarkdownRow(Result{
		Number:      1,
		Archetype:   "text",
		Goal:        "Write a short essay about why local-first software matters.",
		Criteria:    []string{"at least three arguments", "a conclusion"},
		JobID:       "abc123def456",
		JobWallTime: 90 * time.Second,
		PhaseDur: map[string]time.Duration{
			"planning":     20 * time.Second,
			"diverging":    35 * time.Second,
			"synthesizing": 30 * time.Second,
			"comparing":    5 * time.Second,
		},
		TotalCalls: 3, TotalPrompt: 1500, TotalCompl: 800,
		ArtifactBytes: 1234,
		Adherence:     "partial",
		Usefulness:    "4",
		Notes:         "two of three arguments, conclusion present",
	})
	want := "1 | text | Write a short essay about why local-first softw... | at least three arguments; a conclusion | abc12... | 90.0s | 20.0s | 35.0s | 30.0s | 5.0s | 3 | 1500 | 800 | 1234 | partial | 4 | two of three arguments, conclusion present"
	if got != want {
		t.Errorf("renderMarkdownRow mismatch:\n got:  %s\n want: %s", got, want)
	}
}

func TestTrunc(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "he..."},
		{"hello", 2, "he"},
		{"", 5, ""},
	}
	for _, c := range cases {
		if got := trunc(c.in, c.n); got != c.want {
			t.Errorf("trunc(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestFtoa1(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0.0"},
		{1.0, "1.0"},
		{1.5, "1.5"},
		{42.07, "42.0"},
		{0.35, "0.3"},
	}
	for _, c := range cases {
		if got := ftoa1(c.in); got != c.want {
			t.Errorf("ftoa1(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
