package main

import (
	"strings"
	"testing"
	"time"
)

func TestFormatJobEvent(t *testing.T) {
	ev := jobEvent{
		TS:   time.Date(2026, 11, 6, 15, 4, 5, 0, time.UTC),
		Data: `{"event":"llm_call","phase":"planning","prompt_tokens":10}`,
	}
	got := formatJobEvent(ev)
	for _, want := range []string{"15:04:05", "llm_call", "phase=planning", "prompt_tokens=10"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatJobEvent = %q, missing %q", got, want)
		}
	}
	got = formatJobEvent(jobEvent{TS: ev.TS, Category: "jobs", Data: "not-json"})
	if !strings.Contains(got, "not-json") {
		t.Errorf("malformed data = %q, want the raw payload", got)
	}
}
