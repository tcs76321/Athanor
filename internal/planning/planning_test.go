package planning

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func candidate(title string) Candidate {
	return Candidate{
		Title: title, Summary: "A 30-day plan for " + title,
		Tasks: []Task{
			{Title: "Validate demand", Description: strings.Repeat("Run five customer interviews and record evidence. ", 2), Capability: "text", Criteria: []string{"five interviews"}},
			{Title: "Build offer", Description: strings.Repeat("Write the first paid offer and a landing page. ", 2), Capability: "document", Criteria: []string{"offer exists"}},
			{Title: "Outreach", Description: strings.Repeat("Contact 25 prospects with a personal message. ", 2), Capability: "text", Criteria: []string{"25 prospects"}},
			{Title: "Decide", Description: strings.Repeat("Review evidence and decide go, adjust, or stop. ", 2), Capability: "document", Criteria: []string{"decision recorded"}},
		},
		Risks: []string{"thin demand"}, Assumptions: []string{"buyers exist"}, Confidence: 0.6,
	}
}

func TestParseValidateAndScore(t *testing.T) {
	raw, err := json.Marshal([]Candidate{candidate("Fastest validation"), candidate("Deepest quality"), candidate("Lowest cost")})
	if err != nil {
		t.Fatal(err)
	}
	cands, err := Parse(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 3 {
		t.Fatalf("parsed %d candidates", len(cands))
	}
	if _, err := Validate(cands); err != nil {
		t.Fatal(err)
	}
	Score(cands)
	for _, c := range cands {
		if c.Score <= 0 {
			t.Errorf("candidate %q has no score", c.Title)
		}
	}
}

func TestValidateRejectsInvalidCapability(t *testing.T) {
	cands := []Candidate{candidate("A"), candidate("B"), candidate("C")}
	cands[0].Tasks[0].Capability = "venture"
	if _, err := Validate(cands); err == nil {
		t.Fatal("invalid capability accepted")
	}
}

func TestValidateRequiresThreeDistinctTitles(t *testing.T) {
	cands := []Candidate{candidate("Same"), candidate("Same"), candidate("Other")}
	if _, err := Validate(cands); err == nil {
		t.Fatal("duplicate titles accepted as three plans")
	}
}

func TestStoreBeginCompleteSelect(t *testing.T) {
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
		`INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','p1','document','make money online')`); err != nil {
		t.Fatal(err)
	}
	ps := NewStore(s)
	sess, err := ps.Begin(ctx, "p1", "opt1")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Status != "generating" {
		t.Fatalf("status = %q", sess.Status)
	}
	cands := []Candidate{candidate("Fastest"), candidate("Deepest"), candidate("Cheapest")}
	Score(cands)
	if err := ps.Complete(ctx, sess.ID, cands); err != nil {
		t.Fatal(err)
	}
	got, ok, err := ps.Get(ctx, "p1")
	if err != nil || !ok {
		t.Fatalf("get: %v %v", ok, err)
	}
	if got.Status != "ready" || len(got.Candidates) != 3 {
		t.Fatalf("session = %+v", got)
	}
	if err := ps.Select(ctx, "p1", 1); err != nil {
		t.Fatal(err)
	}
	got, _, _ = ps.Get(ctx, "p1")
	if got.SelectedIndex != 1 {
		t.Fatalf("selected = %d", got.SelectedIndex)
	}
	if specs := ToTaskSpecs(got.Candidates[1]); len(specs) != len(got.Candidates[1].Tasks) || len(specs[1].DependsOn) != 1 {
		t.Fatalf("task specs = %+v", specs)
	}
}

func TestStoreFailIsDurable(t *testing.T) {
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
		`INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','p1','document','make money online')`); err != nil {
		t.Fatal(err)
	}
	ps := NewStore(s)
	sess, err := ps.Begin(ctx, "p1", "")
	if err != nil {
		t.Fatal(err)
	}
	ps.Fail(ctx, sess.ID, "model returned prose")
	got, ok, _ := ps.Get(ctx, "p1")
	if !ok || got.Status != "failed" || !strings.Contains(got.Error, "prose") {
		t.Fatalf("failed session = %+v", got)
	}
}

func openPlanStore(t *testing.T) (*store.Store, *Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(context.Background(),
		`INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','p1','document','make money online')`); err != nil {
		t.Fatal(err)
	}
	return s, NewStore(s)
}

func TestScoreClampsConfidenceAndStaysBounded(t *testing.T) {
	cands := []Candidate{candidate("A"), candidate("B"), candidate("C")}
	cands[0].Confidence = 2.5
	Score(cands)
	for _, c := range cands {
		if c.Confidence < 0 || c.Confidence > 1 {
			t.Errorf("candidate %q confidence = %v, want 0..1", c.Title, c.Confidence)
		}
		if c.Score < 0 || c.Score > 100 {
			t.Errorf("candidate %q score = %d, want 0..100", c.Title, c.Score)
		}
	}
}

func TestGetFailsLoudlyOnMalformedCandidateJSON(t *testing.T) {
	s, ps := openPlanStore(t)
	ctx := context.Background()
	sess, err := ps.Begin(ctx, "p1", "o1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `
		INSERT INTO plan_candidates (id, session_id, sort_order, title, tasks_json)
		VALUES ('c1', ?, 0, 'Broken', 'not-json')`, sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ps.Get(ctx, "p1"); err == nil {
		t.Fatal("malformed candidate JSON was silently accepted")
	}
}

func TestGetReturnsNewestSessionEvenOnTimestampTie(t *testing.T) {
	_, ps := openPlanStore(t)
	ctx := context.Background()
	if _, err := ps.Begin(ctx, "p1", "o1"); err != nil {
		t.Fatal(err)
	}
	newest, err := ps.Begin(ctx, "p1", "o2")
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := ps.Get(ctx, "p1")
	if err != nil || !ok {
		t.Fatalf("get: %v %v", ok, err)
	}
	if got.ID != newest.ID || got.OptionID != "o2" {
		t.Fatalf("got session %s/%s, want %s/o2", got.ID, got.OptionID, newest.ID)
	}
}

func TestSelectRejectsNonReadySession(t *testing.T) {
	_, ps := openPlanStore(t)
	ctx := context.Background()
	if _, err := ps.Begin(ctx, "p1", "o1"); err != nil {
		t.Fatal(err)
	}
	if err := ps.Select(ctx, "p1", 0); err == nil {
		t.Fatal("selecting a plan from a generating session was accepted")
	}
}

func TestBuildPromptIncludesDirectionConstraintsAndRules(t *testing.T) {
	got := BuildPrompt(Request{
		Goal: "make money online", OptionTitle: "Local bookkeeping",
		OneLiner: "Monthly close for trades", Constraints: []string{"two hours a day"},
		Sources: []string{"https://example.com"},
	})
	for _, want := range []string{
		"GOAL: make money online", "Local bookkeeping — Monthly close for trades",
		"two hours a day", "ALLOWED SOURCES", "exactly 3 objects",
		"capability must be one of",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}
