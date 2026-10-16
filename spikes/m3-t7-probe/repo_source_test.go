package main

import "testing"

// TestRepoSourceFor pins the M9 fixture-injection rule: a non-code fixture is
// indexed as the project repository (so the "provided source" reaches the
// model), a code fixture is not (it is staged into the pod via fixture_path),
// and an explicit repo always wins.
func TestRepoSourceFor(t *testing.T) {
	cases := []struct {
		name string
		g    sampleGoal
		want string
	}{
		{"explicit repo wins", sampleGoal{Repo: "r", Fixture: "f"}, "r"},
		{"document fixture indexed", sampleGoal{Archetype: "document", Fixture: "f"}, "f"},
		{"text fixture indexed", sampleGoal{Archetype: "text", Fixture: "f"}, "f"},
		{"data fixture indexed", sampleGoal{Archetype: "data", Fixture: "f"}, "f"},
		{"code fixture not indexed", sampleGoal{Archetype: "code", Fixture: "f"}, ""},
		{"no source", sampleGoal{Archetype: "text"}, ""},
	}
	for _, c := range cases {
		if got := repoSourceFor(c.g); got != c.want {
			t.Errorf("%s: repoSourceFor = %q, want %q", c.name, got, c.want)
		}
	}
}
