// Package deps enforces the project's dependency policy as an
// executable test. AGENTS.md states: "Adding a dependency is a project
// decision, not an agent decision."
//
// This test reads `go.mod` from the repo root and fails the build when
// a direct (non-indirect) dependency is not in the ratified allowlist
// below, or when a ratified dependency is no longer required. The
// allowlist is the mechanism: a new dependency cannot land without
// adding its module path here, which is the human-visible act of
// ratification. The test is bidirectional, so it also catches stale
// entries after a removal.
//
// History: an earlier version only matched the *flat* `require <path>
// <version>` form and never tracked `require ( ... )` block state, so
// it missed every dependency inside a block. It reported one direct
// dependency while go.mod carried ten, meaning the policy was not
// actually enforced for the deps adopted in M4-T6 (Reader Mode / HTML
// parsing), M5-T2 (tree-sitter), or M4-T2 (fsnotify). `directRequires`
// now tracks block state and counts both forms.
//
// What this test does NOT cover:
//
//   - Transitive dependencies. These are out of our control without
//     dropping the direct dep that pulls them in; if a future dep
//     drags in something unacceptable, the response is to find an
//     alternative direct dep, not to widen the allowlist.
//   - Test-only deps. The Go module system has no `require test`
//     directive as of Go 1.17+; everything in `require` is treated
//     uniformly. The intended workflow if a test-only dep is needed
//     is to ship it as `//go:build` gated code that the production
//     binary doesn't compile, then add the require with explicit
//     justification in the PR and the allowlist update.
//
// The test is in `internal/deps/` rather than `internal/gate/` because
// it's a different kind of guarantee: structural on the module file,
// not on the source AST. Gate G1 stays focused on tool-execution
// containment; this stays focused on the dependency policy.
package deps

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// allowedDirectDeps is the ratified set of direct dependencies, one
// module path per go.mod direct `require` (block and flat forms alike).
//
// Adding a dependency is a project decision (AGENTS.md). Adding its
// path to this list in the same commit is that decision made concrete;
// removing a dependency means removing its path here too, or the test
// fails on the stale entry.
var allowedDirectDeps = []string{
	"codeberg.org/readeck/go-readability/v2",
	"github.com/fsnotify/fsnotify",
	"github.com/mattn/go-sqlite3",
	"github.com/microcosm-cc/bluemonday",
	"github.com/tree-sitter/go-tree-sitter",
	"github.com/tree-sitter/tree-sitter-go",
	"github.com/tree-sitter/tree-sitter-javascript",
	"github.com/tree-sitter/tree-sitter-python",
	"golang.org/x/net",
	"gopkg.in/yaml.v3",
}

func TestDirectDependenciesAreRatified(t *testing.T) {
	// Walk up from this test's directory to the module root. The
	// test file lives at internal/deps/deps_test.go, so two `..`
	// hops reach the module root.
	root, err := findModuleRoot()
	if err != nil {
		t.Fatalf("finding module root: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}

	got := directRequires(string(raw))
	allowed := make(map[string]bool, len(allowedDirectDeps))
	for _, p := range allowedDirectDeps {
		allowed[p] = true
	}
	required := make(map[string]bool, len(got))
	for _, p := range got {
		required[p] = true
	}

	for _, p := range got {
		if !allowed[p] {
			t.Errorf("unratified direct dependency %q.\n"+
				"Adding a dependency is a project decision, not an agent decision "+
				"(AGENTS.md): add its module path to allowedDirectDeps in this "+
				"file, with justification, and update the AGENTS.md/DEVELOPMENT.md "+
				"dependency notes.", p)
		}
	}
	for _, p := range allowedDirectDeps {
		if !required[p] {
			t.Errorf("ratified dependency %q is no longer required in go.mod.\n"+
				"Remove it from allowedDirectDeps so the list reflects reality.", p)
		}
	}
}

// findModuleRoot walks up the directory tree looking for go.mod.
func findModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

// directRequires returns the module paths of every direct (non-indirect)
// require in go.mod, in file order. It understands both forms:
//
//	require gopkg.in/yaml.v3 v3.0.1          // flat
//	require (                                 // block
//		github.com/mattn/go-sqlite3 v1.14.0
//		golang.org/x/sys v0.45.0 // indirect
//	)
//
// Only direct requires are returned; a line carrying the `// indirect`
// marker is skipped. The parser stays small on purpose — it tracks block
// state and reads the first whitespace-separated token as the path —
// because `go mod tidy` normalizes the file and CI runs the same
// targets. It does not attempt the full go.mod grammar (`replace`,
// `exclude`, retract directives): those do not add direct requires, so
// ignoring them is correct for this test.
func directRequires(goMod string) []string {
	var out []string
	inBlock := false
	for _, raw := range strings.Split(goMod, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if strings.HasPrefix(line, "require (") {
			inBlock = true
			continue
		}
		if inBlock {
			if line == ")" {
				inBlock = false
				continue
			}
			if strings.Contains(line, "// indirect") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) >= 2 && strings.Contains(fields[0], "/") {
				out = append(out, fields[0])
			}
			continue
		}
		// Flat form: `require <path> <version>`.
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "require" &&
			strings.Contains(fields[1], "/") && !strings.Contains(line, "// indirect") {
			out = append(out, fields[1])
		}
	}
	return out
}

// TestDirectRequiresParsesBothForms pins the parser against the exact
// regression this file's history describes: an earlier flat-only parser
// returned 1 path from a go.mod that carried 10, so the policy was never
// enforced for block-form deps. The synthetic inputs here are small and
// form-focused; TestDirectDependenciesAreRatified runs the same parser
// against the real go.mod.
func TestDirectRequiresParsesBothForms(t *testing.T) {
	cases := []struct {
		name string
		mod  string
		want []string
	}{
		{
			name: "flat_form",
			mod:  "module x\n\ngo 1.26\n\nrequire gopkg.in/yaml.v3 v3.0.1\n",
			want: []string{"gopkg.in/yaml.v3"},
		},
		{
			name: "block_form_counts_direct_and_skips_indirect",
			mod: "module x\n\ngo 1.26\n\nrequire (\n" +
				"\tgithub.com/mattn/go-sqlite3 v1.14.0\n" +
				"\tgolang.org/x/sys v0.45.0 // indirect\n" +
				")\n",
			want: []string{"github.com/mattn/go-sqlite3"},
		},
		{
			name: "mixed_forms_preserve_order",
			mod: "module x\n\ngo 1.26\n\nrequire (\n" +
				"\tgithub.com/fsnotify/fsnotify v1.10.1\n" +
				")\n\n" +
				"require gopkg.in/yaml.v3 v3.0.1\n",
			want: []string{"github.com/fsnotify/fsnotify", "gopkg.in/yaml.v3"},
		},
		{
			name: "only_indirect_yields_nothing",
			mod:  "module x\n\ngo 1.26\n\nrequire (\n\tgolang.org/x/text v0.37.0 // indirect\n)\n",
			want: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := directRequires(c.mod)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("directRequires = %v, want %v", got, c.want)
			}
		})
	}
}
