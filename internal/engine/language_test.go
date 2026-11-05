package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
)

func TestParseLanguageHint(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"LANGUAGE: python", project.LanguagePython},
		{"text\nLANGUAGE: Go\nmore", project.LanguageGo},
		{"language: js", project.LanguageJavaScript},
		{"LANGUAGE: nonsense", ""},
		{"no hint", ""},
	}
	for _, c := range cases {
		if got := parseLanguageHint(c.in); got != c.want {
			t.Errorf("parseLanguageHint(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCodeInstructionNamesLanguage(t *testing.T) {
	for _, tc := range []struct {
		lang string
		want string
	}{
		{project.LanguagePython, "Python source"},
		{project.LanguageGo, "Go source"},
		{project.LanguageRust, "Rust source"},
		{project.LanguageUnknown, "source code"},
	} {
		got := codeInstruction(tc.lang)
		if !strings.Contains(got, "raw "+tc.want) {
			t.Errorf("codeInstruction(%q) = %q, want raw %s", tc.lang, got, tc.want)
		}
		if !strings.Contains(got, "=== FILE:") {
			t.Errorf("codeInstruction(%q) lost the multi-file marker contract", tc.lang)
		}
	}
}

func TestLanguageForUsesRepositoryEvidence(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &Engine{}
	got := e.languageFor(context.Background(), job.Job{ID: "j1"},
		project.Project{RepositoryPath: dir}, project.Task{})
	if got != project.LanguageGo {
		t.Errorf("languageFor(repo go.mod) = %q, want go", got)
	}
}
