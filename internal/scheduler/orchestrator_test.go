package scheduler

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

type fakeEnqueuer struct{ jobs []string }

func (f *fakeEnqueuer) Enqueue(id string) { f.jobs = append(f.jobs, id) }

func diamondSpecs() []project.TaskSpec {
	return []project.TaskSpec{
		{Key: "A", Title: "A", Criteria: []string{"a"}},
		{Key: "B", Title: "B", DependsOn: []string{"A"}, Criteria: []string{"b"}},
		{Key: "C", Title: "C", DependsOn: []string{"A"}, Criteria: []string{"c"}},
		{Key: "D", Title: "D", DependsOn: []string{"B", "C"}, Criteria: []string{"d"}},
	}
}

func harness(t *testing.T) (*Scheduler, *project.Repo, *job.Repository, *fakeEnqueuer, string) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	projects := project.NewRepo(s)
	jobs := job.NewRepository(s)
	const goal = "Build a small REST endpoint with tests and documentation."
	p, _, err := projects.Create(context.Background(), "demo", project.ArchetypeCode, goal, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	goalID, _, err := projects.CreateDAG(context.Background(), p.ID, goal, nil, diamondSpecs())
	if err != nil {
		t.Fatal(err)
	}
	eng := &fakeEnqueuer{}
	return New(projects, jobs, eng, s), projects, jobs, eng, goalID
}

func statuses(t *testing.T, projects *project.Repo, goalID string) map[string]string {
	t.Helper()
	tasks, err := projects.TasksByGoal(context.Background(), goalID)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]string, len(tasks))
	for _, task := range tasks {
		out[task.Title] = task.Status
	}
	return out
}

func assertStatus(t *testing.T, got map[string]string, title, want string) {
	t.Helper()
	if got[title] != want {
		t.Errorf("%s status = %q, want %q (all: %v)", title, got[title], want, got)
	}
}

func TestDiamondExecutesInDependencyOrder(t *testing.T) {
	sched, projects, _, eng, goalID := harness(t)
	ctx := context.Background()

	started, err := sched.Start(ctx, goalID)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(started) != 1 || len(eng.jobs) != 1 {
		t.Fatalf("initial schedule = %v (jobs %v), want only A", started, eng.jobs)
	}
	st := statuses(t, projects, goalID)
	assertStatus(t, st, "A", project.TaskRunning)
	assertStatus(t, st, "B", project.TaskPending)

	// A completes: B and C become ready together.
	sched.OnJobTerminal(ctx, started[0], job.StateCompleted)
	st = statuses(t, projects, goalID)
	assertStatus(t, st, "A", project.TaskCompleted)
	assertStatus(t, st, "B", project.TaskRunning)
	assertStatus(t, st, "C", project.TaskRunning)
	assertStatus(t, st, "D", project.TaskPending)
	if len(eng.jobs) != 3 {
		t.Fatalf("jobs after A = %v, want 3", eng.jobs)
	}

	// Complete B and C: D becomes ready.
	sched.OnJobTerminal(ctx, eng.jobs[1], job.StateCompleted)
	sched.OnJobTerminal(ctx, eng.jobs[2], job.StateCompleted)
	st = statuses(t, projects, goalID)
	assertStatus(t, st, "D", project.TaskRunning)
	if len(eng.jobs) != 4 {
		t.Fatalf("jobs after B,C = %v, want 4", eng.jobs)
	}

	sched.OnJobTerminal(ctx, eng.jobs[3], job.StateCompleted)
	assertStatus(t, statuses(t, projects, goalID), "D", project.TaskCompleted)
}

func TestFailureBlocksDescendants(t *testing.T) {
	sched, projects, _, eng, goalID := harness(t)
	ctx := context.Background()
	started, err := sched.Start(ctx, goalID)
	if err != nil {
		t.Fatal(err)
	}

	sched.OnJobTerminal(ctx, started[0], job.StateFailed)
	st := statuses(t, projects, goalID)
	assertStatus(t, st, "A", project.TaskFailed)
	assertStatus(t, st, "B", project.TaskBlocked)
	assertStatus(t, st, "C", project.TaskBlocked)
	assertStatus(t, st, "D", project.TaskBlocked)
	if len(eng.jobs) != 1 {
		t.Errorf("jobs after failure = %v, want no new work", eng.jobs)
	}
}

func TestTerminalCallbackIsIdempotent(t *testing.T) {
	sched, projects, _, eng, goalID := harness(t)
	ctx := context.Background()
	started, _ := sched.Start(ctx, goalID)

	sched.OnJobTerminal(ctx, started[0], job.StateCompleted)
	sched.OnJobTerminal(ctx, started[0], job.StateCompleted)
	if len(eng.jobs) != 3 {
		t.Fatalf("jobs = %v, want 3 (no duplicate scheduling)", eng.jobs)
	}
	assertStatus(t, statuses(t, projects, goalID), "A", project.TaskCompleted)
}

func TestReconcilePicksUpTerminalJob(t *testing.T) {
	sched, projects, jobs, _, goalID := harness(t)
	ctx := context.Background()
	started, err := sched.Start(ctx, goalID)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a crash: the job finished while the daemon was down, so the
	// task is still `running` with a `failed` job.
	if _, err := jobs.Transition(ctx, started[0], job.StateContextBuilding); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Transition(ctx, started[0], job.StateFailed); err != nil {
		t.Fatal(err)
	}

	if err := sched.Reconcile(ctx, goalID); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	st := statuses(t, projects, goalID)
	assertStatus(t, st, "A", project.TaskFailed)
	assertStatus(t, st, "B", project.TaskBlocked)
	assertStatus(t, st, "D", project.TaskBlocked)
}
