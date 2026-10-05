package project

import (
	"context"
	"encoding/json"
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
	if _, err := r.Get(ctx, projectID); err != nil {
		return "", nil, err
	}
	if len(specs) == 0 {
		return "", nil, fmt.Errorf("decomposition has no tasks")
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
			        depends_on_json, acceptance_criteria_json, budget_json, priority)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			idsByKey[s.Key], projectID, goalID, parentID, s.Title, s.Description,
			string(depsJSON), criteriaJSON, string(budgetJSON), s.Priority,
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
