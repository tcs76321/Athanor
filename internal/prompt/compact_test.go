package prompt

import (
	"reflect"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/llm"
)

// TestCompactMessagesDeterministic pins the ADR-0025 §1 request half: identical
// input yields byte-identical messages, so a Temp 0.0 compaction request is
// reproducible.
func TestCompactMessagesDeterministic(t *testing.T) {
	cases := []struct {
		name string
		fn   func(profileType, profileState, sourceLabel, content string) []llm.Message
		sys  string
	}{
		{"deterministic", CompactDeterministicMessages, CompactDeterministicSystemV1},
		{"semantic", CompactSemanticMessages, CompactSemanticSystemV1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.fn("log", "episodic", "job-1", "boom: exit 1\n")
			b := tc.fn("log", "episodic", "job-1", "boom: exit 1\n")
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("not deterministic:\n%+v\n%+v", a, b)
			}
			if len(a) != 2 || a[0].Role != "system" || a[1].Role != "user" {
				t.Fatalf("shape = %+v, want system+user", a)
			}
			if a[0].Content != tc.sys {
				t.Error("system message is not the expected versioned Core prompt")
			}
			for _, want := range []string{"PROFILE: log/episodic", "SOURCE: job-1", "boom: exit 1"} {
				if !strings.Contains(a[1].Content, want) {
					t.Errorf("user message missing %q", want)
				}
			}
		})
	}
}

// TestCompactKindSelectsSystemPrompt pins that the two kinds use distinct,
// versioned Core prompts — a kind must never fall through to the wrong one.
func TestCompactKindSelectsSystemPrompt(t *testing.T) {
	det := CompactDeterministicMessages("log", "episodic", "", "x")
	sem := CompactSemanticMessages("conversation", "archival", "", "x")
	if det[0].Content == sem[0].Content {
		t.Fatal("deterministic and semantic share a system prompt")
	}
	if det[0].Content != CompactDeterministicSystemV1 || sem[0].Content != CompactSemanticSystemV1 {
		t.Fatal("a kind did not select its versioned system prompt")
	}
}

// TestCompactVersionConstants pins the §11.1 naming contract: each *Version
// string names the symbol it versions, and is part of the content-address key.
func TestCompactVersionConstants(t *testing.T) {
	if CompactDeterministicVersion != "compact-deterministic-v1" {
		t.Errorf("CompactDeterministicVersion = %q", CompactDeterministicVersion)
	}
	if CompactSemanticVersion != "compact-semantic-v1" {
		t.Errorf("CompactSemanticVersion = %q", CompactSemanticVersion)
	}
}

// TestCompactMessagesNoSourceLabelOmitsLine guards the optional source header.
func TestCompactMessagesNoSourceLabelOmitsLine(t *testing.T) {
	m := CompactDeterministicMessages("log", "episodic", "", "x")
	if strings.Contains(m[1].Content, "SOURCE:") {
		t.Errorf("empty source label should omit the SOURCE line: %q", m[1].Content)
	}
	if !strings.Contains(m[1].Content, "PROFILE: log/episodic") {
		t.Errorf("profile header missing: %q", m[1].Content)
	}
}
