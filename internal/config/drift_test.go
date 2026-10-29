package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestConfigFieldsAreConsumed is the configuration-honesty gate: a field
// that is parsed, defaulted, and validated must be read by production code
// (outside internal/config), or it must be on the explicit unimplemented
// list. A new dead knob fails here instead of being discovered by a user
// whose setting silently did nothing.
//
// The check is syntactic (selector names), not a full data-flow analysis:
// it catches the common "field exists but nothing reads it" regression.
// Nested config structs are walked through pointers, maps, and slices.
func TestConfigFieldsAreConsumed(t *testing.T) {
	used := selectorNames(t)
	// A field read only inside a resolver method (e.g. ReaderModeDefault via
	// Network.ReaderMode) is consumed when that method is called. Fold the
	// method→field relation into the used set before judging leaves.
	for method, fields := range methodFieldUses(t) {
		if !used[method] {
			continue
		}
		for _, f := range fields {
			used[f] = true
		}
	}
	cfg := &Config{}
	leaves := leafYAMLNames(reflect.TypeOf(cfg).Elem(), nil)
	unimplemented := map[string]bool{
		// Rejected by validateUnenforced when set to a non-default value.
		"MaxTotalRecoveriesPerJob":                    true,
		"MaxToolCallRetries":                          true,
		"MaxLoopInterventions":                        true,
		"MaxContextCompactions":                       true,
		"MaxProactiveTasksPerDay":                     true,
		"BrowserModeRequiresApproval":                 true,
		"IncludeWorkspaceMetadata":                    true,
		"DaydreamMaxConcurrent":                       true,
		"ResearchFloor":                               true,
		"InsightExpiryDays":                           true,
		"VerifierMinFraction":                         true,
		"MinAnchorAgreement":                          true,
		"ScanIngressFiles":                            true,
		"ScanEgressFiles":                             true,
		"PromptInjectionScan":                         true,
		"QuarantineSuspiciousFiles":                   true,
		"PromptInjectionLongUserPromptThresholdBytes": true,
		"PromptInjectionScanLongUserPrompts":          true,
		"Ingress":                                     true,
		"Egress":                                      true,
		"UserPrompt":                                  true,
		// Reserved cloud surface: accepted, warned about, not wired to a client.
		"DefaultBackend":        true,
		"CloudEnabled":          true,
		"CloudRequiresApproval": true,
	}
	var dead []string
	for _, name := range leaves {
		if used[name] || unimplemented[name] {
			continue
		}
		dead = append(dead, name)
	}
	if len(dead) > 0 {
		t.Fatalf("config fields parsed but never read outside internal/config: %v\nAdd the field to validateUnenforced's reject list or wire it up", dead)
	}
}

// methodFieldUses maps each config method name to the field selectors it
// reads. It lets the gate credit a field that is only reached through a
// resolver such as Network.ReaderMode.
func methodFieldUses(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	dir := filepath.Join("..", "..", "internal", "config")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Body == nil {
				continue
			}
			var fields []string
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok {
					fields = append(fields, sel.Sel.Name)
				}
				return true
			})
			out[fn.Name.Name] = append(out[fn.Name.Name], fields...)
		}
	}
	return out
}

// selectorNames returns every selector identifier used in production Go
// files under internal/ and cmd/, excluding internal/config and tests.
func selectorNames(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	root := filepath.Join("..", "..")
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "config" && strings.HasSuffix(filepath.Dir(path), filepath.Join("internal")) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(f, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok {
					out[sel.Sel.Name] = true
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", top, err)
		}
	}
	return out
}

// leafYAMLNames collects the Go field names of every leaf carrying a yaml
// tag. Containers (structs, pointers, maps, slices) are traversed; a cycle
// guard keeps a self-referential type from looping.
func leafYAMLNames(t reflect.Type, seen map[reflect.Type]bool) []string {
	if seen == nil {
		seen = map[reflect.Type]bool{}
	}
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	if t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || seen[t] {
		return nil
	}
	seen[t] = true
	defer delete(seen, t)

	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("yaml")
		if tag == "" || tag == "-" {
			// Recurse into untagged embedded structs (none today, but cheap).
			if f.Anonymous {
				out = append(out, leafYAMLNames(f.Type, seen)...)
			}
			continue
		}
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct || ft.Kind() == reflect.Map || ft.Kind() == reflect.Slice || ft.Kind() == reflect.Array {
			children := leafYAMLNames(ft, seen)
			if len(children) > 0 {
				out = append(out, children...)
				continue
			}
		}
		out = append(out, f.Name)
	}
	return out
}
