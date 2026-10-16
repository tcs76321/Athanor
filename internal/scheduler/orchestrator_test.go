package scheduler

import (
	"context"
	"errors"
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

// H6: a duplicate terminal callback for a job that a retry has superseded
// must be ignored. It used to re-process the failure (the task is `running`
// again because of the retry), double-count the retry budget, and block the
// in-flight retry.
func TestStaleTerminalCallbackIgnored(t *testing.T) {
	sched, projects, _, eng, goalID := harness(t)
	sched.SetMaxTaskRetries(1)
	esc := &fakeEscalator{}
	sched.SetEscalator(esc)
	ctx := context.Background()

	started, _ := sched.Start(ctx, goalID)
	sched.OnJobTerminal(ctx, started[0], job.StateFailed) // triggers the retry
	if len(eng.jobs) != 2 {
		t.Fatalf("jobs after retry = %v, want 2", eng.jobs)
	}

	// A stale duplicate of the original failure arrives after the retry was
	// created. It must be a no-op: the retry stays running, the budget is
	// not double-counted, and nothing escalates.
	sched.OnJobTerminal(ctx, started[0], job.StateFailed)
	assertStatus(t, statuses(t, projects, goalID), "A", project.TaskRunning)
	if esc.calls != 0 {
		t.Fatalf("escalations = %d, want 0 (a stale callback must not block the in-flight retry)", esc.calls)
	}
	if len(eng.jobs) != 2 {
		t.Fatalf("jobs = %v, want 2 (no extra retry from a stale callback)", eng.jobs)
	}
}

// taskIDByTitle resolves a task's generated ID by its title.
func taskIDByTitle(t *testing.T, projects *project.Repo, goalID, title string) string {
	t.Helper()
	tasks, err := projects.TasksByGoal(context.Background(), goalID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.Title == title {
			return task.ID
		}
	}
	t.Fatalf("task %q not found", title)
	return ""
}

// known-issues H8: an operator reset returns the blocked task and its blocked
// dependents to pending and re-schedules the task.
func TestResetUnblocksTaskAndDependents(t *testing.T) {
	sched, projects, _, eng, goalID := harness(t)
	sched.SetMaxTaskRetries(0)
	sched.SetEscalator(&fakeEscalator{})
	ctx := context.Background()

	started, _ := sched.Start(ctx, goalID)
	sched.OnJobTerminal(ctx, started[0], job.StateFailed) // A blocked, B/C/D blocked
	if st := statuses(t, projects, goalID); st["A"] != project.TaskBlocked {
		t.Fatalf("precondition: A = %q, want blocked", st["A"])
	}

	aID := taskIDByTitle(t, projects, goalID, "A")
	if _, err := sched.Reset(ctx, aID); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	st := statuses(t, projects, goalID)
	assertStatus(t, st, "A", project.TaskRunning) // re-scheduled by the pass
	assertStatus(t, st, "B", project.TaskPending)
	assertStatus(t, st, "C", project.TaskPending)
	assertStatus(t, st, "D", project.TaskPending)
	if len(eng.jobs) != 2 {
		t.Errorf("jobs = %v, want a fresh job for A", eng.jobs)
	}
}

// known-issues H8: a reset grants a fresh retry window, so the task retries
// after the reset rather than re-blocking on the very next failure.
func TestResetGrantsFreshRetryWindow(t *testing.T) {
	sched, projects, _, eng, goalID := harness(t)
	sched.SetMaxTaskRetries(1) // allowed = 2
	sched.SetEscalator(&fakeEscalator{})
	ctx := context.Background()

	started, _ := sched.Start(ctx, goalID)                 // job 0
	sched.OnJobTerminal(ctx, started[0], job.StateFailed)  // retry -> job 1
	sched.OnJobTerminal(ctx, eng.jobs[1], job.StateFailed) // exhaust -> blocked
	assertStatus(t, statuses(t, projects, goalID), "A", project.TaskBlocked)

	aID := taskIDByTitle(t, projects, goalID, "A")
	if _, err := sched.Reset(ctx, aID); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if len(eng.jobs) != 3 {
		t.Fatalf("jobs after reset = %v, want a fresh attempt", eng.jobs)
	}
	// The reset task fails again: with a fresh window this must be a retry,
	// not a re-block (without the baseline it would exhaust immediately).
	sched.OnJobTerminal(ctx, eng.jobs[2], job.StateFailed)
	assertStatus(t, statuses(t, projects, goalID), "A", project.TaskRunning)
	if len(eng.jobs) != 4 {
		t.Errorf("jobs = %v, want a retry after the reset", eng.jobs)
	}
}

// known-issues H8: reset refuses a task that is not blocked.
func TestResetRefusesNonBlockedTask(t *testing.T) {
	sched, projects, _, _, goalID := harness(t)
	ctx := context.Background()
	_, _ = sched.Start(ctx, goalID)
	aID := taskIDByTitle(t, projects, goalID, "A") // running now
	if _, err := sched.Reset(ctx, aID); !errors.Is(err, ErrNotBlocked) {
		t.Fatalf("Reset on a running task = %v, want ErrNotBlocked", err)
	}
}

// known-issues H7: if the task-status compare-and-swap loses (the task moved
// off `pending` between the scheduling snapshot and the write), the job insert
// rolls back with it, so no orphan `queued` job is left behind.
func TestAdvanceRollsBackOnStatusConflict(t *testing.T) {
	sched, projects, jobs, _, goalID := harness(t)
	ctx := context.Background()

	// Snapshot with A pending, then move A to `running` out from under it.
	tasks, err := projects.TasksByGoal(ctx, goalID)
	if err != nil {
		t.Fatal(err)
	}
	aID := taskIDByTitle(t, projects, goalID, "A")
	if err := projects.SetTaskStatus(ctx, aID, project.TaskRunning); err != nil {
		t.Fatal(err)
	}

	started, err := sched.advance(ctx, tasks) // stale snapshot: A still reads pending
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	if len(started) != 0 {
		t.Fatalf("started = %v, want none (the CAS must lose)", started)
	}
	js, err := jobs.ByTask(ctx, aID)
	if err != nil {
		t.Fatal(err)
	}
	if len(js) != 0 {
		t.Fatalf("jobs for A = %d, want 0 (the job insert must roll back)", len(js))
	}
}

// known-issues B1: the owning goal's status advances with its task graph, so a
// fully-completed goal is reported honestly (the Morning Digest).
func TestGoalStatusAdvancesWithGraph(t *testing.T) {
	sched, projects, _, eng, goalID := harness(t)
	ctx := context.Background()
	started, err := sched.Start(ctx, goalID)
	if err != nil {
		t.Fatal(err)
	}
	goalStatus := func() string {
		t.Helper()
		s, err := projects.GoalStatus(ctx, goalID)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if got := goalStatus(); got != "decomposed" {
		t.Fatalf("goal after start = %q, want decomposed", got)
	}

	// Complete the diamond: A, then B and C, then D.
	sched.OnJobTerminal(ctx, started[0], job.StateCompleted)
	sched.OnJobTerminal(ctx, eng.jobs[1], job.StateCompleted)
	sched.OnJobTerminal(ctx, eng.jobs[2], job.StateCompleted)
	sched.OnJobTerminal(ctx, eng.jobs[3], job.StateCompleted)
	if got := goalStatus(); got != "completed" {
		t.Fatalf("goal after all tasks complete = %q, want completed", got)
	}
}
