package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func sampleOptions() []Option {
	return []Option{
		{Title: "Daily global news brief", Outcome: "One coherent daily world-news update instead of fragmented coverage", Scope: "World news in one coherent daily update; excludes opinion columns", Method: "Group stories by region, then by consequence", Check: "Five readers mark what is unclear"},
		{Title: "CSV reconciliation CLI", Outcome: "A repeatable reconciliation report for two exports", Scope: "Analysts comparing two exports; excludes schema migration", Method: "Field-level diff with a readable report", Check: "Run it against a known mismatch"},
		{Title: "AI procurement policy memo", Outcome: "A two-page decision memo with a proposed checklist", Scope: "A two-page decision memo for the procurement committee", Method: "Compare existing policies and propose a checklist", Check: "Two policy owners review it"},
		{Title: "Local air-quality dataset", Outcome: "One month of normalized local readings plus provenance notes", Scope: "One month of local readings; excludes historical backfill", Method: "Normalize, deduplicate, and document gaps", Check: "Re-run the normalization on a second month"},
		{Title: "Water-cycle visual explainer", Outcome: "Three labeled diagrams for a secondary-school lesson", Scope: "A secondary-school lesson; excludes a full animation", Method: "Step-by-step annotated diagrams", Check: "Ask a teacher to teach from them"},
		{Title: "Battery-recycling research digest", Outcome: "A weekly digest of five annotated papers", Scope: "A small lab team; five papers per issue", Method: "Annotate claims, methods, and limits", Check: "The lab team rates its usefulness"},
	}
}

func TestParseToleratesProseAndFence(t *testing.T) {
	raw := "Here are ideas:\n```json\n[{\"title\":\"A\",\"one_liner\":\"b\"}]\n```\n"
	opts, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 1 || opts[0].Title != "A" {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestParseWrappedObject(t *testing.T) {
	raw := `{"options":[{"title":"A"}]}`
	opts, err := Parse(raw)
	if err != nil || len(opts) != 1 {
		t.Fatalf("opts=%+v err=%v", opts, err)
	}
}

func TestParseSalvagesTruncatedArray(t *testing.T) {
	raw := `[{"title":"A","one_liner":"a"},{"title":"B","one_liner":"b"},{"title":"C"`
	opts, err := Parse(raw)
	if err != nil {
		t.Fatalf("truncated array should salvage complete objects: %v", err)
	}
	if len(opts) != 2 || opts[0].Title != "A" || opts[1].Title != "B" {
		t.Fatalf("salvaged = %+v, want the two complete objects", opts)
	}
}

func TestParseAcceptsConcatenatedObjects(t *testing.T) {
	raw := `{"title":"A","one_liner":"a"}
{"title":"B","one_liner":"b"}`
	opts, err := Parse(raw)
	if err != nil {
		t.Fatalf("concatenated objects should parse: %v", err)
	}
	if len(opts) != 2 || opts[0].Title != "A" || opts[1].Title != "B" {
		t.Fatalf("parsed = %+v, want A and B", opts)
	}
}

func TestDedupeAndValidate(t *testing.T) {
	opts := Dedupe(append(sampleOptions(), sampleOptions()[0]))
	if len(opts) != 6 {
		t.Fatalf("dedupe len = %d, want 6", len(opts))
	}
	validated, err := Validate(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(validated) != 6 {
		t.Fatalf("validated len = %d, want 6", len(validated))
	}
	bad := opts[:3]
	if _, err := Validate(bad); err == nil {
		t.Fatal("Validate accepted fewer than 6 options")
	}
}

func TestValidateRequiresCoreFields(t *testing.T) {
	opts := sampleOptions()
	opts[0].Check = ""
	if _, err := Validate(opts); err == nil || !strings.Contains(err.Error(), "check") {
		t.Fatalf("missing check error = %v", err)
	}
	opts = sampleOptions()
	opts[0].Outcome = ""
	if _, err := Validate(opts); err == nil || !strings.Contains(err.Error(), "outcome") {
		t.Fatalf("missing problem error = %v", err)
	}
}

func TestValidateTruncatesAndPreservesOrder(t *testing.T) {
	opts := sampleOptions()
	for len(opts) < 20 {
		o := opts[0]
		o.Title = fmt.Sprintf("%s %d", o.Title, len(opts))
		o.Method = fmt.Sprintf("%s %d", o.Method, len(opts))
		opts = append(opts, o)
	}
	validated, err := Validate(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(validated) != 16 {
		t.Fatalf("validated len = %d, want the 16-option bound", len(validated))
	}
	if validated[0].Title != opts[0].Title {
		t.Fatal("Validate reordered the portfolio")
	}
}

func TestStoreRoundTripAndSelect(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// A project is required by the foreign key.
	if _, err := s.DB().ExecContext(ctx,
		`INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','p1','document','a daily news brief')`); err != nil {
		t.Fatal(err)
	}
	ds := NewStore(s)
	opts := sampleOptions()
	sess, err := ds.Save(ctx, "p1", "brief", opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Options) != 6 {
		t.Fatalf("saved options = %d, want 6", len(sess.Options))
	}
	if err := ds.Select(ctx, "p1", sess.Options[0].ID); err != nil {
		t.Fatal(err)
	}
	got, ok, err := ds.Get(ctx, "p1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.SelectedID != sess.Options[0].ID {
		t.Fatalf("selected = %q, want %q", got.SelectedID, sess.Options[0].ID)
	}
	if got.Options[0].Check == "" || got.Options[0].Outcome == "" {
		t.Fatal("content fields did not survive the round trip")
	}
	if !strings.Contains(got.Options[0].Title, " ") {
		t.Fatal("title did not survive the round trip")
	}
}

func TestResetRemovesSessionsAndOptionsForProject(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.DB().ExecContext(ctx,
		`INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','p1','document','daily news')`); err != nil {
		t.Fatal(err)
	}
	ds := NewStore(s)
	opts := sampleOptions()
	if _, err := ds.Save(ctx, "p1", "old brief", opts); err != nil {
		t.Fatal(err)
	}
	if err := ds.Reset(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := ds.Get(ctx, "p1"); err != nil || ok {
		t.Fatalf("session survived reset: ok=%v err=%v session=%+v", ok, err, got)
	}
	var n int
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM discovery_options`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d discovery options survived reset", n)
	}
}

func TestBeginAndFailAreDurable(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.DB().ExecContext(ctx,
		`INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','p1','document','a daily news brief')`); err != nil {
		t.Fatal(err)
	}
	ds := NewStore(s)
	sess, err := ds.Begin(ctx, "p1", "brief")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Status != "generating" {
		t.Fatalf("status = %q, want generating", sess.Status)
	}
	ds.Fail(ctx, sess.ID, "model unavailable")
	got, ok, err := ds.Get(ctx, "p1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Status != "failed" || got.Error != "model unavailable" {
		t.Fatalf("failed session = %+v", got)
	}
}

func TestBuildPromptMentionsBoundariesAndHonesty(t *testing.T) {
	p := BuildPrompt(Request{
		Goal: "a daily news brief", Boundaries: "2 hours a day",
		Sources: []string{"https://example.com"},
	})
	for _, want := range []string{"a daily news brief", "2 hours a day", "example.com", "Do NOT invent"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestBuildPromptDoesNotForceBusinessFraming(t *testing.T) {
	p := BuildPrompt(Request{Goal: "produce a daily global news report"})
	for _, want := range []string{
		"Stay inside the goal's subject",
		"outcome: what will exist",
		"method: the distinctive approach",
		"check: the concrete test",
		"Do not add a commercial or fundraising framing",
		"Do not estimate time, cost, human effort, or required skills",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	for _, unwanted := range []string{
		"first paid offer", "time to first revenue", "time_to_first_revenue",
		"business_model", "customer", "pricing",
		"time_to_first_deliverable_days", "setup_cost_usd", "skills_required",
		"confidence", "alternatives", "audience:", "first_deliverable", "first_check",
	} {
		if strings.Contains(strings.ToLower(p), unwanted) {
			t.Errorf("prompt still contains old direction instruction %q", unwanted)
		}
	}
}

func TestSanitizeDropsIncompleteDirections(t *testing.T) {
	opts := sampleOptions()
	opts[2].Outcome = ""
	opts[3].Check = ""
	cleaned, dropped := Sanitize(opts)
	if len(cleaned) != 4 || len(dropped) != 2 {
		t.Fatalf("cleaned=%d dropped=%d, want 4 and 2 (%v)", len(cleaned), len(dropped), dropped)
	}
	if _, err := Validate(cleaned); err != nil {
		t.Fatalf("four complete directions should be a usable portfolio: %v", err)
	}
	if _, err := Validate(cleaned[:3]); err == nil {
		t.Fatal("three directions passed the minimum portfolio check")
	}
}

type repairGenerator struct {
	raws  []string
	calls int
}

func (g *repairGenerator) Generate(_ context.Context, req Request) (string, error) {
	if g.calls >= len(g.raws) {
		return g.raws[len(g.raws)-1], nil
	}
	raw := g.raws[g.calls]
	if g.calls > 0 && strings.TrimSpace(req.Repair) == "" {
		return "", fmt.Errorf("retry %d had no repair hint", g.calls)
	}
	g.calls++
	return raw, nil
}

func TestGenerateRepairsARejectedPortfolioOnce(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.DB().ExecContext(ctx,
		`INSERT INTO projects (id, name, archetype, goal) VALUES ('p3','p3','document','a daily news brief')`); err != nil {
		t.Fatal(err)
	}
	broken := sampleOptions()[:4]
	broken[0].Outcome = ""
	brokenRaw, _ := json.Marshal(broken)
	goodRaw, _ := json.Marshal(sampleOptions())
	gen := &repairGenerator{raws: []string{string(brokenRaw), string(goodRaw)}}
	svc := NewService(gen, NewStore(s))
	sess, err := svc.Generate(ctx, "p3", Request{Goal: "a daily news brief"})
	if err != nil {
		t.Fatalf("repair run: %v", err)
	}
	if gen.calls != 2 {
		t.Fatalf("model calls = %d, want 2 (original + one repair)", gen.calls)
	}
	if len(sess.Options) != 6 {
		t.Fatalf("options = %d, want the repaired portfolio", len(sess.Options))
	}
}

type progressGenerator struct{ raw string }

func (g progressGenerator) Generate(_ context.Context, req Request) (string, error) {
	req.Report(ProgressEvent{Stage: "model", Detail: "fake model"})
	return g.raw, nil
}

func TestGenerateReportsDurableProgress(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.DB().ExecContext(ctx,
		`INSERT INTO projects (id, name, archetype, goal) VALUES ('p2','p2','document','a daily news brief')`); err != nil {
		t.Fatal(err)
	}
	opts := sampleOptions()
	raw, err := json.Marshal(opts)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(progressGenerator{raw: string(raw)}, NewStore(s))
	var stages []string
	_, err = svc.Generate(ctx, "p2", Request{Goal: "a daily news brief", Progress: func(ev ProgressEvent) {
		stages = append(stages, ev.Stage)
	}})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(stages, ",")
	for _, want := range []string{"session", "model", "parse", "validate", "save"} {
		if !strings.Contains(joined, want) {
			t.Errorf("progress stages = %q, missing %q", joined, want)
		}
	}
}
