package toolenvelope

import (
	"path"
	"strings"
)

// IsHarnessPath reports whether a repository-relative candidate path names part
// of the verification harness rather than implementation source.
//
// A code candidate is staged over a copy of the repository (or a fixture) and
// the task's real test command then runs in that same tree. Without this
// classifier a model could emit its own tests, conftest, or test-runner
// configuration and weaken the check that decides acceptance. The classifier
// is deliberately conservative: when a path is ambiguous, the harness wins and
// the engine ignores the candidate file.
//
// The rules are path-based and language-agnostic; they cover the common Python,
// Go, Node, Rust, JVM, Ruby, PHP, and Make ecosystems. They do not attempt to
// parse arbitrary test commands.
func IsHarnessPath(p string) bool {
	if p == "" {
		return false
	}
	clean := path.Clean(strings.ReplaceAll(p, `\`, "/"))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return false
	}
	for _, part := range strings.Split(clean, "/") {
		switch strings.ToLower(part) {
		case "test", "tests", "spec", "specs", "__tests__":
			return true
		}
	}

	base := strings.ToLower(path.Base(clean))
	switch base {
	case "conftest.py", "pytest.ini", "tox.ini", "noxfile.py", "setup.cfg",
		"setup.py", "pyproject.toml", "requirements.txt", "pipfile",
		"makefile", "gnumakefile", "justfile",
		"package.json", "package-lock.json", "npm-shrinkwrap.json",
		"yarn.lock", "pnpm-lock.yaml",
		"go.mod", "go.sum",
		"cargo.toml", "cargo.lock",
		"pom.xml", "build.gradle", "build.gradle.kts",
		"settings.gradle", "settings.gradle.kts",
		"composer.json", "composer.lock",
		"gemfile", "gemfile.lock",
		"sitecustomize.py", "usercustomize.py":
		return true
	}
	switch {
	case strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py"):
		return true
	case strings.HasSuffix(base, "_test.py"), strings.HasSuffix(base, "_test.go"),
		strings.HasSuffix(base, "_test.rs"):
		return true
	case strings.Contains(base, ".test."), strings.Contains(base, ".spec."):
		return true
	case strings.HasPrefix(base, "conftest") && strings.HasSuffix(base, ".py"):
		return true
	}
	return false
}

// SplitHarnessFiles separates candidate implementation files from files that
// would modify the verification harness. It preserves the original order of
// both slices.
func SplitHarnessFiles(files []File) (implementation, harness []File) {
	for _, f := range files {
		if IsHarnessPath(f.Path) {
			harness = append(harness, f)
			continue
		}
		implementation = append(implementation, f)
	}
	return implementation, harness
}
