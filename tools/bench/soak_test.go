package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirBytes(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b"), []byte("123"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := dirBytes(dir); got != 8 {
		t.Errorf("dirBytes = %d, want 8", got)
	}
	if got := dirBytes(filepath.Join(dir, "missing")); got != 0 {
		t.Errorf("dirBytes(missing) = %d, want 0", got)
	}
}

func TestSoakSummaryReportsArtifactsAndScratch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "soak.csv")
	csv := "ts,rss_kb,db_bytes,free_gb,goroutines,artifacts_bytes,scratch_bytes,backups_bytes\n" +
		"2026-10-24T00:00:00Z,1024,2048,10.0,10,2097152,3145728,512\n"
	if err := os.WriteFile(path, []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	got := soakSummary(path)
	for _, want := range []string{"max_artifacts=2.0MB", "max_scratch=3.0MB"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q missing %q", got, want)
		}
	}
}
