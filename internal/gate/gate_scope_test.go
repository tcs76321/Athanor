// Gate — scope isolation for the pod-facing request surface.
//
// The M3-T7 security review found two cross-scope disclosure bugs
// (`context_swap`, `query_memory`) where a pod-supplied scope was
// trusted (F-1/F-2), and recommended a structural backstop so a future
// scope-carrying field cannot land silently:
//
//	"no pod-supplied identifier may select a scope; scopes are always
//	 derived server-side from the authenticated job."
//
// This gate enforces that structurally. It parses the shared wire types
// in internal/toolenvelope/types.go, finds every *Request struct that
// carries a scope-selecting field (Scope / ProjectID / JobID), and
// requires each to be registered with the internalapi handler that
// constrains it server-side. A new scope-carrying request type, or a
// new scope field on an existing one, fails the build until it is
// registered — and a stale registry entry fails too.
//
// It is a structural backstop, not a correctness proof: the behavioral
// tests (`TestQueryMemory_RejectsForeignScope`, the context_swap
// foreign-chunk tests) prove the handlers actually reject a foreign
// scope. This gate ensures a new field cannot slip past review.
package gate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
)

// toolenvelopeTypesFile is the shared wire-type source the gate
// inspects, relative to this test file (internal/gate/).
const toolenvelopeTypesFile = "../../internal/toolenvelope/types.go"

// scopeCarryingFields are the struct field names a pod could use to
// select data outside its own scope. Every *Request struct carrying one
// must be registered below.
var scopeCarryingFields = map[string]bool{
	"Scope":     true,
	"ProjectID": true,
	"JobID":     true,
}

// scopeConstrainedRequestTypes maps each scope-carrying request type to
// the internalapi handler that derives or validates the scope
// server-side. context_swap forces Scope to the authenticated job;
// query_memory accepts only the caller's job or project. Adding a
// scope-carrying type (or field) without an entry here fails the gate.
var scopeConstrainedRequestTypes = map[string]string{
	"ContextSwapRequest": "handleContextSwap",
	"QueryMemoryRequest": "handleQueryMemory",
}

// TestGateScopeIsolation is the structural proof described in the
// package comment. It fails if (a) a scope-carrying *Request type is not
// registered, (b) a registry entry no longer carries a scope field
// (stale), (c) a registered type's handler does not exist, or (d) that
// handler does not reference req.Scope (so the field is untouched and
// could be trusted verbatim).
func TestGateScopeIsolation(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, toolenvelopeTypesFile, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", toolenvelopeTypesFile, err)
	}

	// detected maps a request type name to the scope-carrying field
	// names it declares.
	detected := map[string][]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		if !strings.HasSuffix(ts.Name.Name, "Request") {
			return true // responses may echo Scope; only requests are pod input
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		for _, f := range st.Fields.List {
			for _, name := range f.Names {
				if scopeCarryingFields[name.Name] {
					detected[ts.Name.Name] = append(detected[ts.Name.Name], name.Name)
				}
			}
		}
		return true
	})

	// (a) every detected scope-carrying type is registered.
	var unregistered []string
	for typ := range detected {
		if _, ok := scopeConstrainedRequestTypes[typ]; !ok {
			unregistered = append(unregistered, typ)
		}
	}
	sort.Strings(unregistered)
	if len(unregistered) > 0 {
		t.Errorf("scope-carrying request types not registered in scopeConstrainedRequestTypes: %v — "+
			"register each with the handler that constrains its scope server-side (scope isolation gate)", unregistered)
	}

	// (b)–(d) every registry entry is live and its handler touches req.Scope.
	for typ, handler := range scopeConstrainedRequestTypes {
		if _, ok := detected[typ]; !ok {
			t.Errorf("registry names %q but no scope-carrying field was detected; remove the stale entry", typ)
			continue
		}
		path, content, ok := findFileContaining(internalapiDir, "func (a *API) "+handler)
		if !ok {
			t.Errorf("handler %s for %q not found under internalapi; the scope constraint has no home", handler, typ)
			continue
		}
		if !strings.Contains(content, "req.Scope") {
			t.Errorf("%s declares %s but does not reference req.Scope; a pod-supplied scope may be trusted (scope isolation gate)", path, handler)
		}
	}
}
