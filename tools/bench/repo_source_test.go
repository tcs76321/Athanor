package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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

// TestInitRepoCopyRootsTheCopy pins the containment fix: a staged fixture copy
// becomes its own git worktree root, so Git-as-undo cannot resolve (and mutate)
// the developer's repo above it.
func TestInitRepoCopyRootsTheCopy(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "input.py"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := initRepoCopy(dir); err != nil {
		t.Fatalf("initRepoCopy: %v", err)
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("rev-parse --show-toplevel: %v", err)
	}
	got := strings.TrimSpace(string(out))
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		want = dir
	}
	if got != want {
		t.Errorf("toplevel = %q, want the copy %q (git resolved an ancestor repo)", got, want)
	}
	// The initial commit makes the worktree clean, so prepareBranch would pass.
	if st, _ := exec.Command("git", "-C", dir, "status", "--porcelain").Output(); strings.TrimSpace(string(st)) != "" {
		t.Errorf("copy not clean after initRepoCopy: %q", st)
	}
}
