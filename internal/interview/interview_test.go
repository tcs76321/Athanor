package interview

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func testRepo(t *testing.T) (*Repo, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(
		`INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','demo','code','write a parser')`); err != nil {
		t.Fatal(err)
	}
	return NewRepo(st), st
}

func TestInterviewIsResumableAndApproves(t *testing.T) {
	r, _ := testRepo(t)
	ctx := context.Background()
	in, err := r.Ensure(ctx, "p1", Questions("code"))
	if err != nil {
		t.Fatal(err)
	}
	if in.Status != StatusQuestions || len(in.Questions) == 0 {
		t.Fatalf("fresh interview = %+v", in)
	}

	plan := BuildPlan(PlanInput{
		Name: "demo", Archetype: "code", Goal: "write a parser",
		Criteria: []string{"tests pass"}, TestCommand: "pytest -q",
		Answers:   map[string]string{"scope": "only the parser"},
		Questions: in.Questions,
	})
	if !strings.Contains(plan, "only the parser") || !strings.Contains(plan, "pytest -q") {
		t.Errorf("plan missing answers/verification:\n%s", plan)
	}
	in, err = r.SaveAnswers(ctx, "p1", map[string]string{"scope": "only the parser"}, plan)
	if err != nil {
		t.Fatal(err)
	}
	if in.Status != StatusPlanReady || in.Plan != plan {
		t.Errorf("saved interview = %+v", in)
	}

	// A fresh Get is the resume path.
	again, ok, err := r.Get(ctx, "p1")
	if err != nil || !ok || again.Plan != plan {
		t.Fatalf("resume = (%+v, %v, %v)", again, ok, err)
	}
	if _, err := r.Decide(ctx, "p1", StatusApproved); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := r.Get(ctx, "p1"); got.Status != StatusApproved {
		t.Errorf("status = %q, want approved", got.Status)
	}
}

func TestSaveAnswersRequiresPlan(t *testing.T) {
	r, _ := testRepo(t)
	ctx := context.Background()
	if _, err := r.Ensure(ctx, "p1", Questions("text")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SaveAnswers(ctx, "p1", nil, "  "); err == nil {
		t.Error("empty plan accepted")
	}
}
