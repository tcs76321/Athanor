package workflow

import (
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/project"
)

func linearGraph() Graph {
	return Graph{
		Blocks: []Block{
			{ID: "research", Kind: KindResearch, Config: map[string]any{"instructions": "Find sources."}},
			{ID: "fetch", Kind: KindFetch},
			{ID: "extract", Kind: KindExtract},
			{ID: "condense", Kind: KindCondense},
			{ID: "synthesize", Kind: KindSynthesize, Config: map[string]any{
				"acceptance": []any{"every claim has a citation", "under 800 words"},
			}},
			{ID: "verify", Kind: KindVerify},
		},
		Edges: []Edge{
			{From: "research", To: "fetch"},
			{From: "fetch", To: "extract"},
			{From: "extract", To: "condense"},
			{From: "condense", To: "synthesize"},
			{From: "synthesize", To: "verify"},
		},
	}
}

func TestCompileLinearGraph(t *testing.T) {
	specs, err := Compile(linearGraph(), CompileOptions{CycleID: "cycle-1"})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(specs) != 6 {
		t.Fatalf("specs = %d, want 6", len(specs))
	}
	byKey := map[string]int{}
	for i, s := range specs {
		byKey[s.Key] = i
		if s.CycleID != "cycle-1" {
			t.Errorf("%s cycle = %q", s.Key, s.CycleID)
		}
	}
	for _, want := range []string{"research", "fetch", "extract", "condense", "synthesize", "verify"} {
		if _, ok := byKey[want]; !ok {
			t.Errorf("missing spec %q", want)
		}
	}
	// fetch depends on research, synthesize on condense, verify on synthesize.
	checkDep := func(key, dep string) {
		t.Helper()
		s := specs[byKey[key]]
		found := false
		for _, d := range s.DependsOn {
			if d == dep {
				found = true
			}
		}
		if !found {
			t.Errorf("%s depends on %v, want %s", key, s.DependsOn, dep)
		}
	}
	checkDep("fetch", "research")
	checkDep("synthesize", "condense")
	checkDep("verify", "synthesize")
	if got := specs[byKey["research"]].Description; !strings.Contains(got, "Find sources.") {
		t.Errorf("instructions not carried: %q", got)
	}
	if got := specs[byKey["research"]].Description; !strings.Contains(got, "every claim names the URL") {
		t.Errorf("block contract not carried: %q", got)
	}
	if got := specs[byKey["synthesize"]].Criteria; len(got) != 2 {
		t.Errorf("criteria = %v, want 2", got)
	}
	if _, ok := byKey["implement"]; ok {
		t.Error("implement unexpectedly present")
	}
	if got := specs[byKey["research"]].AllowedTools; len(got) != 3 || got[0] != "fetch_url" {
		t.Errorf("research tools = %v", got)
	}
}

func TestValidateResolvesUniqueCompatiblePorts(t *testing.T) {
	// research has two outputs (urls, text). An unnamed edge resolves by the
	// unique compatible pair, so both natural compositions validate.
	g := Graph{
		Blocks: []Block{
			{ID: "research", Kind: KindResearch},
			{ID: "fetch", Kind: KindFetch},
			{ID: "options", Kind: KindGenerateOptions},
		},
		Edges: []Edge{
			{From: "research", To: "fetch"},
			{From: "research", To: "options"},
		},
	}
	if err := Validate(g, DefaultLimits()); err != nil {
		t.Fatalf("unique-pair graph rejected: %v", err)
	}
}

func TestValidateRejectsAmbiguousPorts(t *testing.T) {
	// condense has one input and one output of the same type, so a chain
	// between two condensers has two compatible pairs only if a block exposes
	// both. Build that case explicitly: two identical ports on one side.
	orig, _ := Spec(KindCondense)
	dup := orig
	dup.Inputs = []Port{{Name: "a", Type: PortItems}, {Name: "b", Type: PortItems}}
	specs[KindCondense] = dup
	t.Cleanup(func() { specs[KindCondense] = orig })
	g := Graph{
		Blocks: []Block{{ID: "x", Kind: KindCondense}, {ID: "y", Kind: KindCondense}},
		Edges:  []Edge{{From: "x", To: "y"}},
	}
	err := Validate(g, DefaultLimits())
	if err == nil || !strings.Contains(err.Error(), "ambiguous edge") {
		t.Fatalf("Validate = %v, want ambiguous edge", err)
	}
}

func TestNormalizeRewritesEdgePorts(t *testing.T) {
	g := Graph{
		Blocks: []Block{
			{ID: "research", Kind: KindResearch},
			{ID: "options", Kind: KindGenerateOptions},
		},
		Edges: []Edge{{From: "research", To: "options", FromPort: "sources", ToPort: "brief"}},
	}
	got, err := Normalize(g)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if got.Edges[0].FromPort != "notes" || got.Edges[0].ToPort != "brief" {
		t.Errorf("edge = %+v, want notes -> brief", got.Edges[0])
	}
	if g.Edges[0].FromPort != "sources" {
		t.Error("Normalize mutated the caller's graph")
	}
	bad := Graph{Blocks: []Block{{ID: "a", Kind: "teleport"}}}
	if _, err := Normalize(bad); err == nil {
		t.Error("invalid graph normalized")
	}
}

func TestValidateRepairsExplicitIncompatiblePorts(t *testing.T) {
	cases := []Graph{
		// A named pair with the wrong types: urls -> text.
		{
			Blocks: []Block{
				{ID: "research", Kind: KindResearch},
				{ID: "options", Kind: KindGenerateOptions},
			},
			Edges: []Edge{{
				From: "research", To: "options",
				FromPort: "sources", ToPort: "brief",
			}},
		},
		// A hallucinated port name: select has "options", not "inputs".
		{
			Blocks: []Block{
				{ID: "critique", Kind: KindCritique},
				{ID: "select", Kind: KindSelect},
			},
			Edges: []Edge{{
				From: "critique", To: "select",
				ToPort: "inputs",
			}},
		},
	}
	for i, g := range cases {
		if err := Validate(g, DefaultLimits()); err != nil {
			t.Errorf("case %d: repairable edge rejected: %v", i, err)
		}
	}
}

func TestValidateRejectsBadGraphs(t *testing.T) {
	base := func() Graph { return linearGraph() }

	cases := []struct {
		name string
		mut  func(Graph) Graph
		want string
	}{
		{"empty", func(g Graph) Graph { return Graph{} }, "no blocks"},
		{"unknown kind", func(g Graph) Graph {
			g.Blocks[0].Kind = "teleport"
			return g
		}, "unknown block kind"},
		{"duplicate id", func(g Graph) Graph {
			g.Blocks[1].ID = "research"
			return g
		}, "duplicate block id"},
		{"unknown edge from", func(g Graph) Graph {
			g.Edges[0].From = "nope"
			return g
		}, "unknown from block"},
		{"unknown edge to", func(g Graph) Graph {
			g.Edges[0].To = "nope"
			return g
		}, "unknown to block"},
		{"self edge", func(g Graph) Graph {
			g.Edges[0].To = "research"
			return g
		}, "self edge"},
		{"cycle", func(g Graph) Graph {
			return Graph{
				Blocks: []Block{
					{ID: "a", Kind: KindCondense},
					{ID: "b", Kind: KindCondense},
				},
				Edges: []Edge{
					{From: "a", To: "b"},
					{From: "b", To: "a"},
				},
			}
		}, "graph has a cycle"},
		{"bad port", func(g Graph) Graph {
			g.Edges[0].To = "extract"
			g.Edges[0].ToPort = "documents"
			g.Edges[0].FromPort = "nope"
			return g
		}, "unknown output port"},
		{"type mismatch", func(g Graph) Graph {
			g.Edges[0].To = "extract"
			g.Edges[0].ToPort = "documents"
			return g
		}, "type mismatch"},
		{"empty id", func(g Graph) Graph {
			g.Blocks[0].ID = ""
			return g
		}, "empty id"},
		{"uppercase id", func(g Graph) Graph {
			g.Blocks[0].ID = "Research"
			return g
		}, "lowercase"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.mut(base()), DefaultLimits())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateLimits(t *testing.T) {
	blocks := make([]Block, 4)
	edges := make([]Edge, 0, 3)
	for i := range blocks {
		blocks[i] = Block{ID: string(rune('a' + i)), Kind: KindCondense}
		if i > 0 {
			edges = append(edges, Edge{From: blocks[i-1].ID, To: blocks[i].ID})
		}
	}
	g := Graph{Blocks: blocks, Edges: edges}
	if err := Validate(g, Limits{MaxBlocks: 3, MaxDepth: 12}); err == nil {
		t.Fatal("max blocks not enforced")
	}
	if err := Validate(g, Limits{MaxBlocks: 12, MaxDepth: 2}); err == nil {
		t.Fatal("max depth not enforced")
	}
	if err := Validate(g, Limits{MaxBlocks: 12, MaxDepth: 12}); err != nil {
		t.Fatalf("valid graph rejected: %v", err)
	}
}

func TestRegistryClosed(t *testing.T) {
	reg := Registry()
	if len(reg) != 14 {
		t.Fatalf("registry = %d, want 14", len(reg))
	}
	for _, s := range reg {
		if s.Title == "" || s.Capability == "" {
			t.Errorf("incomplete spec %+v", s)
		}
		if len(s.Inputs) == 0 && len(s.Outputs) == 0 {
			t.Errorf("%s has no ports", s.Kind)
		}
		if _, ok := Spec(s.Kind); !ok {
			t.Errorf("Spec(%s) missing", s.Kind)
		}
	}
	if _, ok := Spec("unknown"); ok {
		t.Error("unknown kind resolved")
	}
}

func TestTemplateRoundTrip(t *testing.T) {
	tpl := Template{
		ID: "daily-brief", Name: "Daily Brief", Version: 1, CycleKind: "operate",
		Graph: linearGraph(),
		Triggers: []Trigger{
			{ID: "schedule", Kind: TriggerSchedule, Spec: "0 7 * * *", Enabled: true},
			{ID: "manual", Kind: TriggerManual, Enabled: true},
		},
	}
	raw, err := EncodeTemplate(tpl)
	if err != nil {
		t.Fatalf("EncodeTemplate: %v", err)
	}
	got, err := DecodeTemplate(raw)
	if err != nil {
		t.Fatalf("DecodeTemplate: %v", err)
	}
	if got.ID != tpl.ID || got.Version != tpl.Version || len(got.Graph.Blocks) != 6 || len(got.Triggers) != 2 {
		t.Errorf("round trip = %+v", got)
	}

	tpl.Version = 0
	if _, err := EncodeTemplate(tpl); err == nil {
		t.Fatal("version 0 accepted")
	}
	tpl.Version = 1
	tpl.Triggers[0].Kind = "cron"
	if _, err := EncodeTemplate(tpl); err == nil {
		t.Fatal("unknown trigger accepted")
	}
}

func TestBuiltinTemplatesValidate(t *testing.T) {
	for _, tpl := range Templates() {
		if err := ValidateTemplate(tpl); err != nil {
			t.Errorf("%s: %v", tpl.ID, err)
		}
		if _, err := EncodeTemplate(tpl); err != nil {
			t.Errorf("%s encode: %v", tpl.ID, err)
		}
	}
	brief, ok := TemplateByID("daily-brief")
	if !ok {
		t.Fatal("daily-brief missing")
	}
	if brief.CycleKind != "operate" || len(brief.Graph.Blocks) != 7 {
		t.Errorf("daily brief = %+v", brief)
	}
	seen := map[string]bool{}
	for _, b := range brief.Graph.Blocks {
		seen[b.ID] = true
	}
	for _, want := range []string{"research", "fetch", "extract", "condense", "synthesize", "verify", "retain"} {
		if !seen[want] {
			t.Errorf("daily brief missing %q", want)
		}
	}
	if _, ok := TemplateByID("nope"); ok {
		t.Error("unknown template resolved")
	}
}

func TestCompileFoldsSourcesIntoResearchAndFetch(t *testing.T) {
	specs, err := Compile(linearGraph(), CompileOptions{
		CycleID: "c1", Sources: []string{"https://example.com/a", "https://example.com/b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]project.TaskSpec{}
	for _, s := range specs {
		byKey[s.Key] = s
	}
	for _, key := range []string{"research", "fetch"} {
		desc := byKey[key].Description
		if !strings.Contains(desc, "https://example.com/a") || !strings.Contains(desc, "https://example.com/b") {
			t.Errorf("%s description missing sources: %q", key, desc)
		}
	}
	if desc := byKey["condense"].Description; strings.Contains(desc, "https://example.com/a") {
		t.Errorf("condense should not carry project sources: %q", desc)
	}
}

func TestCompileUsesDefaultAcceptanceWhenConfigOmitsIt(t *testing.T) {
	g := Graph{Blocks: []Block{{ID: "research", Kind: KindResearch}}}
	specs, err := Compile(g, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(specs[0].Criteria) == 0 {
		t.Fatal("research has no default criteria")
	}
	if !strings.Contains(strings.Join(specs[0].Criteria, " "), "source") {
		t.Errorf("criteria = %v", specs[0].Criteria)
	}
}

func TestCompileInjectsLocalSourceText(t *testing.T) {
	g := Graph{Blocks: []Block{{ID: "research", Kind: KindResearch}}}
	specs, err := Compile(g, CompileOptions{
		Sources:     []string{"note.md"},
		SourceTexts: map[string]string{"note.md": "The authoritative fact."},
	})
	if err != nil {
		t.Fatal(err)
	}
	desc := specs[0].Description
	if !strings.Contains(desc, "SOURCE MATERIAL") || !strings.Contains(desc, "The authoritative fact.") {
		t.Errorf("source text not injected: %q", desc)
	}
	if !strings.Contains(desc, "note.md") {
		t.Errorf("source name not injected: %q", desc)
	}
}

func TestCompileIsTopological(t *testing.T) {
	g := Graph{
		Blocks: []Block{
			{ID: "c", Kind: KindCondense},
			{ID: "a", Kind: KindFetch},
			{ID: "b", Kind: KindExtract},
		},
		Edges: []Edge{
			{From: "a", To: "b"}, {From: "b", To: "c"},
		},
	}
	specs, err := Compile(g, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for i, s := range specs {
		pos[s.Key] = i
	}
	if pos["a"] >= pos["b"] || pos["b"] >= pos["c"] {
		t.Errorf("order = %v", pos)
	}
}
