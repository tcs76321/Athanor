package project

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tcs76321/athanor/internal/ids"
)

// CreateDAG persists a decomposed task graph for a new goal in one
// transaction (M6-T1, ADR-0032): the goal row, one tasks row per spec with
// generated IDs, resolved parent/dependency references, the §7.3 criteria
// and budget columns, and the goals.status='decomposed' marker.
//
// A spec with a duplicate key or an unknown parent/dependency reference is
// rejected before any write. The returned tasks are in insertion (graph
// declaration) order.
func (r *Repo) CreateDAG(ctx context.Context, projectID, goalText string, criteria []string, specs []TaskSpec) (string, []Task, error) {
	if err := validateGoalText(goalText); err != nil {
		return "", nil, err
	}
	p, err := r.Get(ctx, projectID)
	if err != nil {
		return "", nil, err
	}
	if len(specs) == 0 {
		return "", nil, fmt.Errorf("decomposition has no tasks")
	}
	toolsJSON, err := defaultToolsJSON(p.Archetype)
	if err != nil {
		return "", nil, err
	}

	// Resolve keys to generated IDs first; this also rejects duplicate
	// keys and unknown references before the transaction opens.
	idsByKey := make(map[string]string, len(specs))
	for _, s := range specs {
		if s.Key == "" {
			return "", nil, fmt.Errorf("task spec has an empty key")
		}
		if _, dup := idsByKey[s.Key]; dup {
			return "", nil, fmt.Errorf("duplicate task key %q", s.Key)
		}
		idsByKey[s.Key] = ids.New()
	}
	for _, s := range specs {
		if s.ParentKey != "" {
			if _, ok := idsByKey[s.ParentKey]; !ok {
				return "", nil, fmt.Errorf("task %q references unknown parent %q", s.Key, s.ParentKey)
			}
		}
		for _, dep := range s.DependsOn {
			if _, ok := idsByKey[dep]; !ok {
				return "", nil, fmt.Errorf("task %q references unknown dependency %q", s.Key, dep)
			}
		}
	}

	goalID := ids.New()
	tx, err := r.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return "", nil, fmt.Errorf("beginning DAG create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO goals (id, project_id, text, status) VALUES (?, ?, ?, 'decomposed')`,
		goalID, projectID, goalText,
	); err != nil {
		return "", nil, fmt.Errorf("inserting goal: %w", err)
	}

	for _, s := range specs {
		var parentID any
		if s.ParentKey != "" {
			parentID = idsByKey[s.ParentKey]
		}
		depsJSON, err := json.Marshal(resolveDeps(s.DependsOn, idsByKey))
		if err != nil {
			return "", nil, fmt.Errorf("marshalling dependencies: %w", err)
		}
		criteriaJSON, err := marshalCriteria(s.Criteria)
		if err != nil {
			return "", nil, err
		}
		budgetJSON, err := json.Marshal(s.Budget)
		if err != nil {
			return "", nil, fmt.Errorf("marshalling budget: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO tasks (id, project_id, goal_id, parent_task_id, title, description,
			        depends_on_json, acceptance_criteria_json, budget_json, allowed_tools_json, priority)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			idsByKey[s.Key], projectID, goalID, parentID, s.Title, s.Description,
			string(depsJSON), criteriaJSON, string(budgetJSON), toolsJSON, s.Priority,
		); err != nil {
			return "", nil, fmt.Errorf("inserting task %q: %w", s.Key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return "", nil, fmt.Errorf("committing DAG: %w", err)
	}
	tasks, err := r.TasksByGoal(ctx, goalID)
	if err != nil {
		return "", nil, err
	}
	return goalID, tasks, nil
}

// resolveDeps maps caller-local dependency keys to generated task IDs.
func resolveDeps(keys []string, idsByKey map[string]string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, idsByKey[k])
	}
	return out
}

// TasksByGoal loads a goal's tasks in insertion (graph declaration) order.
func (r *Repo) TasksByGoal(ctx context.Context, goalID string) ([]Task, error) {
	rows, err := r.store.DB().QueryContext(ctx,
		`SELECT `+taskColumns+` FROM tasks WHERE goal_id = ? ORDER BY rowid`, goalID)
	if err != nil {
		return nil, fmt.Errorf("listing tasks for goal %s: %w", goalID, err)
	}
	defer func() { _ = rows.Close() }()
	return scanTasks(rows)
}

// TasksByProject loads every task for a project in insertion order (M6-T1:
// the DAG inspection surface). A missing project yields an empty list.
func (r *Repo) TasksByProject(ctx context.Context, projectID string) ([]Task, error) {
	rows, err := r.store.DB().QueryContext(ctx,
		`SELECT `+taskColumns+` FROM tasks WHERE project_id = ? ORDER BY rowid`, projectID)
	if err != nil {
		return nil, fmt.Errorf("listing tasks for project %s: %w", projectID, err)
	}
	defer func() { _ = rows.Close() }()
	return scanTasks(rows)
}

// scanTasks drains a tasks result set using the shared column mapping.
func scanTasks(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]Task, error) {
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning task: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating tasks: %w", err)
	}
	return out, nil
}

// ReconcilableGoalIDs returns the distinct goal IDs that still have a task in
// a status the scheduler may act on — `pending` (a crash before the first
// schedule), `ready`, or `running`. A goal that is fully terminal
// (completed/failed) or stalled on a `blocked` leaf (which only an operator
// Reset can unstick) is excluded, so the boot reconcile walks each goal once
// rather than re-walking every permanently-stalled graph (known-issues B1).
func (r *Repo) ReconcilableGoalIDs(ctx context.Context) ([]string, error) {
	rows, err := r.store.DB().QueryContext(ctx,
		`SELECT DISTINCT goal_id FROM tasks
		 WHERE goal_id IS NOT NULL AND status IN ('pending','ready','running')
		 ORDER BY goal_id`)
	if err != nil {
		return nil, fmt.Errorf("listing reconcilable goals: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning goal id: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating goal ids: %w", err)
	}
	return out, nil
}

// RecomputeGoalStatus advances a goal's status to match its task graph:
// `completed` once every task is completed; `decomposed` (in flight) while any
// task is pending/ready/running/paused, and also when only `blocked` tasks
// remain (a blocked leaf is recoverable via Reset, so the goal is not failed);
// `failed` once a task has terminally failed and nothing is in flight. It is
// idempotent and a no-op when the status is unchanged, so it never churns
// `updated_at` — which is what the Morning Digest completion window keys on.
func (r *Repo) RecomputeGoalStatus(ctx context.Context, goalID string) error {
	tasks, err := r.TasksByGoal(ctx, goalID)
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		return nil
	}
	allCompleted, inFlight, anyFailed := true, false, false
	for _, t := range tasks {
		switch t.Status {
		case TaskCompleted:
		case TaskPending, TaskReady, TaskRunning, TaskPaused:
			allCompleted, inFlight = false, true
		case TaskFailed:
			allCompleted, anyFailed = false, true
		default: // blocked
			allCompleted = false
		}
	}
	status := "decomposed"
	switch {
	case allCompleted:
		status = "completed"
	case inFlight:
		status = "decomposed"
	case anyFailed:
		status = "failed"
	}
	if _, err := r.store.DB().ExecContext(ctx,
		`UPDATE goals SET status = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
		 WHERE id = ? AND status != ?`, status, goalID, status); err != nil {
		return fmt.Errorf("recomputing goal %s status: %w", goalID, err)
	}
	return nil
}

// GoalStatus returns a goal's persisted status (§5; migration 0001 set).
func (r *Repo) GoalStatus(ctx context.Context, goalID string) (string, error) {
	var status string
	err := r.store.DB().QueryRowContext(ctx,
		`SELECT status FROM goals WHERE id = ?`, goalID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w (goal): %s", ErrNotFound, goalID)
	}
	if err != nil {
		return "", fmt.Errorf("loading goal %s status: %w", goalID, err)
	}
	return status, nil
}
