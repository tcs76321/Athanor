package ui

import (
	"strings"
	"testing"
)

func TestHighlightMarksAndEscapes(t *testing.T) {
	got := string(highlightCode("sample.py", "def f():\n    # note\n    return \"<script>\"\n"))
	for _, want := range []string{`class="tok-kw"`, `class="tok-com"`, `class="tok-str"`, "&lt;script&gt;"} {
		if !strings.Contains(got, want) {
			t.Errorf("highlight missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "<script>") {
		t.Error("highlight emitted unescaped markup")
	}
}

func TestLineNumbers(t *testing.T) {
	if got := lineNumbers("a\nb\n"); got != "1\n2" {
		t.Errorf("lineNumbers = %q, want 1\\n2", got)
	}
	if got := lineNumbers("one line"); got != "1" {
		t.Errorf("lineNumbers = %q, want 1", got)
	}
}

func TestHighlightUnknownLanguageIsPlain(t *testing.T) {
	got := string(highlightCode("notes.unknown", "plain <text>"))
	if strings.Contains(got, "<text>") {
		t.Error("plain text was not escaped")
	}
	if strings.Contains(got, "tok-kw") {
		t.Error("unknown language should not highlight keywords")
	}
}

func TestHighlightBlockComment(t *testing.T) {
	got := string(highlightCode("x.go", "/* start\nstill */\nfunc main() {}\n"))
	if strings.Count(got, `class="tok-com"`) != 2 {
		t.Errorf("block comment not marked across lines: %q", got)
	}
	if !strings.Contains(got, `class="tok-kw"`) {
		t.Error("keyword after a block comment was not highlighted")
	}
}
