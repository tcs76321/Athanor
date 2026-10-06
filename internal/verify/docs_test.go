package verify

import (
	"testing"

	"github.com/tcs76321/athanor/internal/project"
)

// TestRequireTestsGate pins the F5 require_tests_for_code semantics: a code
// candidate with no test run is a hard failure when required, and
// non-decisive (the pre-F5 behavior) when not.
func TestRequireTestsGate(t *testing.T) {
	r := Default()
	required := r.Run(Input{Archetype: project.ArchetypeCode, RequireTests: true})
	if !required.Decisive() || !required.HardDecisive() || required.Passed {
		t.Fatalf("tests required but not run should be a hard failure: %+v", required)
	}
	notRequired := r.Run(Input{Archetype: project.ArchetypeCode})
	if notRequired.HardDecisive() {
		t.Fatalf("tests not required and not run should be non-decisive: %+v", notRequired)
	}
}

// TestDocsVerifierGate pins the F5 require_documentation_for_code semantics.
func TestDocsVerifierGate(t *testing.T) {
	r := Default()

	// Not required: the docs verifier is absent, so an undocumented
	// candidate is not decided by it.
	if got := r.Run(Input{Archetype: project.ArchetypeCode, Content: "print(1)"}); contains(got.Verifiers, "docs") {
		t.Fatalf("docs verifier should not apply when not required: %+v", got)
	}

	// Required and undocumented: hard failure.
	bad := r.Run(Input{Archetype: project.ArchetypeCode, RequireDocs: true, Content: "print(1)\n"})
	if !bad.HardDecisive() || bad.Passed {
		t.Fatalf("undocumented code should be a hard failure: %+v", bad)
	}

	// Required and documented: pass.
	good := r.Run(Input{Archetype: project.ArchetypeCode, RequireDocs: true,
		Content: "def add(a, b):\n    \"\"\"Return the sum of a and b.\"\"\"\n    return a + b\n"})
	if !good.HardDecisive() || !good.Passed {
		t.Fatalf("documented code should pass: %+v", good)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// TestHasDocumentation covers the recognized constructs and the negatives.
func TestHasDocumentation(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"python module docstring", "\"\"\"Book manager module.\"\"\"\n\nimport os\n", true},
		{"python function docstring", "def add(a, b):\n    \"\"\"Return the sum.\"\"\"\n    return a + b\n", true},
		{"go doc comment block", "// Add adds two integers.\n// It returns the sum.\nfunc Add(a, b int) int { return a + b }\n", true},
		{"js single doc comment above function", "// parseRequest parses the incoming request.\nfunction parseRequest(req) { return req }\n", true},
		{"module header two lines", "# mymodule\n# Copyright 2026\n\nx = 1\n", true},
		{"markdown docs section", "intro\n\n## Usage\n\nrun it\n", true},
		{"no documentation", "x = 1\nprint(x)\n", false},
		{"triple quote as data", "x = \"\"\"not a docstring here\"\"\"\n", false},
		{"empty docstring", "\"\"\"\"\"\"\n", false},
		{"single top comment only", "# just one line\nx = 1\n", false},
		{"comment not above declaration", "# a note\nx = 1\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDocumentation(c.content); got != c.want {
				t.Errorf("hasDocumentation(%q) = %v, want %v", c.content, got, c.want)
			}
		})
	}
}

// TestHasDocstringRejectsShortBody pins the placeholder floor.
func TestHasDocstringRejectsShortBody(t *testing.T) {
	if hasDocumentation("\"\"\"hi\"\"\"\n") {
		t.Fatal("a two-character docstring should not count as documentation")
	}
	if !hasDocumentation("\"\"\"A longer module summary.\"\"\"\n") {
		t.Fatal("a real module docstring should count")
	}
}

// TestHasDocumentationIgnoresWhitespaceOnlyBlock makes sure a blank comment
// run is not mistaken for docs.
func TestHasDocumentationIgnoresWhitespaceOnlyBlock(t *testing.T) {
	if hasDocumentation("#\n#\n") {
		t.Fatal("a whitespace-only comment block should not count")
	}
}
