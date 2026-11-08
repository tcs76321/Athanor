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
		Title: title, Summary: "A plan for " + title,
		Tasks: []Task{
			{Title: "Gather source material", Description: strings.Repeat("Collect and read the approved sources, recording what each contributes. ", 2), Capability: "text", Criteria: []string{"sources recorded"}},
			{Title: "Produce the deliverable", Description: strings.Repeat("Write the first usable deliverable and cite the approved sources. ", 2), Capability: "document", Criteria: []string{"deliverable exists"}},
			{Title: "Check it", Description: strings.Repeat("Review the deliverable against the brief and the success criteria. ", 2), Capability: "document", Criteria: []string{"gaps recorded"}},
			{Title: "Decide", Description: strings.Repeat("Record what worked, what did not, and the next step. ", 2), Capability: "document", Criteria: []string{"decision recorded"}},
		},
		Risks: []string{"incomplete sources"}, Assumptions: []string{"approved sources are reachable"},
	}
}

func TestParseValidatePreservesOrder(t *testing.T) {
	raw, err := json.Marshal([]Candidate{candidate("Smallest verifiable slice"), candidate("Deepest coverage"), candidate("Lowest-risk sequence")})
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
	validated, err := Validate(cands)
	if err != nil {
		t.Fatal(err)
	}
	if validated[0].Title != "Smallest verifiable slice" || validated[2].Title != "Lowest-risk sequence" {
		t.Fatalf("Validate reordered the plans: %+v", validated)
	}
}

func TestValidateRejectsInvalidCapability(t *testing.T) {
	cands := []Candidate{candidate("A"), candidate("B"), candidate("C")}
	cands[0].Tasks[0].Capability = "not_a_capability"
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
		`INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','p1','document','a daily news brief')`); err != nil {
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
	cands := []Candidate{candidate("Smallest slice"), candidate("Deepest"), candidate("Lowest risk")}
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
		`INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','p1','document','a daily news brief')`); err != nil {
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
		`INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','p1','document','a daily news brief')`); err != nil {
		t.Fatal(err)
	}
	return s, NewStore(s)
}

func TestBuildPromptOmitsHumanCostAndUngroundedSignals(t *testing.T) {
	p := BuildPrompt(Request{Goal: "a daily news brief", OptionTitle: "Daily brief", OneLiner: "A source-triaged update"})
	for _, unwanted := range []string{"confidence", "score", "time to first", "setup cost", "fastest", "leanest"} {
		if strings.Contains(strings.ToLower(p), unwanted) {
			t.Errorf("plan prompt still contains %q", unwanted)
		}
	}
	for _, want := range []string{"smallest verifiable slice", "deepest coverage", "lowest-risk sequence", "acceptance criterion", "Do not estimate time, cost, human effort"} {
		if !strings.Contains(p, want) {
			t.Errorf("plan prompt missing %q", want)
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
		Goal: "a daily news brief", OptionTitle: "Daily news brief",
		OneLiner: "A short source-triaged update", Constraints: []string{"two hours a day"},
		Sources: []string{"https://example.com"},
	})
	for _, want := range []string{
		"GOAL: a daily news brief", "Daily news brief — A short source-triaged update",
		"two hours a day", "ALLOWED SOURCES", "exactly 3 objects",
		"capability must be one of",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}
