package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
)

// Enqueuer is the engine surface the scheduler starts work through.
// *engine.Engine satisfies it; tests pass a fake.
type Enqueuer interface {
	Enqueue(jobID string)
}

// ReDecomposer is the §7.2 "attempt alternative persona re-decomposition"
// seam (ADR-0034 §3). It replaces a blocked leaf with subtasks and returns
// how many it created; a zero count or an error falls through to escalation.
// The concrete alternative-persona adapter is T3b; nil disables the attempt.
type ReDecomposer interface {
	ReDecompose(ctx context.Context, t project.Task) (int, error)
}

// Escalator is the §7.2 "escalate to HITL" seam (ADR-0034 §3). M6-T4's HITL
// queue fills it; nil means "block and audit", which is the honest default
// until the queue exists.
type Escalator interface {
	Escalate(ctx context.Context, t project.Task, reason string) error
}

// ErrNotBlocked reports that a reset was requested for a task that is not in
// the `blocked` state (known-issues H8). Reset refuses any other status so an
// operator action cannot disturb an in-flight or already-terminal task.
var ErrNotBlocked = errors.New("task is not blocked")

// Scheduler schedules a goal's DAG and advances it as jobs finish
// (ARCHITECTURE §7.2; ADR-0033, ADR-0034). One mutex serializes every
// scheduling pass so concurrent terminal callbacks cannot race a
// read-modify-write.
type Scheduler struct {
	projects    *project.Repo
	jobs        *job.Repository
	engine      Enqueuer
	events      *store.Store
	maxRetries  int
	redecompose ReDecomposer
	escalator   Escalator
	mu          sync.Mutex
}

// New wires a Scheduler. engine may be nil (tests that drive terminal
// callbacks directly and never execute a job). events is the store the
// scheduler opens its atomic job+task-status transaction on and appends audit
// events to; it must be non-nil for scheduling (known-issues H7). Retries
// default to 0 until SetMaxTaskRetries is called; the seams are nil until set.
func New(projects *project.Repo, jobs *job.Repository, engine Enqueuer, events *store.Store) *Scheduler {
	return &Scheduler{projects: projects, jobs: jobs, engine: engine, events: events}
}

// SetMaxTaskRetries sets the §7.2 retry budget (ADR-0034 §1).
func (s *Scheduler) SetMaxTaskRetries(n int) {
	if n < 0 {
		n = 0
	}
	s.maxRetries = n
}

// SetReDecomposer wires the alternative-persona re-decomposition seam.
func (s *Scheduler) SetReDecomposer(r ReDecomposer) { s.redecompose = r }

// SetEscalator wires the HITL escalation seam.
func (s *Scheduler) SetEscalator(e Escalator) { s.escalator = e }

// Start schedules the ready leaves of a goal and returns the new job IDs.
func (s *Scheduler) Start(ctx context.Context, goalID string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks, err := s.projects.TasksByGoal(ctx, goalID)
	if err != nil {
		return nil, err
	}
	return s.advance(ctx, tasks)
}

// OnJobTerminal applies a finished job's outcome to its task and advances
// the graph. It is idempotent: a task already terminal or already running
// is not disturbed, and no task receives a second job.
func (s *Scheduler) OnJobTerminal(ctx context.Context, jobID string, state job.State) {
	if state != job.StateCompleted && state != job.StateFailed && state != job.StateCancelled {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	j, err := s.jobs.Get(ctx, jobID)
	if err != nil {
		slog.Error("scheduler: loading terminal job", "job", jobID, "err", err)
		return
	}
	t, err := s.projects.Task(ctx, j.TaskID)
	if err != nil {
		slog.Error("scheduler: loading task", "task", j.TaskID, "err", err)
		return
	}
	// Only an in-flight task is ours to resolve. This makes a duplicate
	// callback a no-op and keeps the scheduler away from M1 single-task
	// jobs (whose tasks never enter the `running` state).
	if t.Status != project.TaskRunning {
		return
	}

	// H6: only the task's LATEST job's terminal event is authoritative. A
	// failed job's retry supersedes it (the task returns to running with a
	// new job), so a duplicate delivery of the superseded job's terminal
	// event must be ignored rather than re-processed — otherwise it would
	// double-count the retry budget and block the in-flight retry.
	if jobs, err := s.jobs.ByTask(ctx, t.ID); err != nil {
		slog.Error("scheduler: listing task jobs for terminal idempotency", "task", t.ID, "err", err)
		return
	} else if len(jobs) == 0 || jobs[len(jobs)-1].ID != j.ID {
		return
	}

	switch state {
	case job.StateCompleted:
		if err := s.projects.SetTaskStatus(ctx, t.ID, project.TaskCompleted); err != nil {
			slog.Error("scheduler: recording task completion", "job", jobID, "task", t.ID, "err", err)
			return
		}
	case job.StateCancelled:
		// A cancellation is a decision, not a failure: terminal, no retry.
		if err := s.projects.SetTaskStatus(ctx, t.ID, project.TaskFailed); err != nil {
			slog.Error("scheduler: recording task cancellation", "job", jobID, "task", t.ID, "err", err)
			return
		}
	default: // failed
		s.handleFailure(ctx, t)
	}

	tasks, err := s.projects.TasksByGoal(ctx, t.GoalID)
	if err != nil {
		slog.Error("scheduler: listing goal tasks", "goal", t.GoalID, "err", err)
		return
	}
	if _, err := s.advance(ctx, tasks); err != nil {
		slog.Error("scheduler: advancing graph", "goal", t.GoalID, "err", err)
	}
}

// handleFailure applies the §7.2 leaf-failure policy (ADR-0034 §1): retry
// while attempts remain, otherwise block and attempt re-decomposition, then
// escalate. The caller holds s.mu.
func (s *Scheduler) handleFailure(ctx context.Context, t project.Task) {
	jobs, err := s.jobs.ByTask(ctx, t.ID)
	if err != nil {
		slog.Error("scheduler: counting task attempts", "task", t.ID, "err", err)
		s.blockAndEscalate(ctx, t, "attempt_count_failed")
		return
	}
	attempts := len(jobs) // includes the job that just failed
	// An operator reset (Reset) pins retry_baseline to the jobs recorded at
	// reset time, granting a fresh §7.2 window (known-issues H8).
	attempts -= t.RetryBaseline
	if attempts < 0 {
		attempts = 0
	}
	allowed := s.maxRetries + 1
	cause := "retries_exhausted"
	// A task's own job budget is a tighter bound (§7.3, §7.2 budget row).
	if t.Budget.MaxJobs > 0 && t.Budget.MaxJobs < allowed {
		allowed = t.Budget.MaxJobs
		cause = "budget_exhausted"
	}
	if attempts < allowed {
		if err := s.projects.SetTaskStatus(ctx, t.ID, project.TaskPending); err != nil {
			slog.Error("scheduler: scheduling retry", "task", t.ID, "err", err)
			return
		}
		s.audit(ctx, t.ProjectID, "", map[string]any{
			"event": "task_retry", "task_id": t.ID, "attempt": attempts, "max_attempts": allowed,
		})
		return
	}
	s.blockAndEscalate(ctx, t, cause)
}

// blockAndEscalate marks an exhausted task blocked, then tries
// re-decomposition and falls back to escalation (ADR-0034 §3).
func (s *Scheduler) blockAndEscalate(ctx context.Context, t project.Task, cause string) {
	if err := s.projects.SetTaskStatus(ctx, t.ID, project.TaskBlocked); err != nil {
		slog.Error("scheduler: blocking exhausted task", "task", t.ID, "err", err)
		return
	}
	if s.redecompose != nil {
		n, err := s.redecompose.ReDecompose(ctx, t)
		if err != nil {
			slog.Error("scheduler: re-decomposition failed", "task", t.ID, "err", err)
		} else if n > 0 {
			s.audit(ctx, t.ProjectID, "", map[string]any{
				"event": "task_redecomposed", "task_id": t.ID, "subtasks": n, "cause": cause,
			})
			return
		}
	}
	s.escalate(ctx, t, cause)
}

// escalate offers the blocked task to the Escalator seam. With no seam wired
// the reason is still audited, so nothing is silent (ADR-0034 §3).
func (s *Scheduler) escalate(ctx context.Context, t project.Task, cause string) {
	reason := fmt.Sprintf("%s: task %q blocked", cause, t.Title)
	if s.escalator != nil {
		if err := s.escalator.Escalate(ctx, t, reason); err != nil {
			slog.Error("scheduler: escalation failed", "task", t.ID, "err", err)
		}
	}
	s.audit(ctx, t.ProjectID, "", map[string]any{
		"event": "task_escalated", "task_id": t.ID, "cause": cause, "reason": reason,
	})
}

// Reset is the operator recovery seam (known-issues H8): it returns a
// `blocked` task and its transitively-blocked dependents to `pending` and
// grants the task a fresh §7.2 retry window, so a goal stalled by an
// exhausted leaf is recoverable without manual database edits. It never
// touches a terminal (`completed`/`failed`) task, and it re-blocks any
// dependent whose other dependencies are still failed/blocked — Reset can
// only un-stick a graph, never run a task prematurely. Returns the IDs of
// jobs started by the resulting scheduling pass.
func (s *Scheduler) Reset(ctx context.Context, taskID string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, err := s.projects.Task(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if t.Status != project.TaskBlocked {
		return nil, fmt.Errorf("%w: task %s is %s", ErrNotBlocked, taskID, t.Status)
	}
	tasks, err := s.projects.TasksByGoal(ctx, t.GoalID)
	if err != nil {
		return nil, err
	}

	// Fresh retry window: count the task's attempts from the jobs already
	// recorded, then reset. handleFailure subtracts RetryBaseline.
	baseline := 0
	if js, err := s.jobs.ByTask(ctx, t.ID); err != nil {
		return nil, err
	} else {
		baseline = len(js)
	}
	if err := s.projects.ResetTask(ctx, t.ID, baseline); err != nil {
		return nil, err
	}

	// Return the blocked dependents to pending as well. advance re-blocks any
	// that still depend on a failed/blocked task outside this chain.
	byID := index(tasks)
	for _, id := range dependents(tasks, t.ID) {
		if byID[id].Status != project.TaskBlocked {
			continue
		}
		if err := s.projects.SetTaskStatus(ctx, id, project.TaskPending); err != nil {
			return nil, fmt.Errorf("unblocking dependent %s: %w", id, err)
		}
	}

	s.audit(ctx, t.ProjectID, "", map[string]any{
		"event": "task_reset", "task_id": t.ID, "goal_id": t.GoalID, "retry_baseline": baseline,
	})

	tasks, err = s.projects.TasksByGoal(ctx, t.GoalID)
	if err != nil {
		return nil, err
	}
	return s.advance(ctx, tasks)
}

// Reconcile applies terminal job outcomes that predate a restart and then
// schedules newly-ready leaves (§23.6; ADR-0033 §4). It is idempotent, so it
// is safe to run for every non-terminal goal at boot.
func (s *Scheduler) Reconcile(ctx context.Context, goalID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tasks, err := s.projects.TasksByGoal(ctx, goalID)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if t.Status != project.TaskRunning {
			continue
		}
		jobs, err := s.jobs.ByTask(ctx, t.ID)
		if err != nil {
			return err
		}
		if len(jobs) == 0 {
			slog.Warn("scheduler: running task has no job; leaving for operator", "task", t.ID)
			continue
		}
		switch last := jobs[len(jobs)-1].State; last {
		case job.StateCompleted:
			if err := s.projects.SetTaskStatus(ctx, t.ID, project.TaskCompleted); err != nil {
				return err
			}
		case job.StateCancelled:
			if err := s.projects.SetTaskStatus(ctx, t.ID, project.TaskFailed); err != nil {
				return err
			}
		case job.StateFailed:
			// A pre-crash failure goes through the same §7.2 policy as a
			// live one, so recovery cannot skip a retry (ADR-0034).
			s.handleFailure(ctx, t)
		}
	}
	tasks, err = s.projects.TasksByGoal(ctx, goalID)
	if err != nil {
		return err
	}
	_, err = s.advance(ctx, tasks)
	return err
}

// advance applies the §7.2 blocked closure and starts every ready leaf. The
// caller holds s.mu.
func (s *Scheduler) advance(ctx context.Context, tasks []project.Task) ([]string, error) {
	if len(tasks) == 0 {
		return nil, nil
	}
	goalID := tasks[0].GoalID

	for _, id := range Blocked(tasks) {
		if err := s.projects.SetTaskStatus(ctx, id, project.TaskBlocked); err != nil {
			return nil, fmt.Errorf("blocking task %s: %w", id, err)
		}
	}

	// Reload so Ready sees the blocked set the loop above just wrote.
	tasks, err := s.projects.TasksByGoal(ctx, goalID)
	if err != nil {
		return nil, err
	}
	byID := index(tasks)

	var started []string
	for _, id := range Ready(tasks) {
		t := byID[id]
		jobID, err := s.createJobAndRun(ctx, id, t.ProjectID)
		if err != nil {
			// A zero-row task CAS means a concurrent pass already moved the
			// task off `pending`; the transaction rolled back, so there is no
			// orphan job. Skip it and let the winner proceed (known-issues H7).
			var conc *project.ErrConcurrentTaskTransition
			if errors.As(err, &conc) {
				continue
			}
			return started, fmt.Errorf("scheduling task %s: %w", id, err)
		}
		started = append(started, jobID)
		s.audit(ctx, t.ProjectID, jobID, map[string]any{
			"event": "task_scheduled", "task_id": id, "goal_id": goalID,
		})
		if s.engine != nil {
			s.engine.Enqueue(jobID)
		}
	}
	return started, nil
}

// createJobAndRun creates a queued job for a task and marks the task `running`
// in one transaction (known-issues H7): the old two-step write could fail
// after the job insert, leaving the task `pending` with an orphan job that the
// next pass would duplicate. The task-status compare-and-swap inside the
// transaction is the atomicity guard; the caller holds s.mu.
func (s *Scheduler) createJobAndRun(ctx context.Context, taskID, projectID string) (string, error) {
	if s.events == nil {
		return "", fmt.Errorf("scheduler has no store for the atomic job+status write")
	}
	tx, err := s.events.DB().BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("beginning schedule transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	jobID, err := s.jobs.CreateTx(ctx, tx, taskID, projectID)
	if err != nil {
		return "", err
	}
	if err := s.projects.SetTaskStatusTx(ctx, tx, taskID, project.TaskPending, project.TaskRunning); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("committing schedule transaction: %w", err)
	}
	return jobID, nil
}

// audit appends a `jobs` event, nil-safe.
func (s *Scheduler) audit(ctx context.Context, projectID, jobID string, data map[string]any) {
	if s.events == nil {
		return
	}
	if _, err := s.events.AppendEvent(ctx, store.Event{
		Category: "jobs", ProjectID: projectID, JobID: jobID, Data: data,
	}); err != nil {
		slog.Error("scheduler: appending event", "event", data["event"], "err", err)
	}
}
