// Package cycle owns a project's timeline of bounded runs (ADR-0134). A cycle
// references a workflow template version or an inline graph; its tasks are
// persisted through internal/project with tasks.cycle_id set.
package cycle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/tcs76321/athanor/internal/ids"
	"github.com/tcs76321/athanor/internal/store"
)

// Kind is the cycle's role in the project timeline.
type Kind string

const (
	KindLegacy    Kind = "legacy"
	KindDiscovery Kind = "discovery"
	KindDesign    Kind = "design"
	KindBuild     Kind = "build"
	KindOperate   Kind = "operate"
	KindReview    Kind = "review"
)

// State is the cycle lifecycle. It mirrors the job/task posture: persist at
// every transition; a terminal cycle is never mutated back to running.
type State string

const (
	StateLegacy         State = "legacy"
	StateDraft          State = "draft"
	StateApproved       State = "approved"
	StateRunning        State = "running"
	StatePaused         State = "paused"
	StateAwaitingReview State = "awaiting_review"
	StateCompleted      State = "completed"
	StateFailed         State = "failed"
	StateCancelled      State = "cancelled"
)

// ErrNotFound is returned for an unknown cycle.
var ErrNotFound = errors.New("cycle: not found")

// Cycle is one run in a project's timeline.
type Cycle struct {
	ID              string
	ProjectID       string
	Ordinal         int
	Kind            Kind
	Goal            string
	State           State
	TemplateID      string
	TemplateVersion int
	TriggerID       string
	BudgetJSON      string
	ReviewPolicy    string
	FrameJSON       string
	GraphJSON       string
	CreatedAt       string
	UpdatedAt       string
}

// Repo persists cycles.
type Repo struct{ store *store.Store }

// NewRepo returns a repository over s.
func NewRepo(s *store.Store) *Repo { return &Repo{store: s} }

const columns = `id, project_id, ordinal, kind, goal, state, template_id,
	template_version, trigger_id, budget_json, review_policy, frame_json,
	graph_json, created_at, updated_at`

// Create appends a cycle to a project's timeline. The ordinal is the next
// unused position, so a legacy cycle 0 is never overwritten.
func (r *Repo) Create(ctx context.Context, projectID string, kind Kind, goal string) (Cycle, error) {
	if projectID == "" {
		return Cycle{}, fmt.Errorf("cycle: project id is required")
	}
	if !validKind(kind) {
		return Cycle{}, fmt.Errorf("cycle: unknown kind %q", kind)
	}
	if goal == "" {
		return Cycle{}, fmt.Errorf("cycle: goal is required")
	}
	var next int
	if err := r.store.DB().QueryRowContext(ctx,
		`SELECT COALESCE(MAX(ordinal), -1) + 1 FROM project_cycles WHERE project_id = ?`,
		projectID).Scan(&next); err != nil {
		return Cycle{}, fmt.Errorf("cycle: next ordinal: %w", err)
	}
	c := Cycle{
		ID: ids.New(), ProjectID: projectID, Ordinal: next, Kind: kind,
		Goal: goal, State: StateDraft, BudgetJSON: "{}", FrameJSON: "{}",
	}
	if _, err := r.store.DB().ExecContext(ctx,
		`INSERT INTO project_cycles (id, project_id, ordinal, kind, goal, state, budget_json, frame_json, graph_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.ProjectID, c.Ordinal, c.Kind, c.Goal, c.State, c.BudgetJSON, c.FrameJSON, "{}",
	); err != nil {
		return Cycle{}, fmt.Errorf("cycle: insert: %w", err)
	}
	return r.Get(ctx, c.ID)
}

// Get loads one cycle.
func (r *Repo) Get(ctx context.Context, id string) (Cycle, error) {
	row := r.store.DB().QueryRowContext(ctx, `SELECT `+columns+` FROM project_cycles WHERE id = ?`, id)
	c, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Cycle{}, ErrNotFound
	}
	if err != nil {
		return Cycle{}, fmt.Errorf("cycle: load: %w", err)
	}
	return c, nil
}

// ListByProject returns the timeline in ordinal order.
func (r *Repo) ListByProject(ctx context.Context, projectID string) ([]Cycle, error) {
	rows, err := r.store.DB().QueryContext(ctx,
		`SELECT `+columns+` FROM project_cycles WHERE project_id = ? ORDER BY ordinal`, projectID)
	if err != nil {
		return nil, fmt.Errorf("cycle: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Cycle
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("cycle: scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetState moves a cycle to a new lifecycle state. It is not a free-form
// update: an unknown state is rejected.
func (r *Repo) SetState(ctx context.Context, id string, state State) error {
	if !validState(state) {
		return fmt.Errorf("cycle: unknown state %q", state)
	}
	res, err := r.store.DB().ExecContext(ctx,
		`UPDATE project_cycles SET state = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`,
		state, id)
	if err != nil {
		return fmt.Errorf("cycle: set state: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetGraphRef pins the template version and trigger that produced this cycle.
func (r *Repo) SetGraphRef(ctx context.Context, id, templateID string, version int, triggerID string) error {
	if version < 0 {
		return fmt.Errorf("cycle: template version must be >= 0")
	}
	_, err := r.store.DB().ExecContext(ctx,
		`UPDATE project_cycles SET template_id = ?, template_version = ?, trigger_id = ?,
		 updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`,
		templateID, version, triggerID, id)
	if err != nil {
		return fmt.Errorf("cycle: set graph ref: %w", err)
	}
	return nil
}

// SetGraph stores a validated draft graph on the cycle. The canvas edits this
// JSON; StartDraftCycle compiles it to tasks.
func (r *Repo) SetGraph(ctx context.Context, id, graphJSON string) error {
	if graphJSON == "" {
		return fmt.Errorf("cycle: graph json is required")
	}
	res, err := r.store.DB().ExecContext(ctx,
		`UPDATE project_cycles SET graph_json = ?,
		 updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`,
		graphJSON, id)
	if err != nil {
		return fmt.Errorf("cycle: set graph: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetFrame stores the reviewed project frame and its review policy on the
// cycle. The frame is the conversation's contract: the graph and tasks are
// derived from it, and later cycles can read the decisions it records.
func (r *Repo) SetFrame(ctx context.Context, id, frameJSON, reviewPolicy string) error {
	if frameJSON == "" {
		return fmt.Errorf("cycle: frame json is required")
	}
	_, err := r.store.DB().ExecContext(ctx,
		`UPDATE project_cycles SET frame_json = ?, review_policy = ?,
		 updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`,
		frameJSON, reviewPolicy, id)
	if err != nil {
		return fmt.Errorf("cycle: set frame: %w", err)
	}
	return nil
}

func scan(row interface{ Scan(...any) error }) (Cycle, error) {
	var c Cycle
	err := row.Scan(&c.ID, &c.ProjectID, &c.Ordinal, &c.Kind, &c.Goal, &c.State,
		&c.TemplateID, &c.TemplateVersion, &c.TriggerID, &c.BudgetJSON,
		&c.ReviewPolicy, &c.FrameJSON, &c.GraphJSON, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

func validKind(k Kind) bool {
	switch k {
	case KindLegacy, KindDiscovery, KindDesign, KindBuild, KindOperate, KindReview:
		return true
	}
	return false
}

func validState(s State) bool {
	switch s {
	case StateLegacy, StateDraft, StateApproved, StateRunning, StatePaused,
		StateAwaitingReview, StateCompleted, StateFailed, StateCancelled:
		return true
	}
	return false
}
