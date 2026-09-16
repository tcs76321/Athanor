package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// TestExtractSourceURLs is the extraction corpus (ADR-0019 §7, as
// amended): absolute http(s) URLs are pulled from prose, trailing
// punctuation is stripped, duplicates dedupe, non-http schemes and
// scheme-less text are ignored, and the cap holds.
func TestExtractSourceURLs(t *testing.T) {
	cases := []struct {
		name string
		text string
		max  int
		want []string
	}{
		{"none", "a task with no urls at all", 8, nil},
		{"simple", "see https://docs.test/guide for details", 8,
			[]string{"https://docs.test/guide"}},
		{"trailing punctuation", "read https://docs.test/guide, then act.", 8,
			[]string{"https://docs.test/guide"}},
		{"trailing paren", "(see https://docs.test/guide)", 8,
			[]string{"https://docs.test/guide"}},
		{"dedupe", "https://a.test/x and https://a.test/x again", 8,
			[]string{"https://a.test/x"}},
		{"ftp ignored", "ftp://nope.test/f and https://yes.test/", 8,
			[]string{"https://yes.test/"}},
		{"cap", "https://1.test/ https://2.test/ https://3.test/", 2,
			[]string{"https://1.test/", "https://2.test/"}},
		{"max zero", "https://1.test/", 0, nil},
		{"in angle brackets", "link <https://docs.test/x> here", 8,
			[]string{"https://docs.test/x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractSourceURLs(tc.text, tc.max)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// researchFixture creates a text-archetype project whose goal carries
// the given text (the research sub-step reads p.Goal) and returns a
// diverging-state job with its loaded project and task (via the
// engine's own contexts() loader).
func researchFixture(t *testing.T, env *testEnv, goal string) (context.Context, job.Job, project.Project, project.Task) {
	t.Helper()
	_, _, jobID := env.createProjectTask(t, "text", goal)
	j, err := env.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatalf("jobs.Get: %v", err)
	}
	j.State = job.StateDiverging
	p, task, err := env.eng.contexts(context.Background(), j)
	if err != nil {
		t.Fatalf("contexts: %v", err)
	}
	return context.Background(), j, p, task
}

// researchOutcomes tallies the `research_fetch` outcomes in the job's
// event log.
func researchOutcomes(t *testing.T, env *testEnv, jobID string) map[string]int {
	t.Helper()
	rows, err := env.db.QueryEvents(context.Background(), store.EventFilter{JobID: jobID})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, r := range rows {
		var d struct {
			Event   string `json:"event"`
			Outcome string `json:"outcome"`
		}
		_ = json.Unmarshal([]byte(r.DataJSON), &d)
		if d.Event == "research_fetch" {
			out[d.Outcome]++
		}
	}
	return out
}

// TestResearchContext_NoURLsIsSilentNoOp proves a task without URLs
// produces no block and no runner calls.
func TestResearchContext_NoURLsIsSilentNoOp(t *testing.T) {
	env := newEnv(t)
	ctx, j, p, task := researchFixture(t, env, "a goal with no urls at all")

	block, err := env.eng.researchContext(ctx, j, p, task)
	if err != nil {
		t.Fatalf("researchContext: %v", err)
	}
	if block != "" {
		t.Errorf("block = %q, want empty", block)
	}
	if env.runner.CallCount() != 0 {
		t.Errorf("runner calls = %d, want 0", env.runner.CallCount())
	}
}

// TestResearchContext_FetchesAndFormats proves the happy path: each
// declared URL is fetched once, the block carries attributed source
// headings, and the audit log records the outcomes.
func TestResearchContext_FetchesAndFormats(t *testing.T) {
	env := newEnv(t)
	ctx, j, p, task := researchFixture(t, env,
		"Ground this in https://docs.test/a and https://docs.test/b.")
	env.runner.WithFetch("https://docs.test/a", "alpha markdown")

	block, err := env.eng.researchContext(ctx, j, p, task)
	if err != nil {
		t.Fatalf("researchContext: %v", err)
	}
	if !strings.Contains(block, "### Source: https://docs.test/a") ||
		!strings.Contains(block, "alpha markdown") ||
		!strings.Contains(block, "fake source markdown for https://docs.test/b") {
		t.Errorf("block = %q, want both attributed sources", block)
	}
	if !strings.HasPrefix(block, "RESEARCH SOURCES") {
		t.Errorf("block = %q, want the research preamble", block)
	}
	if got := env.runner.CallCount(); got != 2 {
		t.Errorf("runner calls = %d, want 2", got)
	}
	out := researchOutcomes(t, env, j.ID)
	if out["fetched"] != 2 {
		t.Errorf("outcomes = %v, want fetched=2", out)
	}
}

// TestResearchContext_SoftFailMatrix proves every failure mode
// soft-fails (ADR-0019 §7 as amended): denied, empty, and error rows
// are audited, the call returns nil error, and the block only carries
// what succeeded.
func TestResearchContext_SoftFailMatrix(t *testing.T) {
	env := newEnv(t)
	ctx, j, p, task := researchFixture(t, env,
		"Sources: https://ok.test/good https://empty.test/x https://err.test/e")
	env.runner.WithFetch("https://ok.test/good", "good markdown")
	env.runner.WithFetch("https://empty.test/x", "") // empty markdown → "empty" outcome
	env.eng.runner = &pickRunner{
		ToolRunner: env.runner,
		errFor:     map[string]error{"https://err.test/e": errors.New("dial: refused")},
	}

	block, err := env.eng.researchContext(ctx, j, p, task)
	if err != nil {
		t.Fatalf("researchContext: %v (soft-fail expected)", err)
	}
	if !strings.Contains(block, "good markdown") || strings.Contains(block, "err.test") {
		t.Errorf("block = %q, want only the successful source", block)
	}
	out := researchOutcomes(t, env, j.ID)
	if out["fetched"] != 1 || out["empty"] != 1 || out["error"] != 1 {
		t.Errorf("outcomes = %v, want fetched=1 empty=1 error=1", out)
	}
}

// TestResearchContext_DisallowedWhenEnvelopeLacksTool proves the
// envelope gate is observable from the engine side: the runner
// returns ErrToolDisallowed, the block is empty, and the call is a
// nil-error no-op.
func TestResearchContext_DisallowedWhenEnvelopeLacksTool(t *testing.T) {
	env := newEnv(t)
	ctx, j, p, task := researchFixture(t, env, "See https://docs.test/guide for context.")
	env.runner.mu.Lock()
	env.runner.disallow["FetchURL"] = true
	env.runner.mu.Unlock()

	block, err := env.eng.researchContext(ctx, j, p, task)
	if err != nil {
		t.Fatalf("researchContext: %v", err)
	}
	if block != "" {
		t.Errorf("block = %q, want empty (source disallowed)", block)
	}
	out := researchOutcomes(t, env, j.ID)
	if out["disallowed"] != 1 {
		t.Errorf("outcomes = %v, want disallowed=1", out)
	}
}

// pickRunner wraps a ToolRunner and fails FetchURL for specific URLs
// (the per-URL error case the global fake cannot express).
type pickRunner struct {
	ToolRunner
	errFor map[string]error
}

func (p *pickRunner) FetchURL(ctx context.Context, jobID string, req toolenvelope.FetchURLRequest) (toolenvelope.FetchURLResponse, error) {
	if err, ok := p.errFor[req.URL]; ok {
		return toolenvelope.FetchURLResponse{}, err
	}
	return p.ToolRunner.FetchURL(ctx, jobID, req)
}

// TestDiverge_InjectsResearchIntoCandidates is the phase-level
// integration proof: a job whose goal declares URLs runs the research
// sub-step inside phaseDivergeN and every divergence candidate's
// prompt carries the research block. The countingOllama records only
// call counts, so the assertion is on the audit trail: one
// research_start + one fetched row per source, and the run completes.
func TestDiverge_InjectsResearchIntoCandidates(t *testing.T) {
	env := newEnv(t)
	_, _, jobID := env.createProjectTask(t, "text",
		"Summarize the state of local-first software, grounded in https://docs.test/local-first.")
	env.runner.WithFetch("https://docs.test/local-first", "local-first means data stays on your device.")

	env.eng.Run(context.Background(), jobID)

	j, err := env.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != job.StateCompleted {
		t.Fatalf("state = %s, want completed", j.State)
	}
	out := researchOutcomes(t, env, jobID)
	if out["fetched"] != 1 {
		t.Errorf("outcomes = %v, want fetched=1 (research ran inside divergence)", out)
	}
}