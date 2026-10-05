package mce

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeRepoFile creates a file (and its parents) under root.
func writeRepoFile(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// buildTestRepo creates a small tree exercising every Discover filter.
func buildTestRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeRepoFile(t, root, "main.go", []byte("package main\n"))
	writeRepoFile(t, root, "pkg/util.py", []byte("def f():\n    return 1\n"))
	writeRepoFile(t, root, "docs/guide.md", []byte("# Guide\n"))
	writeRepoFile(t, root, "notes.txt", []byte("hello\n"))
	writeRepoFile(t, root, "sub/readme.md", []byte("# Sub\n"))
	writeRepoFile(t, root, "ignore.xyz", []byte("unsupported extension\n"))
	writeRepoFile(t, root, "binary.txt", []byte{0x00, 0x01, 0x02, 'x'})
	writeRepoFile(t, root, "big.go", bytes.Repeat([]byte("a"), 2000))
	writeRepoFile(t, root, ".git/config", []byte("vcs\n"))
	writeRepoFile(t, root, "node_modules/x.js", []byte("module.exports = 1\n"))
	writeRepoFile(t, root, "custom/skipme.go", []byte("package skip\n"))
	if err := os.Symlink(filepath.Join(root, "main.go"), filepath.Join(root, "link.go")); err != nil {
		t.Fatalf("symlink file: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "pkg"), filepath.Join(root, "linkdir")); err != nil {
		t.Fatalf("symlink dir: %v", err)
	}
	return root
}

func TestDiscoverFilters(t *testing.T) {
	root := buildTestRepo(t)
	got, err := Discover(root, WalkOptions{ExcludeDirs: []string{"custom"}, MaxSourceBytes: 1000})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	var paths []string
	for _, f := range got {
		paths = append(paths, f.RelPath)
	}
	want := []string{"docs/guide.md", "main.go", "notes.txt", "pkg/util.py", "sub/readme.md"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("Discover paths = %v, want %v", paths, want)
	}
	for _, f := range got {
		if f.Lang == "" {
			t.Errorf("%s: lang is empty", f.RelPath)
		}
		if f.Size <= 0 {
			t.Errorf("%s: size = %d, want > 0", f.RelPath, f.Size)
		}
		if f.MTimeUnix == 0 {
			t.Errorf("%s: mtime = 0", f.RelPath)
		}
	}
}

func TestDiscoverIsDeterministic(t *testing.T) {
	root := buildTestRepo(t)
	a, err := Discover(root, WalkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Discover(root, WalkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Discover not deterministic:\n a=%v\n b=%v", a, b)
	}
}

func TestDiscoverMissingRoot(t *testing.T) {
	if _, err := Discover(filepath.Join(t.TempDir(), "nope"), WalkOptions{}); err == nil {
		t.Fatal("Discover(missing root) = nil, want error")
	}
}

func TestDiscoverNoIndexableFiles(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "data.json", []byte("{}\n"))
	writeRepoFile(t, root, "blob.bin", []byte{0x00})
	got, err := Discover(root, WalkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("Discover = %v, want empty", got)
	}
}
