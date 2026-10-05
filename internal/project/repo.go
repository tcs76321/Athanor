package project

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tcs76321/athanor/internal/ids"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// Create makes a project with its first goal and task, atomically.
// repositoryPath is the §6.1 repository root the M5-T8 indexer walks
// (ADR-0028); empty means no repository is configured.
func (r *Repo) Create(ctx context.Context, name, archetype, goal, repositoryPath string, criteria []string) (Project, Task, error) {
	if !ValidArchetype(archetype) {
		return Project{}, Task{}, fmt.Errorf("invalid archetype %q (§6.2: text|code|document|data|media)", archetype)
	}
	if err := validateGoalText(goal); err != nil {
		return Project{}, Task{}, err
	}
	if name == "" {
		return Project{}, Task{}, fmt.Errorf("project name must not be empty")
	}
	projectID, goalID, taskID := ids.New(), ids.New(), ids.New()
	criteriaJSON, err := marshalCriteria(criteria)
	if err != nil {
		return Project{}, Task{}, err
	}

	tx, err := r.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return Project{}, Task{}, fmt.Errorf("beginning project create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO projects (id, name, archetype, goal, repository_path) VALUES (?, ?, ?, ?, ?)`,
		projectID, name, archetype, goal, nullIfEmpty(repositoryPath),
	); err != nil {
		return Project{}, Task{}, fmt.Errorf("inserting project: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO goals (id, project_id, text) VALUES (?, ?, ?)`,
		goalID, projectID, goal,
	); err != nil {
		return Project{}, Task{}, fmt.Errorf("inserting goal: %w", err)
	}
	// allowed_tools_json defaults to '[]' (migration 0005): the
	// task inherits the daemon-wide default from
	// config.job_pod.default_tools. M2-T4 commit 4 widens the
	// signature to accept a per-task override.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO tasks (id, project_id, goal_id, title, acceptance_criteria_json) VALUES (?, ?, ?, ?, ?)`,
		taskID, projectID, goalID, goal, criteriaJSON,
	); err != nil {
		return Project{}, Task{}, fmt.Errorf("inserting task: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Project{}, Task{}, fmt.Errorf("committing project create: %w", err)
	}

	p, err := r.Get(ctx, projectID)
	if err != nil {
		return Project{}, Task{}, err
	}
	task, err := r.Task(ctx, taskID)
	if err != nil {
		return Project{}, Task{}, err
	}
	return p, task, nil
}

// SubmitGoal adds a new goal (and its M1 task) to an existing project.
func (r *Repo) SubmitGoal(ctx context.Context, projectID, goalText string, criteria []string) (Task, error) {
	if err := validateGoalText(goalText); err != nil {
		return Task{}, err
	}
	if _, err := r.Get(ctx, projectID); err != nil {
		return Task{}, err
	}
	goalID, taskID := ids.New(), ids.New()
	criteriaJSON, err := marshalCriteria(criteria)
	if err != nil {
		return Task{}, err
	}
	if _, err := r.store.DB().ExecContext(ctx,
		`INSERT INTO goals (id, project_id, text) VALUES (?, ?, ?)`,
		goalID, projectID, goalText,
	); err != nil {
		return Task{}, fmt.Errorf("inserting goal: %w", err)
	}
	// allowed_tools_json defaults to '[]' (migration 0005). See Create
	// above for the M2-T4 commit 4 widening rationale.
	if _, err := r.store.DB().ExecContext(ctx,
		`INSERT INTO tasks (id, project_id, goal_id, title, acceptance_criteria_json) VALUES (?, ?, ?, ?, ?)`,
		taskID, projectID, goalID, goalText, criteriaJSON,
	); err != nil {
		return Task{}, fmt.Errorf("inserting task: %w", err)
	}
	return r.Task(ctx, taskID)
}

// Get loads one project by ID.
func (r *Repo) Get(ctx context.Context, id string) (Project, error) {
	var p Project
	var createdAt string
	err := r.store.DB().QueryRowContext(ctx,
		`SELECT id, name, archetype, goal, status, COALESCE(repository_path, ''), created_at
		 FROM projects WHERE id = ?`, id,
	).Scan(&p.ID, &p.Name, &p.Archetype, &p.Goal, &p.Status, &p.RepositoryPath, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return Project{}, fmt.Errorf("loading project: %w", err)
	}
	var perr error
	if p.CreatedAt, perr = time.Parse(time.RFC3339, createdAt); perr != nil {
		return Project{}, fmt.Errorf("parsing project timestamp %q: %w", createdAt, perr)
	}
	return p, nil
}

// SetRepositoryPath records the repository root the M5-T8 indexer walks
// (ADR-0028 §1). An empty path clears it. A missing project is ErrNotFound.
func (r *Repo) SetRepositoryPath(ctx context.Context, id, path string) error {
	res, err := r.store.DB().ExecContext(ctx,
		`UPDATE projects SET repository_path = ? WHERE id = ?`, nullIfEmpty(path), id)
	if err != nil {
		return fmt.Errorf("setting repository path: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("setting repository path: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return nil
}

// EnvelopeFor is the structural seam between the per-task tool
// override and the daemon-wide default (config.job_pod.default_tools).
// The merge rule is "task override wins if non-empty; otherwise the
// passed-in default applies". An empty override + an empty default
// is a valid configuration that yields an empty envelope.
//
// EnvelopeFor is the method that satisfies internalapi.ToolEnvLookup
// in M2-T4. The two-step lookup (job -> task) means the caller only
// needs the job ID; the repo resolves the task.
//
// Errors:
//   - ErrNotFound (wrapped) if either the job or the task is missing.
//   - toolenvelope.ErrUnknownTool (wrapped) if the task's
//     allowed_tools_json contains a closed-set violation. The CHECK
//     constraint on the column should prevent this at write time; the
//     parse error is the defense-in-depth for a hand-edited database.
func (r *Repo) EnvelopeFor(ctx context.Context, jobID string, defaultEnv toolenvelope.Envelope) (toolenvelope.Envelope, error) {
	var taskID string
	err := r.store.DB().QueryRowContext(ctx,
		`SELECT task_id FROM jobs WHERE id = ?`, jobID,
	).Scan(&taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return toolenvelope.Envelope{}, fmt.Errorf("%w: %s", ErrNotFound, jobID)
	}
	if err != nil {
		return toolenvelope.Envelope{}, fmt.Errorf("loading task for job %s: %w", jobID, err)
	}
	task, err := r.Task(ctx, taskID)
	if err != nil {
		return toolenvelope.Envelope{}, err
	}
	if len(task.AllowedTools) == 0 {
		return defaultEnv, nil
	}
	return toolenvelope.Parse(task.AllowedTools)
}

// Task loads one task by ID.
func (r *Repo) Task(ctx context.Context, id string) (Task, error) {
	var t Task
	var criteriaJSON, allowedToolsJSON string
	err := r.store.DB().QueryRowContext(ctx,
		`SELECT id, project_id, COALESCE(goal_id, ''), title, COALESCE(description, ''), status,
		        acceptance_criteria_json, allowed_tools_json
		 FROM tasks WHERE id = ?`, id,
	).Scan(&t.ID, &t.ProjectID, &t.GoalID, &t.Title, &t.Description, &t.Status, &criteriaJSON, &allowedToolsJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, fmt.Errorf("%w (task): %s", ErrNotFound, id)
	}
	if err != nil {
		return Task{}, fmt.Errorf("loading task: %w", err)
	}
	if err := json.Unmarshal([]byte(criteriaJSON), &t.Criteria); err != nil {
		return Task{}, fmt.Errorf("decoding criteria for task %s: %w", id, err)
	}
	// allowed_tools_json is a JSON array; an empty '[]' decodes to a
	// non-nil empty slice, which is the "use config default" signal
	// to the runtime. We normalize to nil so the field reads cleanly
	// as "no override".
	if allowedToolsJSON != "" && allowedToolsJSON != "[]" {
		if err := json.Unmarshal([]byte(allowedToolsJSON), &t.AllowedTools); err != nil {
			return Task{}, fmt.Errorf("decoding allowed_tools for task %s: %w", id, err)
		}
	}
	return t, nil
}
