package project

import (
	"os"
	"path/filepath"
	"strings"
)

// Languages are the closed names the engine uses when it asks a model for
// source. They are deliberately broader than the one language the current Job
// Pod can execute directly: the repository's real test command is the
// verifier, and a language-aware prompt is what makes a Go/Rust/JS repository
// ask for Go/Rust/JS source instead of Python.
const (
	LanguagePython     = "python"
	LanguageGo         = "go"
	LanguageJavaScript = "javascript"
	LanguageTypeScript = "typescript"
	LanguageRust       = "rust"
	LanguageJava       = "java"
	LanguageRuby       = "ruby"
	LanguagePHP        = "php"
	LanguageShell      = "shell"
	LanguageUnknown    = "unknown"
)

var knownLanguages = map[string]bool{
	LanguagePython: true, LanguageGo: true, LanguageJavaScript: true,
	LanguageTypeScript: true, LanguageRust: true, LanguageJava: true,
	LanguageRuby: true, LanguagePHP: true, LanguageShell: true,
}

// ValidLanguage reports whether name is in the closed prompt-language set.
func ValidLanguage(name string) bool { return knownLanguages[strings.ToLower(name)] }

// NormalizeLanguage maps common aliases to the canonical prompt name.
func NormalizeLanguage(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "py", "python", "python3":
		return LanguagePython
	case "go", "golang":
		return LanguageGo
	case "js", "javascript", "node", "nodejs":
		return LanguageJavaScript
	case "ts", "typescript":
		return LanguageTypeScript
	case "rs", "rust":
		return LanguageRust
	case "java":
		return LanguageJava
	case "rb", "ruby":
		return LanguageRuby
	case "php":
		return LanguagePHP
	case "sh", "bash", "shell":
		return LanguageShell
	default:
		return LanguageUnknown
	}
}

// DetectLanguage infers the primary source language from a repository and its
// test command. The command is a strong signal because it is the same evidence
// the verifier uses; manifests are the fallback. The function reads only small
// known files and never executes anything. Unknown is a valid answer: the
// prompt then stays generic rather than guessing Python.
func DetectLanguage(repoPath, testCommand string) string {
	if l := languageFromCommand(testCommand); l != LanguageUnknown {
		return l
	}
	if repoPath == "" {
		return LanguageUnknown
	}
	has := func(name string) bool {
		_, err := os.Stat(filepath.Join(repoPath, name))
		return err == nil
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(repoPath, name))
		if err != nil || len(b) > 1<<20 {
			return ""
		}
		return string(b)
	}
	switch {
	case has("go.mod"):
		return LanguageGo
	case has("Cargo.toml"):
		return LanguageRust
	case has("pom.xml") || has("build.gradle") || has("build.gradle.kts"):
		return LanguageJava
	case has("composer.json"):
		return LanguagePHP
	case has("Gemfile"):
		return LanguageRuby
	}
	if pkg := read("package.json"); pkg != "" {
		if strings.Contains(pkg, `"typescript"`) || has("tsconfig.json") {
			return LanguageTypeScript
		}
		return LanguageJavaScript
	}
	if py := read("pyproject.toml"); strings.Contains(py, "[tool.pytest") ||
		strings.Contains(py, "[project]") || strings.Contains(py, "[tool.poetry]") {
		return LanguagePython
	}
	if has("pytest.ini") || has("tox.ini") || has("setup.py") || has("setup.cfg") {
		return LanguagePython
	}
	if has(filepath.Join("tests", "test_main.py")) || has("test_main.py") {
		return LanguagePython
	}
	if has("Makefile") {
		return LanguageShell
	}
	return LanguageUnknown
}

func languageFromCommand(cmd string) string {
	c := strings.ToLower(cmd)
	switch {
	case strings.Contains(c, "cargo test") || strings.Contains(c, "cargo build"):
		return LanguageRust
	case strings.Contains(c, "go test") || strings.Contains(c, "go build"):
		return LanguageGo
	case strings.Contains(c, "pytest") || strings.Contains(c, "python -m unittest") ||
		strings.Contains(c, "python -m pytest"):
		return LanguagePython
	case strings.Contains(c, "npm test") || strings.Contains(c, "yarn test") ||
		strings.Contains(c, "pnpm test") || strings.Contains(c, "jest"):
		return LanguageJavaScript
	case strings.Contains(c, "mvn ") || strings.Contains(c, "gradle "):
		return LanguageJava
	case strings.Contains(c, "composer test"):
		return LanguagePHP
	case strings.Contains(c, "bundle exec rspec") || strings.Contains(c, "rake test"):
		return LanguageRuby
	default:
		return LanguageUnknown
	}
}

// CandidateFileName returns the conventional single-file candidate name for a
// language. Multi-file candidates keep the paths the model emitted; this is
// only for the single-file shorthand.
func CandidateFileName(language string) string {
	switch language {
	case LanguagePython:
		return "solution.py"
	case LanguageGo:
		return "solution.go"
	case LanguageJavaScript:
		return "solution.js"
	case LanguageTypeScript:
		return "solution.ts"
	case LanguageRust:
		return "solution.rs"
	case LanguageJava:
		return "Solution.java"
	case LanguageRuby:
		return "solution.rb"
	case LanguagePHP:
		return "solution.php"
	case LanguageShell:
		return "solution.sh"
	default:
		return "solution.txt"
	}
}
