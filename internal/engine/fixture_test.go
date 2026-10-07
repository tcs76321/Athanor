package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/toolenvelope"
)

func TestReadFixtureTree(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "messy.py"), "x=1\n")
	mustWrite(t, filepath.Join(root, "pkg", "util.py"), "y=2\n")
	mustWrite(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	mustWrite(t, filepath.Join(root, "bin.dat"), "a\x00b")

	files, err := readFixtureTree(root)
	if err != nil {
		t.Fatalf("readFixtureTree: %v", err)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = f.Content
	}
	if got["messy.py"] != "x=1\n" || got["pkg/util.py"] != "y=2\n" {
		t.Errorf("files = %+v, want messy.py and pkg/util.py", got)
	}
	if _, ok := got[".git/HEAD"]; ok {
		t.Error(".git was not skipped")
	}
	if _, ok := got["bin.dat"]; ok {
		t.Error("binary file was not skipped")
	}
}

func TestReadFixtureTreeRejectsMissing(t *testing.T) {
	if _, err := readFixtureTree(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("missing fixture root accepted, want an error")
	}
}

func TestOverlayFiles(t *testing.T) {
	base := []toolenvelope.File{{Path: "a.py", Content: "old"}, {Path: "b.py", Content: "keep"}}
	over := []toolenvelope.File{{Path: "a.py", Content: "new"}, {Path: "c.py", Content: "added"}}
	got := overlayFiles(base, over)
	want := map[string]string{"a.py": "new", "b.py": "keep", "c.py": "added"}
	if len(got) != 3 {
		t.Fatalf("overlay = %+v, want 3 files", got)
	}
	for _, f := range got {
		if want[f.Path] != f.Content {
			t.Errorf("overlay[%s] = %q, want %q", f.Path, f.Content, want[f.Path])
		}
	}
}

func TestRenderFixtureUsesMarkers(t *testing.T) {
	out := renderFixture([]toolenvelope.File{{Path: "messy.py", Content: "x=1\n"}})
	if !strings.Contains(out, "=== FILE: messy.py ===") || !strings.Contains(out, "x=1") {
		t.Errorf("render = %q, want the marker and content", out)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
