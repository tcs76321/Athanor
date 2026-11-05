package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectLanguageFromCommand(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
	}{
		{"go test ./...", LanguageGo},
		{"pytest -q", LanguagePython},
		{"python -m pytest", LanguagePython},
		{"cargo test", LanguageRust},
		{"npm test", LanguageJavaScript},
		{"mvn -q test", LanguageJava},
		{"composer test", LanguagePHP},
		{"bundle exec rspec", LanguageRuby},
		{"make test", LanguageShell},
		{"true", LanguageUnknown},
	}
	for _, c := range cases {
		if got := DetectLanguage("", c.cmd); got != c.want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", c.cmd, got, c.want)
		}
	}
}

func TestDetectLanguageFromRepository(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DetectLanguage(dir, ""); got != LanguageGo {
		t.Errorf("go.mod language = %q, want %q", got, LanguageGo)
	}
}

func TestNormalizeLanguageAndCandidateName(t *testing.T) {
	if got := NormalizeLanguage("Golang"); got != LanguageGo {
		t.Errorf("NormalizeLanguage(Golang) = %q, want go", got)
	}
	if got := NormalizeLanguage("nonsense"); got != LanguageUnknown {
		t.Errorf("NormalizeLanguage(nonsense) = %q, want unknown", got)
	}
	for _, l := range []string{LanguagePython, LanguageGo, LanguageJavaScript, LanguageRust} {
		if CandidateFileName(l) == "" || !ValidLanguage(l) {
			t.Errorf("language %q has no candidate file or is not valid", l)
		}
	}
}
