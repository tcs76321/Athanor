package gate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestGateSourceReadsGoThroughContainment is the structural half of the
// source-layer contract. Local project sources (declared files and the news
// wire) must be read through internal/source, which routes every open through
// airlock/paths (O_NOFOLLOW, symlink refusal, caps). A direct os.ReadFile in
// one of these packages would quietly reopen the path-escape gap that
// internal/source exists to close.
//
// The fixture reader is the one named exception: internal/engine/phases/
// fixture.go has its own containment and size checks (ADR-0065 §6), and its
// tests exercise the escape cases.
func TestGateSourceReadsGoThroughContainment(t *testing.T) {
	root := filepath.Join("..", "..")
	scoped := map[string]bool{
		"internal/firstuse":          true,
		"internal/engine/contextasm": true,
		"internal/engine/phases":     true,
	}
	allowed := map[string]bool{
		"internal/engine/phases/fixture.go": true,
	}
	seenDirs := map[string]int{}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == ".git" || rel == "spikes" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		if !scoped[dir] {
			return nil
		}
		seenDirs[dir]++
		if allowed[rel] {
			return nil
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			switch {
			case pkg.Name == "os" && sel.Sel.Name == "ReadFile":
				t.Errorf("%s: os.ReadFile bypasses internal/source containment", rel)
			case pkg.Name == "ioutil" && sel.Sel.Name == "ReadFile":
				t.Errorf("%s: ioutil.ReadFile bypasses internal/source containment", rel)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree: %v", err)
	}
	for dir := range scoped {
		if seenDirs[dir] == 0 {
			t.Errorf("scoped package %s has no production files (did it move?)", dir)
		}
	}
}
