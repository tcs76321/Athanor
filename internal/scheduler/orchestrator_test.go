package scheduler

import (
	"context"
	"path/filepath"
	"strings"
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
	return harnessWith(t, diamondSpecs())
}

func harnessWith(t *testing.T, specs []project.TaskSpec) (*Scheduler, *project.Repo, *job.Repository, *fakeEnqueuer, string) {
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
	goalID, _, err := projects.CreateDAG(context.Background(), p.ID, goal, nil, specs)
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
	assertStatus(t, st, "A", project.TaskBlocked)
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
	assertStatus(t, st, "A", project.TaskBlocked)
	assertStatus(t, st, "B", project.TaskBlocked)
	assertStatus(t, st, "D", project.TaskBlocked)
}

type fakeReDecomposer struct {
	n     int
	err   error
	calls int
}

func (f *fakeReDecomposer) ReDecompose(_ context.Context, _ project.Task) (int, error) {
	f.calls++
	return f.n, f.err
}

type fakeEscalator struct {
	calls  int
	reason string
}

func (f *fakeEscalator) Escalate(_ context.Context, _ project.Task, reason string) error {
	f.calls++
	f.reason = reason
	return nil
}

func budgetedSpecs() []project.TaskSpec {
	specs := diamondSpecs()
	specs[0].Budget.MaxJobs = 1 // A gets exactly one attempt
	return specs
}

// §7.2 row 1 (happy retry): a transient failure is retried and can succeed.
func TestRetryThenSuccess(t *testing.T) {
	sched, projects, _, eng, goalID := harness(t)
	sched.SetMaxTaskRetries(1)
	esc := &fakeEscalator{}
	sched.SetEscalator(esc)
	ctx := context.Background()

	started, _ := sched.Start(ctx, goalID)
	sched.OnJobTerminal(ctx, started[0], job.StateFailed)
	if len(eng.jobs) != 2 {
		t.Fatalf("jobs after retry = %v, want a second attempt", eng.jobs)
	}
	assertStatus(t, statuses(t, projects, goalID), "A", project.TaskRunning)

	sched.OnJobTerminal(ctx, eng.jobs[1], job.StateCompleted)
	assertStatus(t, statuses(t, projects, goalID), "A", project.TaskCompleted)
	if esc.calls != 0 {
		t.Errorf("escalated on a successful retry")
	}
}

// §7.2 row 1 (exhausted): after the retry budget the task blocks and escalates.
func TestRetryExhaustionBlocksAndEscalates(t *testing.T) {
	sched, projects, _, eng, goalID := harness(t)
	sched.SetMaxTaskRetries(1)
	esc := &fakeEscalator{}
	sched.SetEscalator(esc)
	ctx := context.Background()

	started, _ := sched.Start(ctx, goalID)
	sched.OnJobTerminal(ctx, started[0], job.StateFailed)  // retry
	sched.OnJobTerminal(ctx, eng.jobs[1], job.StateFailed) // exhaust
	st := statuses(t, projects, goalID)
	assertStatus(t, st, "A", project.TaskBlocked)
	assertStatus(t, st, "B", project.TaskBlocked)
	assertStatus(t, st, "D", project.TaskBlocked)
	if esc.calls != 1 {
		t.Fatalf("escalations = %d, want 1", esc.calls)
	}
	if !strings.Contains(esc.reason, "retries_exhausted") {
		t.Errorf("escalation reason = %q, want retries_exhausted", esc.reason)
	}
}

// §7.2 row 3: a task's own budget is a tighter bound than the retry budget,
// and exhaustion is reported as a budget cause.
func TestBudgetExhaustionBlocksAndEscalates(t *testing.T) {
	sched, projects, _, _, goalID := harnessWith(t, budgetedSpecs())
	sched.SetMaxTaskRetries(5) // deliberately looser than the task budget
	esc := &fakeEscalator{}
	sched.SetEscalator(esc)
	ctx := context.Background()

	started, _ := sched.Start(ctx, goalID)
	sched.OnJobTerminal(ctx, started[0], job.StateFailed)
	assertStatus(t, statuses(t, projects, goalID), "A", project.TaskBlocked)
	if esc.calls != 1 || !strings.Contains(esc.reason, "budget_exhausted") {
		t.Fatalf("escalation = %d reason %q, want one budget_exhausted", esc.calls, esc.reason)
	}
}

// §7.2 row 1: a wired ReDecomposer is attempted before escalation.
func TestReDecompositionPrecedesEscalation(t *testing.T) {
	sched, projects, _, _, goalID := harness(t)
	sched.SetMaxTaskRetries(0)
	rd := &fakeReDecomposer{n: 2}
	esc := &fakeEscalator{}
	sched.SetReDecomposer(rd)
	sched.SetEscalator(esc)
	ctx := context.Background()

	started, _ := sched.Start(ctx, goalID)
	sched.OnJobTerminal(ctx, started[0], job.StateFailed)
	assertStatus(t, statuses(t, projects, goalID), "A", project.TaskBlocked)
	if rd.calls != 1 {
		t.Errorf("re-decomposer calls = %d, want 1", rd.calls)
	}
	if esc.calls != 0 {
		t.Errorf("escalated despite successful re-decomposition")
	}
}

// A cancelled job is terminal: no retry, even with retries configured.
func TestCancelledJobFailsTaskWithoutRetry(t *testing.T) {
	sched, projects, _, eng, goalID := harness(t)
	sched.SetMaxTaskRetries(5)
	sched.SetEscalator(&fakeEscalator{})
	ctx := context.Background()

	started, _ := sched.Start(ctx, goalID)
	sched.OnJobTerminal(ctx, started[0], job.StateCancelled)
	assertStatus(t, statuses(t, projects, goalID), "A", project.TaskFailed)
	if len(eng.jobs) != 1 {
		t.Errorf("jobs = %v, want no retry on cancellation", eng.jobs)
	}
}
