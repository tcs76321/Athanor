package decompose

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/dag"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

const validGoal = "Build a small REST endpoint with tests and documentation."

const validGraph = `{"tasks":[
  {"key":"design","title":"Design","acceptance_criteria":["design doc exists"]},
  {"key":"impl","title":"Implement","depends_on":["design"],"acceptance_criteria":["tests pass"],
   "budget":{"max_jobs":2,"max_llm_calls":6,"max_tokens":12000,"max_wall_time":"30m"}}
]}`

type fakeClient struct {
	replies []string
	errs    []error
	calls   int
}

func (f *fakeClient) Chat(_ context.Context, _ llm.Request) (llm.Response, error) {
	i := f.calls
	f.calls++
	if i < len(f.errs) && f.errs[i] != nil {
		return llm.Response{}, f.errs[i]
	}
	if i < len(f.replies) {
		return llm.Response{Content: f.replies[i], PromptTokens: 10, CompletionTokens: 20}, nil
	}
	return llm.Response{Content: "no reply"}, nil
}

func testRegistry(t *testing.T) *llm.Registry {
	t.Helper()
	r, err := llm.NewRegistry(config.Personas{
		Wide:        config.PersonaConfig{Model: "wide", ContextTarget: 65536, Temperature: ptr(0.7)},
		Tall:        config.PersonaConfig{Model: "tall", ContextTarget: 16384, Temperature: ptr(0.2)},
		Main:        config.PersonaConfig{Model: "main", ContextTarget: 32768, Temperature: ptr(0.4)},
		Security:    config.PersonaConfig{Model: "security", ContextTarget: 8192, Temperature: ptr(0.0)},
		Alternative: config.PersonaConfig{Model: "alternative", ContextTarget: 32768, Temperature: ptr(0.8)},
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return r
}

func ptr[T any](v T) *T { return &v }

func harness(t *testing.T, client LLMClient, floors config.ContextEngine) (*Decomposer, *project.Repo, *store.Store, string) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	repo := project.NewRepo(s)
	p, _, err := repo.Create(context.Background(), "demo", project.ArchetypeCode, validGoal, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	d := New(client, testRegistry(t), repo, s, floors,
		dag.Limits{MaxTasks: 10, MaxDepth: 5, MaxTotalJobs: 10}, time.Minute)
	return d, repo, s, p.ID
}

func countEvents(t *testing.T, s *store.Store, needle string) int {
	t.Helper()
	events, err := s.QueryEvents(context.Background(), store.EventFilter{Category: "jobs"})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range events {
		if strings.Contains(e.DataJSON, needle) {
			n++
		}
	}
	return n
}

func TestDecomposeTallSuccess(t *testing.T) {
	fake := &fakeClient{replies: []string{validGraph}}
	d, repo, s, projectID := harness(t, fake, config.ContextEngine{})

	res, err := d.Decompose(context.Background(), projectID, validGoal, []string{"green tests"})
	if err != nil {
		t.Fatalf("Decompose: %v", err)
	}
	if res.Persona != llm.RoleTall || res.GoalID == "" || len(res.Tasks) != 2 {
		t.Fatalf("result = %+v", res)
	}
	if fake.calls != 1 {
		t.Errorf("calls = %d, want 1", fake.calls)
	}
	tasks, err := repo.TasksByGoal(context.Background(), res.GoalID)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("TasksByGoal = %v, %v", tasks, err)
	}
	if countEvents(t, s, "dag_decomposed") != 1 {
		t.Errorf("missing dag_decomposed audit event")
	}
}

func TestDecomposeFallsBackToMain(t *testing.T) {
	fake := &fakeClient{replies: []string{"here is my answer, no json", validGraph}}
	d, _, s, projectID := harness(t, fake, config.ContextEngine{})

	res, err := d.Decompose(context.Background(), projectID, validGoal, nil)
	if err != nil {
		t.Fatalf("Decompose: %v", err)
	}
	if res.Persona != llm.RoleMain {
		t.Errorf("persona = %q, want main", res.Persona)
	}
	if fake.calls != 2 {
		t.Errorf("calls = %d, want 2", fake.calls)
	}
	if countEvents(t, s, "dag_attempt_invalid") != 1 || countEvents(t, s, "dag_decomposed") != 1 {
		t.Errorf("unexpected audit events")
	}
}

func TestDecomposeInvalidGraphFallsBackThenRejects(t *testing.T) {
	// First reply parses; second reply parses but violates coverage.
	invalidGraph := `{"tasks":[{"key":"a","title":"no criteria"}]}`
	fake := &fakeClient{replies: []string{invalidGraph, invalidGraph}}
	d, _, s, projectID := harness(t, fake, config.ContextEngine{})

	_, err := d.Decompose(context.Background(), projectID, validGoal, nil)
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("err = %v, want ErrRejected", err)
	}
	if fake.calls != 2 {
		t.Errorf("calls = %d, want 2", fake.calls)
	}
	// The project's own create goal/task exist; a rejected decomposition
	// must add none.
	var tasks int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if tasks != 1 {
		t.Errorf("tasks = %d, want 1 (nothing persisted)", tasks)
	}
	if countEvents(t, s, "dag_rejected") != 1 {
		t.Errorf("missing dag_rejected audit event")
	}
}

func TestDecomposeInfeasible(t *testing.T) {
	// `main` is subject to the coding floor; make it exceed main's target.
	// `tall` is exempt in planning, so it must fail parsing first to reach
	// the infeasible `main` attempt.
	fake := &fakeClient{replies: []string{"no json"}}
	floors := config.ContextEngine{CodingFloor: 100000}
	d, _, s, projectID := harness(t, fake, floors)

	_, err := d.Decompose(context.Background(), projectID, validGoal, nil)
	if !errors.Is(err, ErrInfeasible) {
		t.Fatalf("err = %v, want ErrInfeasible", err)
	}
	if countEvents(t, s, "dag_infeasible") != 1 {
		t.Errorf("missing dag_infeasible audit event")
	}
}

func TestDecomposeUnreachableIsNotRejected(t *testing.T) {
	fake := &fakeClient{errs: []error{llm.ErrUnreachable}}
	d, _, _, projectID := harness(t, fake, config.ContextEngine{})

	_, err := d.Decompose(context.Background(), projectID, validGoal, nil)
	if !errors.Is(err, llm.ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
	if errors.Is(err, ErrRejected) {
		t.Errorf("transport failure must not look like a rejection")
	}
	if fake.calls != 1 {
		t.Errorf("calls = %d, want 1 (no retry on connectivity)", fake.calls)
	}
}
