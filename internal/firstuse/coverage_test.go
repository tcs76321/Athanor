package firstuse

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/coverage"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/workflow"
	"github.com/tcs76321/athanor/migrations"
)

func TestInjectCoverageAddsForbiddenOldIDs(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	p, _, err := project.NewRepo(st).CreateInferred(context.Background(), "news", "daily news", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	ledger := coverage.NewRepo(st)
	if err := ledger.Record(context.Background(), p.ID, "news", []coverage.Item{
		{Key: "story-001"}, {Key: "story-002"},
	}); err != nil {
		t.Fatal(err)
	}

	g := workflow.Graph{Blocks: []workflow.Block{
		{ID: "synthesize", Kind: workflow.KindSynthesize, Config: map[string]any{
			"coverage_namespace": "news",
			"checks":             map[string]any{"max_words": 400},
		}},
		{ID: "retain", Kind: workflow.KindRetain, Config: map[string]any{"coverage_namespace": "news"}},
	}}
	g = injectCoverage(context.Background(), ledger, p.ID, g)
	specs, err := workflow.Compile(g, workflow.CompileOptions{CycleID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	var synth *project.TaskSpec
	for i := range specs {
		if specs[i].Key == "synthesize" {
			synth = &specs[i]
		}
	}
	if synth == nil || synth.Checks == nil {
		t.Fatalf("synthesize checks missing: %+v", specs)
	}
	if synth.Checks.MaxWords != 400 {
		t.Errorf("max_words lost: %+v", synth.Checks)
	}
	if len(synth.Checks.ForbiddenIDsInSections) != 1 {
		t.Fatalf("forbidden ids = %+v", synth.Checks.ForbiddenIDsInSections)
	}
	rule := synth.Checks.ForbiddenIDsInSections[0]
	if rule.Section != "New" || len(rule.IDs) != 2 {
		t.Errorf("rule = %+v", rule)
	}
}

func TestInjectCoverageIsNilSafe(t *testing.T) {
	g := workflow.Graph{Blocks: []workflow.Block{{ID: "synthesize", Kind: workflow.KindSynthesize}}}
	if got := injectCoverage(context.Background(), nil, "p", g); len(got.Blocks) != 1 {
		t.Fatalf("nil ledger changed the graph: %+v", got)
	}
}
