package prompt

import (
	"reflect"
	"strings"
	"testing"
)

// TestSummarizeChunkMessagesDeterministic pins the ADR-0021 §7 reproducibility
// contract: identical inputs produce byte-identical messages, so a temperature
// 0.0 summary is stable.
func TestSummarizeChunkMessagesDeterministic(t *testing.T) {
	a := SummarizeChunkMessages("a.go", "go", 1, 10, "package a\n")
	b := SummarizeChunkMessages("a.go", "go", 1, 10, "package a\n")
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("SummarizeChunkMessages is not deterministic:\n%+v\n%+v", a, b)
	}
	if len(a) != 2 {
		t.Fatalf("messages = %d, want 2", len(a))
	}
	if a[0].Role != "system" || a[1].Role != "user" {
		t.Fatalf("roles = %q/%q, want system/user", a[0].Role, a[1].Role)
	}
	if a[0].Content != SummarizeChunkSystemV1 {
		t.Error("system message is not the versioned Core prompt")
	}
	for _, want := range []string{"FILE: a.go", "LANG: go", "LINES: 1-10", "package a"} {
		if !strings.Contains(a[1].Content, want) {
			t.Errorf("user message missing %q", want)
		}
	}
}
