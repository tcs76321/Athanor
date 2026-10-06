package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseAnchorCases(t *testing.T) {
	cases, err := parseAnchorCases(filepath.Join("..", "..", "eval", "anchor", "cases.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 16 {
		t.Fatalf("cases = %d, want 16", len(cases))
	}
	for _, c := range cases {
		if c.ID == "" || c.Goal == "" || c.Criteria == "" || c.Artifact == "" {
			t.Errorf("incomplete case: %+v", c)
		}
	}
}

func TestParseAnchorRatings(t *testing.T) {
	ratings, err := parseAnchorRatings(filepath.Join("..", "..", "eval", "anchor", "ratings.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ratings) != 16 {
		t.Fatalf("ratings = %d, want 16", len(ratings))
	}
	if ratings["sa-s1"] != 5 {
		t.Errorf("sa-s1 = %v, want 5", ratings["sa-s1"])
	}
	if ratings["code-fib-fail"] != 1 {
		t.Errorf("code-fib-fail = %v, want 1 (objective label)", ratings["code-fib-fail"])
	}
}

func TestParseAnchorRatingsHumanOverridesAgent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ratings.csv")
	content := "case_id,rater,rating,criteria_met,notes\na,agent,1,N,provisional\na,alice,4,Y,human\nb,agent,2,Y,provisional\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ratings, err := parseAnchorRatings(path)
	if err != nil {
		t.Fatal(err)
	}
	if ratings["a"] != 4 {
		t.Errorf("a = %v, want 4 (human overrides agent)", ratings["a"])
	}
	if ratings["b"] != 2 {
		t.Errorf("b = %v, want 2 (agent fallback)", ratings["b"])
	}
}
