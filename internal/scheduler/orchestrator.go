package scheduler

import (
	"context"
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

// Scheduler schedules a goal's DAG and advances it as jobs finish
// (ARCHITECTURE §7.2; ADR-0033). One mutex serializes every scheduling pass
// so concurrent terminal callbacks cannot race a read-modify-write.
type Scheduler struct {
	projects *project.Repo
	jobs     *job.Repository
	engine   Enqueuer
	events   *store.Store
	mu       sync.Mutex
}

// New wires a Scheduler. engine may be nil (tests that drive terminal
// callbacks directly and never execute a job); events may be nil (no audit).
func New(projects *project.Repo, jobs *job.Repository, engine Enqueuer, events *store.Store) *Scheduler {
	return &Scheduler{projects: projects, jobs: jobs, engine: engine, events: events}
}

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
	to := project.TaskCompleted
	if state != job.StateCompleted {
		to = project.TaskFailed
	}
	if err := s.projects.SetTaskStatus(ctx, j.TaskID, to); err != nil {
		slog.Error("scheduler: recording task outcome", "job", jobID, "task", j.TaskID, "err", err)
		return
	}
	t, err := s.projects.Task(ctx, j.TaskID)
	if err != nil {
		slog.Error("scheduler: loading task", "task", j.TaskID, "err", err)
		return
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
		switch jobs[len(jobs)-1].State {
		case job.StateCompleted:
			if err := s.projects.SetTaskStatus(ctx, t.ID, project.TaskCompleted); err != nil {
				return err
			}
		case job.StateFailed, job.StateCancelled:
			if err := s.projects.SetTaskStatus(ctx, t.ID, project.TaskFailed); err != nil {
				return err
			}
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
		j, err := s.jobs.Create(ctx, id, t.ProjectID)
		if err != nil {
			return started, fmt.Errorf("creating job for task %s: %w", id, err)
		}
		if err := s.projects.SetTaskStatus(ctx, id, project.TaskRunning); err != nil {
			return started, fmt.Errorf("marking task %s running: %w", id, err)
		}
		started = append(started, j.ID)
		s.audit(ctx, t.ProjectID, j.ID, map[string]any{
			"event": "task_scheduled", "task_id": id, "goal_id": goalID,
		})
		if s.engine != nil {
			s.engine.Enqueue(j.ID)
		}
	}
	return started, nil
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
