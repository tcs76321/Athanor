package project

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/ids"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// CodeArchetypeTools is the default per-task tool set for a `code`
// project (M9-T4). The daemon-wide default stays `[]` (no tools), so
// network/context tools remain explicit operator decisions; a code task
// nevertheless needs the three execution tools or its real test command
// can never run. A task-level override still wins in EnvelopeFor.
func CodeArchetypeTools() []string {
	return []string{
		string(toolenvelope.ToolExecuteCode),
		string(toolenvelope.ToolRunTests),
		string(toolenvelope.ToolLint),
		// ADR-0113: a code task may ask to read one repository-relative
		// file before repairing. It cannot write or execute through it.
		string(toolenvelope.ToolReadFile),
	}
}

// defaultToolsJSON returns the task's allowed_tools_json default for an
// archetype. Empty is "[]" so the daemon-wide default applies.
func defaultToolsJSON(capability string) (string, error) {
	if capability != CapabilityCode {
		return "[]", nil
	}
	b, err := json.Marshal(CodeArchetypeTools())
	if err != nil {
		return "", fmt.Errorf("marshalling code tool defaults: %w", err)
	}
	return string(b), nil
}

// Create makes a project with its first goal and task, atomically.
// repositoryPath is the §6.1 repository root the M5-T8 indexer walks
// (ADR-0028); empty means no repository is configured.
func (r *Repo) Create(ctx context.Context, name, archetype, goal, repositoryPath string, criteria []string) (Project, Task, error) {
	if !ValidArchetype(archetype) {
		return Project{}, Task{}, fmt.Errorf("invalid archetype %q (§6.2: text|code|document|data|media)", archetype)
	}
	caps := CapabilitiesFromArchetype(archetype)
	p, task, err := r.create(ctx, name, archetype, caps, PrimaryCapability(caps), goal, repositoryPath, criteria, PriorityMedium)
	if err != nil || archetype != ArchetypeCode {
		return p, task, err
	}
	// Compatibility shim for pre-0093 callers/tests that expect the old
	// pytest default. The product path is CreateInferred, which detects the
	// repository command instead of inventing one.
	if p.Execution.TestCommand == "" {
		ex := Execution{TestCommand: DefaultCodeTestCommand}
		if serr := r.SetExecution(ctx, p.ID, ex); serr != nil {
			return Project{}, Task{}, serr
		}
		p.Execution = ex
	}
	return p, task, nil
}

// CreateInferred is the product create path (ADR-0093): the project has no
// operator-chosen archetype, and both the project capability set and the first
// task's primary capability are inferred from the goal and repository. The
// legacy archetype column is written with the primary capability only for
// compatibility.
func (r *Repo) CreateInferred(ctx context.Context, name, goal, repositoryPath string, criteria []string) (Project, Task, error) {
	return r.CreateInferredWithPriority(ctx, name, goal, repositoryPath, criteria, PriorityMedium)
}

// CreateInferredWithPriority is the product project-create path: capabilities
// are inferred and the operator's priority is set in the same transaction,
// including the exclusive critical demotion.
func (r *Repo) CreateInferredWithPriority(ctx context.Context, name, goal, repositoryPath string, criteria []string, priority string) (Project, Task, error) {
	caps := InferCapabilities(goal, repositoryPath)
	capability := InferTaskCapability(goal, caps, repositoryPath)
	return r.create(ctx, name, capability, caps, capability, goal, repositoryPath, criteria, priority)
}

// CreateBareInferred creates a project and its goal without a placeholder
// task. The guided onboarding owns the first task graph; approval creates it.
// The create-form criteria are stored in the project brief's success field so
// they survive the absence of a task.
func (r *Repo) CreateBareInferred(ctx context.Context, name, goal, repositoryPath string, criteria []string, priority string) (Project, error) {
	if err := validateGoalText(goal); err != nil {
		return Project{}, err
	}
	if name == "" {
		return Project{}, fmt.Errorf("project name must not be empty")
	}
	priority = strings.TrimSpace(priority)
	if priority == "" {
		priority = PriorityMedium
	}
	if !ValidPriority(priority) {
		return Project{}, fmt.Errorf("invalid project priority %q (critical|high|medium|low)", priority)
	}
	caps := NormalizeCapabilities(InferCapabilities(goal, repositoryPath))
	capsJSON, err := json.Marshal(caps)
	if err != nil {
		return Project{}, fmt.Errorf("marshalling capabilities: %w", err)
	}
	projectID, goalID := ids.New(), ids.New()
	tx, err := r.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return Project{}, fmt.Errorf("beginning project create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if priority == PriorityCritical {
		if _, err := tx.ExecContext(ctx,
			`UPDATE projects SET priority = ? WHERE priority = ?`, PriorityHigh, PriorityCritical); err != nil {
			return Project{}, fmt.Errorf("demoting the previous critical project: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO projects (id, name, archetype, goal, repository_path, capabilities_json, priority) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		projectID, name, PrimaryCapability(caps), goal, nullIfEmpty(repositoryPath), string(capsJSON), priority,
	); err != nil {
		return Project{}, fmt.Errorf("inserting project: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO goals (id, project_id, text) VALUES (?, ?, ?)`,
		goalID, projectID, goal,
	); err != nil {
		return Project{}, fmt.Errorf("inserting goal: %w", err)
	}
	success := strings.Join(criteria, "\n")
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO project_briefs (project_id, status, goal, success) VALUES (?, 'pending', ?, ?)`,
		projectID, goal, success,
	); err != nil {
		return Project{}, fmt.Errorf("seeding project brief: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Project{}, fmt.Errorf("committing project create: %w", err)
	}
	return r.Get(ctx, projectID)
}

// UpdateGoalAndCapabilities keeps the project's intent and inferred
// capabilities in step with a refined brief. It is one transaction so the
// project cannot be left with a new goal and a stale capability set.
func (r *Repo) UpdateGoalAndCapabilities(ctx context.Context, id, goal string, caps []string) error {
	if err := validateGoalText(goal); err != nil {
		return err
	}
	caps = NormalizeCapabilities(caps)
	capsJSON, err := json.Marshal(caps)
	if err != nil {
		return fmt.Errorf("marshalling capabilities: %w", err)
	}
	res, err := r.store.DB().ExecContext(ctx,
		`UPDATE projects SET goal = ?, capabilities_json = ? WHERE id = ?`,
		goal, string(capsJSON), id)
	if err != nil {
		return fmt.Errorf("updating project intent: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return nil
}

// UpdateCapabilities adds or replaces the project's inferred capability set.
func (r *Repo) UpdateCapabilities(ctx context.Context, id string, caps []string) error {
	caps = NormalizeCapabilities(caps)
	capsJSON, err := json.Marshal(caps)
	if err != nil {
		return fmt.Errorf("marshalling capabilities: %w", err)
	}
	_, err = r.store.DB().ExecContext(ctx,
		`UPDATE projects SET capabilities_json = ? WHERE id = ?`, string(capsJSON), id)
	return err
}

// create is the shared insert path. archetype is the legacy column value;
// primary is the first task's capability.
func (r *Repo) create(ctx context.Context, name, archetype string, caps []string, primary, goal, repositoryPath string, criteria []string, priority string) (Project, Task, error) {
	if err := validateGoalText(goal); err != nil {
		return Project{}, Task{}, err
	}
	if name == "" {
		return Project{}, Task{}, fmt.Errorf("project name must not be empty")
	}
	priority = strings.TrimSpace(priority)
	if priority == "" {
		priority = PriorityMedium
	}
	if !ValidPriority(priority) {
		return Project{}, Task{}, fmt.Errorf("invalid project priority %q (critical|high|medium|low)", priority)
	}
	caps = NormalizeCapabilities(caps)
	if !ValidCapability(primary) {
		primary = PrimaryCapability(caps)
	}
	if !ValidCapability(archetype) {
		archetype = primary
	}
	projectID, goalID, taskID := ids.New(), ids.New(), ids.New()
	criteriaJSON, err := marshalCriteria(criteria)
	if err != nil {
		return Project{}, Task{}, err
	}
	toolsJSON, err := defaultToolsJSON(primary)
	if err != nil {
		return Project{}, Task{}, err
	}
	capsJSON, err := json.Marshal(caps)
	if err != nil {
		return Project{}, Task{}, fmt.Errorf("marshalling capabilities: %w", err)
	}

	tx, err := r.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return Project{}, Task{}, fmt.Errorf("beginning project create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if priority == PriorityCritical {
		if _, err := tx.ExecContext(ctx,
			`UPDATE projects SET priority = ? WHERE priority = ?`,
			PriorityHigh, PriorityCritical); err != nil {
			return Project{}, Task{}, fmt.Errorf("demoting the previous critical project: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO projects (id, name, archetype, goal, repository_path, capabilities_json, priority) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		projectID, name, archetype, goal, nullIfEmpty(repositoryPath), string(capsJSON), priority,
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
		`INSERT INTO tasks (id, project_id, goal_id, title, acceptance_criteria_json, allowed_tools_json, capability) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		taskID, projectID, goalID, goal, criteriaJSON, toolsJSON, primary,
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
	p, err := r.Get(ctx, projectID)
	if err != nil {
		return Task{}, err
	}
	goalID, taskID := ids.New(), ids.New()
	criteriaJSON, err := marshalCriteria(criteria)
	if err != nil {
		return Task{}, err
	}
	capability := InferTaskCapability(goalText, p.Capabilities, p.RepositoryPath)
	toolsJSON, err := defaultToolsJSON(capability)
	if err != nil {
		return Task{}, err
	}
	tx, err := r.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return Task{}, fmt.Errorf("beginning goal submit: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO goals (id, project_id, text) VALUES (?, ?, ?)`,
		goalID, projectID, goalText,
	); err != nil {
		return Task{}, fmt.Errorf("inserting goal: %w", err)
	}
	// allowed_tools_json defaults to '[]' (migration 0005). See Create
	// above for the M2-T4 commit 4 widening rationale.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO tasks (id, project_id, goal_id, title, acceptance_criteria_json, allowed_tools_json, capability) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		taskID, projectID, goalID, goalText, criteriaJSON, toolsJSON, capability,
	); err != nil {
		return Task{}, fmt.Errorf("inserting task: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Task{}, fmt.Errorf("committing goal submit: %w", err)
	}
	return r.Task(ctx, taskID)
}

// Get loads one project by ID.
func (r *Repo) Get(ctx context.Context, id string) (Project, error) {
	var p Project
	var execJSON, capsJSON, createdAt string
	err := r.store.DB().QueryRowContext(ctx,
		`SELECT id, name, archetype, goal, status, priority, COALESCE(repository_path, ''), execution_json, COALESCE(capabilities_json, '[]'), created_at
		 FROM projects WHERE id = ?`, id,
	).Scan(&p.ID, &p.Name, &p.Archetype, &p.Goal, &p.Status, &p.Priority, &p.RepositoryPath, &execJSON, &capsJSON, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return Project{}, fmt.Errorf("loading project: %w", err)
	}
	if err := decodeExecution(id, execJSON, &p.Execution); err != nil {
		return Project{}, err
	}
	p.Capabilities = decodeCapabilities(capsJSON, p.Archetype)
	var perr error
	if p.CreatedAt, perr = time.Parse(time.RFC3339, createdAt); perr != nil {
		return Project{}, fmt.Errorf("parsing project timestamp %q: %w", createdAt, perr)
	}
	return p, nil
}

// decodeCapabilities reads the capability set and falls back to the legacy
// archetype for rows written before migration 0033.
func decodeCapabilities(raw, archetype string) []string {
	var caps []string
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &caps); err != nil {
			caps = nil
		}
	}
	if len(caps) == 0 {
		caps = CapabilitiesFromArchetype(archetype)
	}
	return NormalizeCapabilities(caps)
}

// decodeExecution unmarshals projects.execution_json into ex. The column is
// NOT NULL DEFAULT '{}' (migration 0016), so an empty value is defensively
// ignored rather than treated as an error.
func decodeExecution(projectID, raw string, ex *Execution) error {
	if raw == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), ex); err != nil {
		return fmt.Errorf("decoding execution config for project %s: %w", projectID, err)
	}
	return nil
}

// SetExecution records the per-project §6.2 execution override (F3-T4,
// ADR-0031). The zero value clears the override (back to archetype
// defaults). A missing project is ErrNotFound.
// SetPriority updates a project's priority. Setting a project to critical
// demotes any other critical project to high in the same transaction, so the
// single-critical invariant is never transiently violated.
func (r *Repo) SetPriority(ctx context.Context, id, priority string) error {
	if !ValidPriority(priority) {
		return fmt.Errorf("invalid project priority %q (critical|high|medium|low)", priority)
	}
	tx, err := r.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning priority update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if priority == PriorityCritical {
		if _, err := tx.ExecContext(ctx,
			`UPDATE projects SET priority = ? WHERE priority = ? AND id <> ?`,
			PriorityHigh, PriorityCritical, id); err != nil {
			return fmt.Errorf("demoting previous critical project: %w", err)
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE projects SET priority = ? WHERE id = ?`, priority, id)
	if err != nil {
		return fmt.Errorf("setting project priority: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("setting project priority: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing priority update: %w", err)
	}
	return nil
}

func (r *Repo) SetExecution(ctx context.Context, id string, ex Execution) error {
	raw, err := json.Marshal(ex)
	if err != nil {
		return fmt.Errorf("marshalling execution config: %w", err)
	}
	res, err := r.store.DB().ExecContext(ctx,
		`UPDATE projects SET execution_json = ? WHERE id = ?`, string(raw), id)
	if err != nil {
		return fmt.Errorf("setting execution config: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("setting execution config: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return nil
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

// WithRepository returns every project that has a repository_path, ordered by
// creation. limit ≤ 0 means no limit. It is the M5-T8 idle indexer's work
// list (ADR-0028 §6).
func (r *Repo) WithRepository(ctx context.Context, limit int) ([]Project, error) {
	q := `SELECT id, name, archetype, goal, status, priority, COALESCE(repository_path, ''), execution_json, COALESCE(capabilities_json, '[]'), created_at
	      FROM projects
	      WHERE repository_path IS NOT NULL AND repository_path <> ''
	      ORDER BY CASE priority WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END, created_at`
	var args []any
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := r.store.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("listing projects with repositories: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Project
	for rows.Next() {
		var p Project
		var execJSON, capsJSON, createdAt string
		if err := rows.Scan(&p.ID, &p.Name, &p.Archetype, &p.Goal, &p.Status, &p.Priority, &p.RepositoryPath, &execJSON, &capsJSON, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning project: %w", err)
		}
		if err := decodeExecution(p.ID, execJSON, &p.Execution); err != nil {
			return nil, err
		}
		p.Capabilities = decodeCapabilities(capsJSON, p.Archetype)
		var perr error
		if p.CreatedAt, perr = time.Parse(time.RFC3339, createdAt); perr != nil {
			return nil, fmt.Errorf("parsing project timestamp %q: %w", createdAt, perr)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating projects: %w", err)
	}
	return out, nil
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

// taskColumns is the shared column list for task reads, so the schema and
// the Go field mapping cannot drift.
const taskColumns = `id, project_id, COALESCE(goal_id, ''), COALESCE(parent_task_id, ''),
	title, COALESCE(description, ''), COALESCE(capability, ''), status, task_type, depends_on_json,
	acceptance_criteria_json, budget_json, priority, allowed_tools_json, retry_baseline`

// scanTask reads one row produced by taskColumns.
func scanTask(row interface{ Scan(...any) error }) (Task, error) {
	var t Task
	var depsJSON, criteriaJSON, budgetJSON, allowedToolsJSON string
	if err := row.Scan(&t.ID, &t.ProjectID, &t.GoalID, &t.ParentID, &t.Title, &t.Description,
		&t.Capability, &t.Status, &t.TaskType, &depsJSON, &criteriaJSON, &budgetJSON, &t.Priority, &allowedToolsJSON,
		&t.RetryBaseline); err != nil {
		return Task{}, err
	}
	if depsJSON != "" && depsJSON != "[]" {
		if err := json.Unmarshal([]byte(depsJSON), &t.DependsOn); err != nil {
			return Task{}, fmt.Errorf("decoding depends_on for task %s: %w", t.ID, err)
		}
	}
	if err := json.Unmarshal([]byte(criteriaJSON), &t.Criteria); err != nil {
		return Task{}, fmt.Errorf("decoding criteria for task %s: %w", t.ID, err)
	}
	if budgetJSON != "" && budgetJSON != "{}" {
		if err := json.Unmarshal([]byte(budgetJSON), &t.Budget); err != nil {
			return Task{}, fmt.Errorf("decoding budget for task %s: %w", t.ID, err)
		}
	}
	// allowed_tools_json is a JSON array; an empty '[]' decodes to a
	// non-nil empty slice, which is the "use config default" signal
	// to the runtime. We normalize to nil so the field reads cleanly
	// as "no override".
	if allowedToolsJSON != "" && allowedToolsJSON != "[]" {
		if err := json.Unmarshal([]byte(allowedToolsJSON), &t.AllowedTools); err != nil {
			return Task{}, fmt.Errorf("decoding allowed_tools for task %s: %w", t.ID, err)
		}
	}
	return t, nil
}

// JobProject resolves the project that owns a job. It is used by the
// internal API to attach the caller's project for context_swap ownership
// checks (M3-T7 finding B). An unknown job is ErrNotFound.
func (r *Repo) JobProject(ctx context.Context, jobID string) (string, error) {
	var projectID string
	err := r.store.DB().QueryRowContext(ctx,
		`SELECT project_id FROM jobs WHERE id = ?`, jobID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: %s", ErrNotFound, jobID)
	}
	if err != nil {
		return "", fmt.Errorf("loading project for job %s: %w", jobID, err)
	}
	return projectID, nil
}

// JobTask resolves a job to its owning task and the job's current
// state. It is the job-context lookup for GET /internal/v1/jobs/{id}
// (known-issues O2/F-3): the route previously loaded a *task* by a *job*
// id — always a 404 in production — and hardcoded the state to
// "running". An unknown job is ErrNotFound.
func (r *Repo) JobTask(ctx context.Context, jobID string) (Task, string, error) {
	var taskID, state string
	err := r.store.DB().QueryRowContext(ctx,
		`SELECT task_id, state FROM jobs WHERE id = ?`, jobID).Scan(&taskID, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, "", fmt.Errorf("%w: %s", ErrNotFound, jobID)
	}
	if err != nil {
		return Task{}, "", fmt.Errorf("loading task for job %s: %w", jobID, err)
	}
	t, err := r.Task(ctx, taskID)
	if err != nil {
		return Task{}, "", err
	}
	return t, state, nil
}

// Task loads one task by ID.
func (r *Repo) Task(ctx context.Context, id string) (Task, error) {
	row := r.store.DB().QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, fmt.Errorf("%w (task): %s", ErrNotFound, id)
	}
	if err != nil {
		return Task{}, fmt.Errorf("loading task: %w", err)
	}
	return t, nil
}
