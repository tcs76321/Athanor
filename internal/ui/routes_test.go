package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// expectedRoutes is the UI's public route contract (M9 hardening; the UI split
// in 0.0.x). It is deliberately a static list: a refactor that moves handlers
// between files may not silently drop, duplicate, or rename a route. Adding a
// page means adding its pattern here in the same commit.
//
// Keep the list sorted; the test sorts before comparing.
const expectedRoutes = `
GET /ui
GET /ui/artifacts
GET /ui/artifacts/{id}
GET /ui/artifacts/{id}/diff
GET /ui/artifacts/{id}/download
GET /ui/assets/
GET /ui/compare
GET /ui/corrections
GET /ui/cycles/{id}/graph.json
GET /ui/daydream
GET /ui/digest
GET /ui/events
GET /ui/events/stream
GET /ui/jobs
GET /ui/jobs/{id}
GET /ui/jobs/{id}/stream
GET /ui/jobs/{id}/trace
GET /ui/memory
GET /ui/memory/chunks/{id}
GET /ui/memory/compacted/{id}
GET /ui/models
GET /ui/new
GET /ui/notifications
GET /ui/notifications/count
GET /ui/projects
GET /ui/projects/new
GET /ui/projects/{id}
GET /ui/projects/{id}/canvas
GET /ui/projects/{id}/cycles/new
GET /ui/projects/{id}/onboarding
GET /ui/reviews
GET /ui/runtime
GET /ui/search
GET /ui/settings
GET /ui/setup
GET /ui/sitemap
GET /ui/statistics
GET /ui/welcome
POST /ui/alarms/{id}/resolve
POST /ui/approvals/{id}
POST /ui/artifacts/{id}/export
POST /ui/artifacts/{id}/review
POST /ui/assist
POST /ui/corrections/{id}
POST /ui/cycles/{id}/graph
POST /ui/cycles/{id}/start
POST /ui/density
POST /ui/freeze
POST /ui/jobs/{id}/control
POST /ui/jobs/{id}/note
POST /ui/mode
POST /ui/models/pool
POST /ui/models/qualify
POST /ui/models/qualify/deep
POST /ui/new/approve
POST /ui/new/message
POST /ui/projects
POST /ui/projects/{id}/corrections
POST /ui/projects/{id}/cycles
POST /ui/projects/{id}/execution
POST /ui/projects/{id}/goals
POST /ui/projects/{id}/index
POST /ui/projects/{id}/intent
POST /ui/projects/{id}/onboarding
POST /ui/projects/{id}/priority
POST /ui/projects/{id}/push
POST /ui/projects/{id}/triggers/{triggerID}/toggle
POST /ui/setup/personas
POST /ui/setup/qualify
POST /ui/setup/qualify/cancel
POST /ui/strategy/insights/{id}
POST /ui/tasks/{id}/reset
POST /ui/theme
POST /ui/welcome
`

// TestRouteTableIsStable pins the route table in routes.go. It parses the
// source rather than constructing a UI: the package needs a full Deps to
// register routes, and this test is about the contract, not the wiring.
func TestRouteTableIsStable(t *testing.T) {
	got := registeredRoutes(t)
	want := splitRoutes(expectedRoutes)

	if len(got) != len(want) {
		t.Errorf("route count = %d, want %d", len(got), len(want))
	}
	for _, r := range got {
		if !strings.HasPrefix(r, "GET /") && !strings.HasPrefix(r, "POST /") {
			t.Errorf("route %q has no GET/POST method prefix", r)
		}
	}

	sort.Strings(got)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Errorf("route table drifted from the contract")
		for _, r := range missing(want, got) {
			t.Errorf("  missing route: %q", r)
		}
		for _, r := range missing(got, want) {
			t.Errorf("  unexpected route: %q", r)
		}
	}
}

// TestRouteTableHasNoDuplicates fails on a repeated method+pattern. Go's mux
// panics on a duplicate registration at boot; this catches it at test time with
// a readable diff.
func TestRouteTableHasNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range registeredRoutes(t) {
		if seen[r] {
			t.Errorf("duplicate route %q", r)
		}
		seen[r] = true
	}
}

// registeredRoutes parses routes.go and returns every mux.HandleFunc pattern.
func registeredRoutes(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "routes.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing routes.go: %v", err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "HandleFunc" {
			return true
		}
		if len(call.Args) == 0 {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			t.Errorf("route at %s is not a string literal", fset.Position(call.Pos()))
			return true
		}
		pattern, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Errorf("unquoting route at %s: %v", fset.Position(call.Pos()), err)
			return true
		}
		out = append(out, pattern)
		return true
	})
	return out
}

func splitRoutes(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// missing returns the elements of want that are absent from got. Both slices
// must be sorted.
func missing(want, got []string) []string {
	var out []string
	for _, w := range want {
		if !slices.Contains(got, w) {
			out = append(out, w)
		}
	}
	return out
}
