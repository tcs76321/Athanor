// Package interview is the F3 first-use project interview: a small,
// resumable question set and a deterministic plan/brief the operator approves
// before work starts. It is intentionally model-free: the plan is assembled
// from the project goal, criteria, execution command, and the operator's
// answers, so approval is about intent, not a generated artifact's wording.
package interview

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tcs76321/athanor/internal/store"
)

// Status is the interview lifecycle.
const (
	StatusQuestions = "questions"
	StatusPlanReady = "plan_ready"
	StatusApproved  = "approved"
	StatusSkipped   = "skipped"
)

// ErrNotFound reports a project with no interview row.
var ErrNotFound = errors.New("interview: not found")

// Question is one clarifying prompt. Required questions must be answered
// before a plan is produced; the UI may still allow "skip the interview".
type Question struct {
	ID       string `json:"id"`
	Prompt   string `json:"prompt"`
	Help     string `json:"help,omitempty"`
	Required bool   `json:"required,omitempty"`
}

// Interview is one project's resumable state.
type Interview struct {
	ProjectID string            `json:"project_id"`
	Status    string            `json:"status"`
	Questions []Question        `json:"questions"`
	Answers   map[string]string `json:"answers"`
	Plan      string            `json:"plan_md"`
}

// Repo persists interviews in the daemon's SQLite database.
type Repo struct{ db *store.Store }

func NewRepo(s *store.Store) *Repo { return &Repo{db: s} }

// Questions returns the archetype-appropriate question set. The defaults are
// deliberately short: the interview must be skippable and should not become a
// second goal-definition UI.
func Questions(archetype string) []Question {
	switch archetype {
	case "code":
		return []Question{
			{ID: "scope", Prompt: "What is explicitly out of scope for this change?", Help: "One or two sentences; prevents unrelated rewrites."},
			{ID: "verify", Prompt: "What command proves the change works?", Help: "The resolved test command is already shown; add any extra check."},
			{ID: "risk", Prompt: "What must not break?", Help: "Public APIs, data, compatibility, or a specific workflow."},
		}
	case "data":
		return []Question{
			{ID: "schema", Prompt: "Is the target schema fixed, or may it change?", Required: true},
			{ID: "source", Prompt: "Where does the input data come from?"},
			{ID: "quality", Prompt: "What counts as an invalid row, and what should happen to it?"},
		}
	case "document", "text":
		return []Question{
			{ID: "audience", Prompt: "Who is the reader, and what should they do after reading?", Required: true},
			{ID: "sources", Prompt: "Which sources or facts must be preserved exactly?"},
			{ID: "tone", Prompt: "Any required style, length, or format constraints?"},
		}
	default:
		return []Question{
			{ID: "scope", Prompt: "What is in scope, and what is explicitly out of scope?", Required: true},
			{ID: "constraints", Prompt: "What constraints or assets must be respected?"},
		}
	}
}

// PlanInput is everything the deterministic plan needs.
type PlanInput struct {
	Name        string
	Archetype   string
	Goal        string
	Criteria    []string
	TestCommand string
	Answers     map[string]string
	Questions   []Question
}

// BuildPlan renders a reviewable Markdown brief. It is deterministic: the same
// inputs produce the same bytes, and no model call is involved.
func BuildPlan(in PlanInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Plan: %s\n\n", in.Name)
	fmt.Fprintf(&b, "**Archetype:** %s\n\n", in.Archetype)
	b.WriteString("## Goal\n\n")
	b.WriteString(strings.TrimSpace(in.Goal))
	b.WriteString("\n\n## Acceptance criteria\n\n")
	if len(in.Criteria) == 0 {
		b.WriteString("- (none declared; the operator should add them before approval)\n")
	} else {
		for _, c := range in.Criteria {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	b.WriteString("\n## Clarifications\n\n")
	answered := 0
	for _, q := range in.Questions {
		a := strings.TrimSpace(in.Answers[q.ID])
		if a == "" {
			continue
		}
		answered++
		fmt.Fprintf(&b, "- **%s** — %s\n", q.Prompt, a)
	}
	if answered == 0 {
		b.WriteString("- (none; the operator skipped the interview)\n")
	}
	b.WriteString("\n## Verification\n\n")
	if strings.TrimSpace(in.TestCommand) == "" {
		b.WriteString("- No deterministic test command declared. Acceptance will rely on declared checks or the advisory judge.\n")
	} else {
		fmt.Fprintf(&b, "- `%s` must pass in the Job Pod before acceptance.\n", in.TestCommand)
	}
	b.WriteString("\n## Execution\n\n")
	b.WriteString("1. Build the context bundle and plan the work.\n")
	b.WriteString("2. Generate and evaluate candidates against the criteria.\n")
	b.WriteString("3. Accept only a verified candidate; otherwise reflect or escalate.\n")
	b.WriteString("4. Present the result for review.\n")
	return b.String()
}

// Ensure creates the interview row if absent and returns the current state.
func (r *Repo) Ensure(ctx context.Context, projectID string, questions []Question) (Interview, error) {
	if projectID == "" {
		return Interview{}, fmt.Errorf("interview: project id is required")
	}
	qj, err := json.Marshal(questions)
	if err != nil {
		return Interview{}, err
	}
	if _, err := r.db.DB().ExecContext(ctx,
		`INSERT OR IGNORE INTO project_interviews (project_id, questions_json)
		 VALUES (?, ?)`, projectID, string(qj)); err != nil {
		return Interview{}, fmt.Errorf("creating interview: %w", err)
	}
	in, ok, err := r.Get(ctx, projectID)
	if err != nil {
		return Interview{}, err
	}
	if !ok {
		return Interview{}, ErrNotFound
	}
	return in, nil
}

// Get returns the project's interview.
func (r *Repo) Get(ctx context.Context, projectID string) (Interview, bool, error) {
	var (
		status, qj, aj, plan string
	)
	err := r.db.DB().QueryRowContext(ctx,
		`SELECT status, questions_json, answers_json, plan_md
		   FROM project_interviews WHERE project_id = ?`, projectID).
		Scan(&status, &qj, &aj, &plan)
	if errors.Is(err, sql.ErrNoRows) {
		return Interview{}, false, nil
	}
	if err != nil {
		return Interview{}, false, fmt.Errorf("reading interview: %w", err)
	}
	in := Interview{ProjectID: projectID, Status: status, Plan: plan, Answers: map[string]string{}}
	if err := json.Unmarshal([]byte(qj), &in.Questions); err != nil {
		return Interview{}, false, fmt.Errorf("decoding interview questions: %w", err)
	}
	if err := json.Unmarshal([]byte(aj), &in.Answers); err != nil {
		return Interview{}, false, fmt.Errorf("decoding interview answers: %w", err)
	}
	return in, true, nil
}

// SaveAnswers stores the answers and the generated plan, moving the interview
// to plan_ready. An empty plan is rejected: approval must have something to
// review.
func (r *Repo) SaveAnswers(ctx context.Context, projectID string, answers map[string]string, plan string) (Interview, error) {
	if strings.TrimSpace(plan) == "" {
		return Interview{}, fmt.Errorf("interview: a plan is required before approval")
	}
	aj, err := json.Marshal(answers)
	if err != nil {
		return Interview{}, err
	}
	res, err := r.db.DB().ExecContext(ctx,
		`UPDATE project_interviews
		    SET answers_json = ?, plan_md = ?, status = ?
		  WHERE project_id = ?`,
		string(aj), plan, StatusPlanReady, projectID)
	if err != nil {
		return Interview{}, fmt.Errorf("saving interview: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Interview{}, ErrNotFound
	}
	in, _, err := r.Get(ctx, projectID)
	return in, err
}

// Decide records operator approval or a skip. The caller starts the first job.
func (r *Repo) Decide(ctx context.Context, projectID, status string) (Interview, error) {
	if status != StatusApproved && status != StatusSkipped {
		return Interview{}, fmt.Errorf("interview: invalid decision %q", status)
	}
	res, err := r.db.DB().ExecContext(ctx,
		`UPDATE project_interviews SET status = ? WHERE project_id = ?`, status, projectID)
	if err != nil {
		return Interview{}, fmt.Errorf("recording interview decision: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Interview{}, ErrNotFound
	}
	in, _, err := r.Get(ctx, projectID)
	return in, err
}
