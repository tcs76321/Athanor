package phases

import "testing"

// TestExtractSourceURLs is the extraction corpus (ADR-0019 §7, as amended):
// absolute http(s) URLs are pulled from prose, trailing punctuation is
// stripped, duplicates dedupe, non-http schemes and scheme-less text are
// ignored, and the cap holds.
func TestExtractSearchQueries(t *testing.T) {
	cases := []struct {
		name string
		text string
		max  int
		want []string
	}{
		{"none", "no marker here", 3, nil},
		{"one", "SEARCH QUERIES:\n- local first software", 3,
			[]string{"local first software"}},
		{"two", "intro\nSEARCH QUERIES:\n- alpha\n- beta\n\ntail", 3,
			[]string{"alpha", "beta"}},
		{"cap", "SEARCH QUERIES:\n- a\n- b\n- c\n- d", 2,
			[]string{"a", "b"}},
		{"blank ends", "SEARCH QUERIES:\n- a\n\n- b", 3,
			[]string{"a"}},
		{"case", "search queries:\n- x", 3, []string{"x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractSearchQueries(tc.text, tc.max)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestExtractSourceURLs(t *testing.T) {
	cases := []struct {
		name string
		text string
		max  int
		want []string
	}{
		{"none", "a task with no urls at all", 8, nil},
		{"simple", "see https://docs.test/guide for details", 8,
			[]string{"https://docs.test/guide"}},
		{"trailing punctuation", "read https://docs.test/guide, then act.", 8,
			[]string{"https://docs.test/guide"}},
		{"trailing paren", "(see https://docs.test/guide)", 8,
			[]string{"https://docs.test/guide"}},
		{"dedupe", "https://a.test/x and https://a.test/x again", 8,
			[]string{"https://a.test/x"}},
		{"ftp ignored", "ftp://nope.test/f and https://yes.test/", 8,
			[]string{"https://yes.test/"}},
		{"cap", "https://1.test/ https://2.test/ https://3.test/", 2,
			[]string{"https://1.test/", "https://2.test/"}},
		{"max zero", "https://1.test/", 0, nil},
		{"in angle brackets", "link <https://docs.test/x> here", 8,
			[]string{"https://docs.test/x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractSourceURLs(tc.text, tc.max)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
