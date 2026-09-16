package gateway

import (
	"strings"
	"testing"
)

// TestExtractSearchHits_Table is the structural corpus for the
// search-results link extractor (ADR-0019 §4): markdown links
// produce absolute http(s) hits, relative links resolve against the
// engine page, non-http targets and garbage lines are dropped,
// duplicates dedupe, and the max cap holds.
func TestExtractSearchHits_Table(t *testing.T) {
	engine := "https://html.duckduckgo.com/html/?q=test"
	md := strings.Join([]string{
		"# Search results",
		"",
		"- [First result](https://a.test/one) — the first snippet.",
		"- [Relative result](/two) trailing text",
		"- [Duplicate](https://a.test/one) dup",
		"[No url]() and [Empty title](https://b.test/x)",
		"mailto:[not a link](mailto:x@y.test)",
		"- [Ftp link](ftp://c.test/f)",
		"- [Fourth](https://d.test/4)",
	}, "\n")

	hits, err := ExtractSearchHits(md, engine, 10)
	if err != nil {
		t.Fatalf("ExtractSearchHits: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("hits = %d (%v), want 3", len(hits), hits)
	}
	if hits[0].Title != "First result" || hits[0].URL != "https://a.test/one" {
		t.Errorf("hit0 = %+v", hits[0])
	}
	if hits[0].Snippet != "the first snippet." {
		t.Errorf("hit0 snippet = %q, want the dash-stripped trailing text", hits[0].Snippet)
	}
	if hits[1].URL != "https://html.duckduckgo.com/two" {
		t.Errorf("hit1 URL = %q, want the relative link resolved against the engine page", hits[1].URL)
	}
	if hits[2].URL != "https://d.test/4" {
		t.Errorf("hit2 = %+v, want the ftp/mailto/empty rows dropped", hits[2])
	}
}

// TestExtractSearchHits_CapAndEmpty prove the max cap and the
// "empty results is valid" contract (ADR-0019 §4: no links surfaced,
// not hidden).
func TestExtractSearchHits_CapAndEmpty(t *testing.T) {
	engine := "https://e.test/s"
	md := "- [a](https://a.test/1)\n- [b](https://b.test/2)\n- [c](https://c.test/3)\n"
	hits, err := ExtractSearchHits(md, engine, 2)
	if err != nil {
		t.Fatalf("ExtractSearchHits: %v", err)
	}
	if len(hits) != 2 || hits[1].URL != "https://b.test/2" {
		t.Errorf("hits = %v, want the first two", hits)
	}
	empty, err := ExtractSearchHits("no links here at all", engine, 10)
	if err != nil {
		t.Fatalf("ExtractSearchHits(no links): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("hits = %v, want empty (valid outcome)", empty)
	}
	if _, err := ExtractSearchHits("", "https://e.test", 0); err == nil {
		t.Error("max=0 returned nil error, want an error")
	}
	if _, err := ExtractSearchHits("", "not a url", 10); err == nil {
		t.Error("bad base URL returned nil error, want an error")
	}
}