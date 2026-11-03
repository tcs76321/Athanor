package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Capabilities are the kinds of work a project can produce (ADR-0093). They
// replace the single operator-chosen archetype. The strings are the same as
// the legacy archetype values so old rows, artifacts, and execution config
// remain readable.
const (
	CapabilityCode     = "code"
	CapabilityData     = "data"
	CapabilityDocument = "document"
	CapabilityText     = "text"
	CapabilityMedia    = "media"
)

// capabilityOrder is the deterministic display/primary order.
var capabilityOrder = []string{
	CapabilityCode, CapabilityData, CapabilityDocument, CapabilityText, CapabilityMedia,
}

// ValidCapability reports whether c is one of the closed capability set.
func ValidCapability(c string) bool {
	switch c {
	case CapabilityCode, CapabilityData, CapabilityDocument, CapabilityText, CapabilityMedia:
		return true
	default:
		return false
	}
}

// CapabilitiesFromArchetype is the migration/compatibility bridge for the
// legacy project archetype. New code must use InferCapabilities instead.
func CapabilitiesFromArchetype(archetype string) []string {
	if !ValidCapability(archetype) {
		return []string{CapabilityDocument}
	}
	return []string{archetype}
}

// PrimaryCapability returns the canonical primary capability from a set. An
// empty set means document (prose output), the fail-safe default.
func PrimaryCapability(caps []string) string {
	seen := make(map[string]bool, len(caps))
	for _, c := range caps {
		if ValidCapability(c) {
			seen[c] = true
		}
	}
	for _, c := range capabilityOrder {
		if seen[c] {
			return c
		}
	}
	return CapabilityDocument
}

// NormalizeCapabilities deduplicates and orders a set. Invalid entries are
// ignored; an empty result becomes [document].
func NormalizeCapabilities(caps []string) []string {
	seen := make(map[string]bool, len(caps))
	for _, c := range caps {
		if ValidCapability(c) {
			seen[c] = true
		}
	}
	out := make([]string, 0, len(seen))
	for _, c := range capabilityOrder {
		if seen[c] {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		out = append(out, CapabilityDocument)
	}
	return out
}

// InferCapabilities derives a project's capability set from its goal and
// repository. It is deterministic and fail-safe: an unrecognized goal defaults
// to document; repository evidence adds capabilities rather than replacing the
// goal's signal. A project can therefore be code+document or data+document.
func InferCapabilities(goal, repoPath string) []string {
	scores := capabilityScores(goal)
	if repoPath != "" {
		addRepoCapabilities(repoPath, scores)
	}
	var caps []string
	for _, c := range capabilityOrder {
		if scores[c] > 0 {
			caps = append(caps, c)
		}
	}
	return NormalizeCapabilities(caps)
}

// InferTaskCapability chooses one primary output kind for a task. Explicit
// output words in the task description win; otherwise the task inherits the
// project's primary capability, with repository code as a final hint.
func InferTaskCapability(description string, projectCaps []string, repoPath string) string {
	scores := capabilityScores(description)
	best, bestScore := "", 0
	for _, c := range capabilityOrder {
		if scores[c] > bestScore {
			best, bestScore = c, scores[c]
		}
	}
	if best != "" {
		return best
	}
	if len(projectCaps) > 0 {
		return PrimaryCapability(projectCaps)
	}
	if repoPath != "" {
		repoScores := map[string]int{}
		addRepoCapabilities(repoPath, repoScores)
		for _, c := range capabilityOrder {
			if repoScores[c] > 0 {
				return c
			}
		}
	}
	return CapabilityDocument
}

// DetectTestCommand inspects a repository's manifests and returns a bounded
// command, or "". It reads only small known files and never executes anything.
func DetectTestCommand(repoPath string) string {
	if repoPath == "" {
		return ""
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
		return "go test ./..."
	case has("Cargo.toml"):
		return "cargo test"
	case has("pom.xml"):
		return "mvn -q test"
	case has("build.gradle") || has("build.gradle.kts"):
		return "gradle test"
	}

	if pkg := read("package.json"); pkg != "" {
		var manifest struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal([]byte(pkg), &manifest) == nil && strings.TrimSpace(manifest.Scripts["test"]) != "" {
			return "npm test"
		}
	}
	if py := read("pyproject.toml"); strings.Contains(py, "[tool.pytest") {
		return "pytest -q"
	}
	if has("pytest.ini") || has("tox.ini") {
		return "pytest -q"
	}
	if cfg := read("setup.cfg"); strings.Contains(cfg, "[tool:pytest]") {
		return "pytest -q"
	}
	if has("Makefile") && regexp.MustCompile(`(?m)^test:`).MatchString(read("Makefile")) {
		return "make test"
	}
	if has("composer.json") {
		return "composer test"
	}
	if has("Gemfile") {
		return "bundle exec rspec"
	}
	// A Python test tree with no runner manifest still has an obvious runner.
	if has(filepath.Join("tests", "test_main.py")) || has("test_main.py") {
		return "pytest -q"
	}
	return ""
}

var (
	codeWords = []string{
		"implement", "fix", "refactor", "debug", "bug", "function", "module",
		"api", "endpoint", "class", "script", "command", "compile", "build",
		"repository", "repo", "commit", "package", "library", "cli", "code",
		"unit test", "tests pass", "go test", "pytest", "javascript", "python",
	}
	dataWords = []string{
		"json", "csv", "schema", "dataset", "data", "sql", "aggregate",
		"normalize", "validate", "table", "column", "records", "parquet",
		"reconcile", "etl", "query",
	}
	documentWords = []string{
		"readme", "report", "adr", "guide", "documentation",
		"postmortem", "policy", "spec", "manual", "proposal",
		"migration guide", "threat model", "architecture decision record",
	}
	textWords = []string{
		"essay", "email", "article", "blog", "post", "rewrite", "tone",
		"story", "argument", "copy", "note", "message", "summary",
		"summarize", "brief",
	}
	mediaWords = []string{
		"image", "audio", "video", "render", "media", "photo", "picture",
		"design", "animation", "sound",
	}
)

func capabilityScores(text string) map[string]int {
	low := " " + strings.ToLower(text) + " "
	scores := map[string]int{}
	add := func(capability string, words []string, weight int) {
		for _, w := range words {
			if strings.Contains(low, w) {
				scores[capability] += weight
			}
		}
	}
	// Concrete implementation verbs are the strongest signal: a task that
	// says "implement X and document it" is a code task whose output carries
	// the documentation, not a document task.
	add(CapabilityCode, codeWords, 2)
	add(CapabilityData, dataWords, 1)
	add(CapabilityDocument, documentWords, 1)
	add(CapabilityText, textWords, 1)
	add(CapabilityMedia, mediaWords, 1)
	return scores
}

var codeExtensions = map[string]bool{
	".go": true, ".py": true, ".js": true, ".ts": true, ".tsx": true,
	".jsx": true, ".rs": true, ".java": true, ".c": true, ".h": true,
	".cc": true, ".cpp": true, ".rb": true, ".php": true, ".cs": true,
	".swift": true, ".kt": true, ".scala": true, ".sh": true,
}

var dataExtensions = map[string]bool{
	".csv": true, ".parquet": true, ".sql": true,
}

var documentExtensions = map[string]bool{
	".md": true, ".rst": true, ".adoc": true, ".tex": true,
}

var mediaExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true,
	".mp3": true, ".wav": true, ".mp4": true, ".mov": true,
}

// addRepoCapabilities walks a bounded, sorted sample of the repository and
// records which capability families are present. It skips VCS/vendor/build
// trees and stops after 300 files so a large monorepo cannot stall create.
func addRepoCapabilities(root string, scores map[string]int) {
	seen := 0
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "dist", "build", "target", ".venv", "__pycache__":
				return filepath.SkipDir
			}
			return nil
		}
		seen++
		if seen > 300 {
			return filepath.SkipAll
		}
		ext := strings.ToLower(filepath.Ext(path))
		switch {
		case codeExtensions[ext]:
			scores[CapabilityCode]++
		case dataExtensions[ext]:
			scores[CapabilityData]++
		case ext == ".json":
			// JSON fixtures and schemas are data evidence only when they
			// are not the package manifest itself.
			if strings.ToLower(filepath.Base(path)) != "package.json" {
				scores[CapabilityData]++
			}
		case documentExtensions[ext]:
			scores[CapabilityDocument]++
		case mediaExtensions[ext]:
			scores[CapabilityMedia]++
		}
		return nil
	})
}
