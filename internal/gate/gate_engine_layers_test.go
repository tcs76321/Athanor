// Package gate also enforces the engine's internal layering (ADR-0131).
//
// The engine split is only durable if the dependency direction is a build
// break, not a convention. This file walks the engine subpackages and fails
// when one imports the root engine package (an upward call) or when a package
// other than jobstate reaches for the database.
//
// The table grows with the split: each extracted package adds its own rule in
// the commit that creates it.
package gate

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	engineRootImport = "github.com/tcs76321/athanor/internal/engine"
	jobstateImport   = "github.com/tcs76321/athanor/internal/engine/jobstate"
	storeImport      = "github.com/tcs76321/athanor/internal/store"
)

// engineLayerRule names one engine subpackage and the imports it may not make.
type engineLayerRule struct {
	pkg           string // directory under internal/engine
	forbidImports []string
}

// engineLayerRules is the dependency-direction table. The root may import any
// subpackage; a subpackage may never import the root. Sibling rules are added
// as the packages appear.
var engineLayerRules = []engineLayerRule{
	{pkg: "jobstate", forbidImports: []string{engineRootImport}},
	{pkg: "acceptance", forbidImports: []string{engineRootImport, jobstateImport}},
	{pkg: "contextasm", forbidImports: []string{engineRootImport}},
	{pkg: "repair", forbidImports: []string{engineRootImport}},
}

// TestGateEngineLayerDirections fails when an extracted engine package imports
// the root package or a package it must not depend on.
func TestGateEngineLayerDirections(t *testing.T) {
	root := "../../internal/engine"
	for _, rule := range engineLayerRules {
		dir := filepath.Join(root, rule.pkg)
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("engine layer %q is missing: %v", rule.pkg, err)
		}
		walkEnginePackage(t, dir, func(path, importPath string) {
			for _, forbidden := range rule.forbidImports {
				if importPath == forbidden {
					t.Errorf("%s imports %s — engine subpackages may not call upward (ADR-0131)", path, importPath)
				}
			}
		})
	}
}

// TestGateEngineDatabaseOwnership keeps the database in jobstate. An
// orchestration package that opens its own SQL would hold the single writer
// across I/O and defeat the layering the split just bought.
func TestGateEngineDatabaseOwnership(t *testing.T) {
	root := "../../internal/engine"
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pkg := entry.Name()
		if pkg == "jobstate" {
			continue // the sanctioned owner
		}
		walkEnginePackage(t, filepath.Join(root, pkg), func(path, importPath string) {
			if importPath == storeImport {
				t.Errorf("%s imports internal/store — only jobstate may touch the engine's database (ADR-0131)", path)
			}
		})
	}
}

// walkEnginePackage parses every production Go file under dir and calls fn with
// the repo-relative path and each imported module path.
func walkEnginePackage(t *testing.T, dir string, fn func(path, importPath string)) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if perr != nil {
			t.Errorf("parsing %s: %v", path, perr)
			return nil
		}
		for _, imp := range file.Imports {
			quoted, uerr := strconv.Unquote(imp.Path.Value)
			if uerr != nil {
				t.Errorf("unquoting import in %s: %v", path, uerr)
				continue
			}
			fn(relPath(path), quoted)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
