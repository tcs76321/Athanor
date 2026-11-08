package phases

import (
	"strings"
	"testing"
)

func TestBoundedTestOutput(t *testing.T) {
	// stderr is preferred; a long tail is kept (tracebacks end with the error).
	got := boundedTestOutput("stdout noise", "Traceback\nValueError: bad")
	if !strings.Contains(got, "ValueError: bad") {
		t.Errorf("stderr not captured: %q", got)
	}
	if got := boundedTestOutput("only stdout", ""); got != "only stdout" {
		t.Errorf("stdout fallback = %q", got)
	}
	long := strings.Repeat("x", maxTestOutputChars+500)
	got = boundedTestOutput("", long)
	if len([]rune(got)) > maxTestOutputChars+1 { // +1 for the leading ellipsis
		t.Errorf("output not bounded: %d runes", len([]rune(got)))
	}
	if !strings.HasPrefix(got, "…") {
		t.Errorf("bounded output should be marked as truncated: %q", got)
	}
}
