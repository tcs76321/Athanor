package planning

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

func TestParseMergesMultipleTopLevelValues(t *testing.T) {
	a := `[{"title":"A","summary":"a","tasks":[{"title":"t","description":"d","capability":"text","criteria":["c"]}]}]`
	b := `[{"title":"B","summary":"b","tasks":[{"title":"t","description":"d","capability":"text","criteria":["c"]}]}]`
	for _, raw := range []string{
		a + ", " + b,
		a + "\n" + b,
		"Here are plans:\n" + a + "\nand\n" + b + "\n",
		"```json\n" + a + "\n" + b + "\n```",
	} {
		got, err := Parse(raw)
		if err != nil {
			t.Fatalf("Parse(%q): %v", raw, err)
		}
		if len(got) != 2 || got[0].Title != "A" || got[1].Title != "B" {
			t.Fatalf("Parse(%q) = %+v, want A and B", raw, got)
		}
	}
}

func TestParseSalvagesTrailingCommaAndTruncation(t *testing.T) {
	raw := `[{"title":"A","summary":"a"},{"title":"B","summary":"b"},]`
	got, err := Parse(raw)
	if err != nil || len(got) != 2 {
		t.Fatalf("trailing comma: got %+v err %v", got, err)
	}
	raw = `[{"title":"A","summary":"a"},{"title":"B","summary":"b"},{"title":"C"`
	got, err = Parse(raw)
	if err != nil || len(got) != 2 {
		t.Fatalf("truncated array: got %+v err %v", got, err)
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

type pipelineGenerator struct {
	calls      int
	failFirst  bool
	failedOnce bool
}

func (g *pipelineGenerator) Generate(_ context.Context, req Request) (string, error) {
	g.calls++
	switch req.Phase {
	case "seed":
		seeds := []Seed{
			{Stance: StanceSmallest, Title: "Smallest slice", Summary: "The smallest useful slice.", TaskTitles: []string{"Baseline", "Deliverable", "Check"}},
			{Stance: StanceDeepest, Title: "Deepest coverage", Summary: "The deepest useful coverage.", TaskTitles: []string{"Survey", "Analyze", "Build", "Verify"}},
			{Stance: StanceLowestRisk, Title: "Lowest risk", Summary: "Retire the largest uncertainty first.", TaskTitles: []string{"Probe", "Decide", "Build"}},
		}
		return string(mustJSON(seeds)), nil
	case "expand":
		if req.Seed == nil {
			return "", fmt.Errorf("missing seed")
		}
		if g.failFirst && !g.failedOnce {
			g.failedOnce = true
			// Three tasks fail the expansion contract; the service must repair.
			bad := Candidate{Stance: req.Seed.Stance, Title: req.Seed.Title, Summary: req.Seed.Summary,
				Tasks: []Task{{Key: "t1", Title: "Only", Description: "too short", Capability: "document", Criteria: []string{"x"}}}}
			return string(mustJSON(bad)), nil
		}
		c := Candidate{
			Stance: req.Seed.Stance, Title: req.Seed.Title + " plan", Summary: req.Seed.Summary,
			Tasks: []Task{
				{Key: "t1", Title: "Establish baseline", Description: "Read the material and record the current state.", Capability: "document", Criteria: []string{"baseline recorded"}},
				{Key: "t2", Title: "Produce deliverable", Description: "Produce the first deliverable.", Capability: "document", DependsOn: []string{"t1"}, Criteria: []string{"deliverable exists"}},
				{Key: "t3", Title: "Probe risk", Description: "Check the riskiest assumption.", Capability: "document", DependsOn: []string{"t1"}, Criteria: []string{"probe recorded"}},
				{Key: "t4", Title: "Verify", Description: "Run the check and record gaps.", Capability: "document", DependsOn: []string{"t2", "t3"}, Criteria: []string{"check recorded"}},
			},
			Risks: []string{"source gaps"}, Assumptions: []string{"sources are reachable"},
		}
		return string(mustJSON(c)), nil
	}
	return "", fmt.Errorf("unexpected phase %q", req.Phase)
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestGenerateSeedsAndExpandsThreePlans(t *testing.T) {
	_, ps := openPlanStore(t)
	gen := &pipelineGenerator{}
	svc := NewService(gen, ps)
	sess, err := svc.Generate(context.Background(), "p1", "o1", Request{Goal: "a daily news brief"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if gen.calls != 4 {
		t.Fatalf("calls = %d, want seed + three expansions", gen.calls)
	}
	if sess.Status != "ready" || len(sess.Candidates) != 3 {
		t.Fatalf("session = %+v, want three ready plans", sess)
	}
	seen := map[string]bool{}
	for _, c := range sess.Candidates {
		if c.Status != "ready" || len(c.Tasks) != 4 {
			t.Fatalf("candidate = %+v, want a ready four-task plan", c)
		}
		seen[c.Stance] = true
	}
	if !seen[StanceSmallest] || !seen[StanceDeepest] || !seen[StanceLowestRisk] {
		t.Fatalf("stances = %v, want all three", seen)
	}
}

func TestGenerateRepairsOneExpansion(t *testing.T) {
	_, ps := openPlanStore(t)
	gen := &pipelineGenerator{failFirst: true}
	svc := NewService(gen, ps)
	sess, err := svc.Generate(context.Background(), "p1", "o1", Request{Goal: "a daily news brief"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if gen.calls != 5 {
		t.Fatalf("calls = %d, want seed + three expansions + one repair", gen.calls)
	}
	if sess.Status != "ready" || len(sess.Candidates) != 3 {
		t.Fatalf("session = %+v, want three ready plans", sess)
	}
}

func TestBuildPromptOmitsHumanCostAndUngroundedSignals(t *testing.T) {
	p := BuildPrompt(Request{Goal: "a daily news brief", OptionTitle: "Daily brief", Problem: "Coverage is fragmented", Scope: "World news", Deliverable: "A daily brief", Check: "Five readers review it"})
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
		Problem: "A short source-triaged update", Scope: "World news",
		Deliverable: "A daily brief", Check: "Five readers review it",
		Constraints: []string{"two hours a day"},
		Sources:     []string{"https://example.com"},
	})
	for _, want := range []string{
		"GOAL: a daily news brief", "CHOSEN DIRECTION: Daily news brief",
		"PROBLEM: A short source-triaged update", "SCOPE: World news",
		"two hours a day", "ALLOWED SOURCES", "exactly 3 objects",
		"capability must be one of",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}
