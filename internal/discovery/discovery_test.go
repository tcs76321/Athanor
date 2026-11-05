package discovery

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func sampleOptions() []Option {
	return []Option{
		{Title: "Local bookkeeping for trades", OneLiner: "Monthly books for one-person trades", Industry: "Finance", Niche: "Trades", Customer: "Electricians", Pain: "Receipts pile up", Wedge: "Done-for-you monthly close", TimeToFirstRevenueDays: 14, StartupCostUSD: 100, Confidence: 0.7},
		{Title: "Niche newsletter for lab managers", OneLiner: "Paid weekly operations brief", Industry: "Media", Niche: "Laboratories", Customer: "Lab managers", Pain: "Fragmented vendor news", Wedge: "Curated buying signals", TimeToFirstRevenueDays: 30, StartupCostUSD: 50, Confidence: 0.6},
		{Title: "Compliance checklist templates", OneLiner: "Templates for small clinics", Industry: "Healthcare", Niche: "Clinics", Customer: "Clinic owners", Pain: "Audit prep", Wedge: "Ready-to-use checklist pack", TimeToFirstRevenueDays: 10, StartupCostUSD: 20, Confidence: 0.65},
		{Title: "AI meeting notes for recruiters", OneLiner: "Structured candidate summaries", Industry: "Software", Niche: "Recruiting", Customer: "Agency recruiters", Pain: "Manual notes", Wedge: "Recruiter-specific templates", TimeToFirstRevenueDays: 21, StartupCostUSD: 200, Confidence: 0.5},
		{Title: "Local workshop booking page", OneLiner: "Booking and deposits for makers", Industry: "Education", Niche: "Craft workshops", Customer: "Workshop owners", Pain: "No-shows", Wedge: "Deposit-first booking", TimeToFirstRevenueDays: 7, StartupCostUSD: 0, Confidence: 0.75},
		{Title: "Vendor comparison service for cafés", OneLiner: "Independent supplier shortlists", Industry: "Hospitality", Niche: "Cafés", Customer: "Café owners", Pain: "Opaque supplier pricing", Wedge: "Paid shortlist + negotiation", TimeToFirstRevenueDays: 28, StartupCostUSD: 300, Confidence: 0.55},
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
		`INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','p1','document','make money online')`); err != nil {
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

func TestBuildPromptMentionsConstraintsAndHonesty(t *testing.T) {
	p := BuildPrompt(Request{
		Goal: "make money online", Brief: "2 hours a day",
		Constraints: []string{"no ads"}, Sources: []string{"https://example.com"},
	})
	for _, want := range []string{"make money online", "2 hours a day", "no ads", "example.com", "Do NOT invent"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}
