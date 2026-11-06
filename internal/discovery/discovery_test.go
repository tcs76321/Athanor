package discovery

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func sampleOptions() []Option {
	return []Option{
		{Title: "Daily global news brief", OneLiner: "A short, source-triaged world brief", Domain: "Media", Focus: "World news", Audience: "Readers who want one coherent update", Need: "Coverage is fragmented across outlets", Approach: "Group stories by region, then by consequence", Format: "Daily brief", Availability: "Public", FirstDeliverable: "One-page brief with source links", FirstCheck: "Five readers mark what is unclear", TimeToFirstDeliverableDays: 14, SetupCostUSD: 0, Confidence: 0.7},
		{Title: "CSV reconciliation CLI", OneLiner: "A local tool that explains row differences", Domain: "Software", Focus: "Data operations", Audience: "Analysts comparing exports", Need: "Manual reconciliation is slow and error-prone", Approach: "Field-level diff with a readable report", Format: "Command-line tool", Availability: "Internal", FirstDeliverable: "Working prototype on two sample files", FirstCheck: "Run it against a known mismatch", TimeToFirstDeliverableDays: 30, SetupCostUSD: 0, Confidence: 0.6},
		{Title: "AI procurement policy memo", OneLiner: "A decision memo for internal tool purchases", Domain: "Public policy", Focus: "Procurement", Audience: "Procurement staff", Need: "Current rules are unclear and inconsistent", Approach: "Compare existing policies and propose a checklist", Format: "Memo", Availability: "Internal", FirstDeliverable: "Two-page memo with a draft checklist", FirstCheck: "Review with two policy owners", TimeToFirstDeliverableDays: 10, SetupCostUSD: 0, Confidence: 0.65},
		{Title: "Local air-quality dataset", OneLiner: "Normalized readings with provenance notes", Domain: "Environment", Focus: "Air quality", Audience: "Local researchers", Need: "Readings are scattered across formats", Approach: "Normalize, deduplicate, and document gaps", Format: "Dataset plus notes", Availability: "Public", FirstDeliverable: "One month of cleaned readings", FirstCheck: "Re-run the normalization on a second month", TimeToFirstDeliverableDays: 21, SetupCostUSD: 0, Confidence: 0.5},
		{Title: "Water-cycle visual explainer", OneLiner: "Annotated diagrams for a school lesson", Domain: "Education", Focus: "Science communication", Audience: "Secondary-school students", Need: "The process is abstract without visuals", Approach: "Step-by-step annotated diagrams", Format: "Image set", Availability: "Public", FirstDeliverable: "Three labeled diagrams", FirstCheck: "Ask a teacher to teach from them", TimeToFirstDeliverableDays: 7, SetupCostUSD: 0, Confidence: 0.75},
		{Title: "Battery-recycling research digest", OneLiner: "A weekly annotated paper digest", Domain: "Research", Focus: "Materials", Audience: "A small lab team", Need: "Relevant papers are easy to miss", Approach: "Annotate claims, methods, and limits", Format: "Weekly digest", Availability: "Internal", FirstDeliverable: "First digest with five papers", FirstCheck: "The lab team rates its usefulness", TimeToFirstDeliverableDays: 28, SetupCostUSD: 0, Confidence: 0.55},
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
	if err := Validate(opts); err != nil {
		t.Fatal(err)
	}
	bad := opts[:3]
	if err := Validate(bad); err == nil {
		t.Fatal("Validate accepted fewer than 6 options")
	}
}

func TestScoreIsDeterministicAndOrdered(t *testing.T) {
	opts := sampleOptions()
	Score(opts)
	for _, o := range opts {
		if o.Scores["total"] <= 0 {
			t.Fatalf("option %q has no score", o.Title)
		}
	}
	if opts[0].Scores["total"] < opts[len(opts)-1].Scores["total"] {
		t.Fatal("Score did not sort by total descending")
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
	Score(opts)
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
	if got.Options[0].Scores["total"] == 0 {
		t.Fatal("scores did not survive the round trip")
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
	Score(opts)
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

func TestBuildPromptMentionsConstraintsAndHonesty(t *testing.T) {
	p := BuildPrompt(Request{
		Goal: "a daily news brief", Brief: "2 hours a day",
		Constraints: []string{"no ads"}, Sources: []string{"https://example.com"},
	})
	for _, want := range []string{"a daily news brief", "2 hours a day", "no ads", "example.com", "Do NOT invent"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestBuildPromptDoesNotForceBusinessFraming(t *testing.T) {
	p := BuildPrompt(Request{Goal: "produce a daily global news report"})
	for _, want := range []string{
		"Stay inside the goal's subject",
		"audience: who the work is for",
		"format: the delivery format or operating model",
		"first_deliverable",
		"time_to_first_deliverable_days",
		"Do not add a commercial or fundraising framing",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	for _, unwanted := range []string{
		"first paid offer", "time to first revenue", "time_to_first_revenue",
		"business_model", "customer", "pricing",
	} {
		if strings.Contains(strings.ToLower(p), unwanted) {
			t.Errorf("prompt still contains business-only instruction %q", unwanted)
		}
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
	Score(opts)
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
	for _, want := range []string{"session", "model", "parse", "validate", "score", "save"} {
		if !strings.Contains(joined, want) {
			t.Errorf("progress stages = %q, missing %q", joined, want)
		}
	}
}
