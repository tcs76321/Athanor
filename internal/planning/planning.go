// Package planning turns a chosen discovery direction into a small set of
// AI-authored, reviewable 30-day plans. It is deliberately separate from the
// deterministic plan builder: the model proposes, the validator and scorer
// constrain, and the human selects before the DAG is created.
package planning

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/ids"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
)

// Task is one plan task before it becomes a project.TaskSpec.
type Task struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Capability  string   `json:"capability"`
	Criteria    []string `json:"criteria"`
}

// Candidate is one complete plan proposal.
type Candidate struct {
	Title       string   `json:"title"`
	Summary     string   `json:"summary"`
	Tasks       []Task   `json:"tasks"`
	Risks       []string `json:"risks"`
	Assumptions []string `json:"assumptions"`
	Confidence  float64  `json:"confidence"`
	Score       int      `json:"-"`
}

// Session is one durable planning run for a project.
type Session struct {
	ID            string
	ProjectID     string
	OptionID      string
	Status        string
	Error         string
	SelectedIndex int
	Candidates    []Candidate
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Request is the generator input.
type Request struct {
	Goal        string
	Brief       string
	Constraints []string
	Sources     []string
	OptionTitle string
	OneLiner    string
	Customer    string
	Pain        string
	Wedge       string
	Offer       string
	Validation  string
}

// Generator produces the raw JSON plan array.
type Generator interface {
	Generate(ctx context.Context, req Request) (string, error)
}

// BuildPrompt is the closed plan-generation contract.
func BuildPrompt(req Request) string {
	var b strings.Builder
	b.WriteString("You are Athanor's planning agent. Produce exactly 3 distinct 30-day plans for the chosen direction below.\n\n")
	fmt.Fprintf(&b, "GOAL: %s\n\n", strings.TrimSpace(req.Goal))
	if req.Brief != "" {
		fmt.Fprintf(&b, "BRIEF:\n%s\n\n", strings.TrimSpace(req.Brief))
	}
	fmt.Fprintf(&b, "CHOSEN DIRECTION: %s — %s\n", req.OptionTitle, req.OneLiner)
	if req.Customer != "" {
		fmt.Fprintf(&b, "AUDIENCE: %s\n", req.Customer)
	}
	if req.Pain != "" {
		fmt.Fprintf(&b, "NEED: %s\n", req.Pain)
	}
	if req.Wedge != "" {
		fmt.Fprintf(&b, "DISTINCTIVE APPROACH: %s\n", req.Wedge)
	}
	if req.Offer != "" {
		fmt.Fprintf(&b, "FIRST CONCRETE DELIVERABLE: %s\n", req.Offer)
	}
	if req.Validation != "" {
		fmt.Fprintf(&b, "FIRST VALIDATION: %s\n", req.Validation)
	}
	if len(req.Constraints) > 0 {
		b.WriteString("\nCONSTRAINTS:\n")
		for _, c := range req.Constraints {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	if len(req.Sources) > 0 {
		b.WriteString("\nALLOWED SOURCES:\n")
		for _, s := range req.Sources {
			fmt.Fprintf(&b, "- %s\n", s)
		}
	}
	b.WriteString("\nReturn ONLY a JSON array of exactly 3 objects. Each object: {title, summary, tasks:[{title, description, capability, criteria:[...]}], risks:[...], assumptions:[...], confidence:0..1}.\n")
	b.WriteString("RULES:\n")
	b.WriteString("- The plans must be genuinely different: one fastest validation, one deepest quality, one lowest cost/least risky.\n")
	b.WriteString("- Tasks must be concrete and ordered; include 4–8 tasks per plan.\n")
	b.WriteString("- capability must be one of: code, data, document, text, media.\n")
	b.WriteString("- Every task must be executable in 30 days and have at least one acceptance criterion.\n")
	b.WriteString("- Cite only the allowed sources; do not invent market data.\n")
	b.WriteString("- Output only the JSON array, no markdown or commentary.\n")
	return b.String()
}

// Parse extracts a candidate array from a model response.
func Parse(raw string) ([]Candidate, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start < 0 || end <= start {
		if ostart := strings.Index(s, "{"); ostart >= 0 {
			var wrapped struct {
				Plans []Candidate `json:"plans"`
			}
			if err := json.Unmarshal([]byte(s[ostart:]), &wrapped); err == nil && len(wrapped.Plans) > 0 {
				return wrapped.Plans, nil
			}
		}
		return nil, fmt.Errorf("planning: response contains no JSON array")
	}
	var out []Candidate
	if err := json.Unmarshal([]byte(s[start:end+1]), &out); err != nil {
		return nil, fmt.Errorf("planning: decoding candidates: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("planning: empty candidate array")
	}
	return out, nil
}

// Validate enforces the plan's structural contract and capability set.
func Validate(candidates []Candidate) ([]Candidate, error) {
	if len(candidates) < 3 {
		return nil, fmt.Errorf("planning: need 3 candidates, got %d", len(candidates))
	}
	out := make([]Candidate, 0, 3)
	seenTitles := map[string]bool{}
	for _, c := range candidates {
		title := strings.TrimSpace(c.Title)
		if title == "" {
			return nil, fmt.Errorf("planning: a candidate has no title")
		}
		key := strings.ToLower(title)
		if seenTitles[key] {
			continue
		}
		seenTitles[key] = true
		if strings.TrimSpace(c.Summary) == "" {
			return nil, fmt.Errorf("planning: candidate %q has no summary", title)
		}
		if c.Risks == nil {
			c.Risks = []string{}
		}
		if c.Assumptions == nil {
			c.Assumptions = []string{}
		}
		if len(c.Tasks) < 2 || len(c.Tasks) > 10 {
			return nil, fmt.Errorf("planning: candidate %q has %d tasks (want 2–10)", title, len(c.Tasks))
		}
		taskTitles := map[string]bool{}
		for _, t := range c.Tasks {
			if strings.TrimSpace(t.Title) == "" || strings.TrimSpace(t.Description) == "" {
				return nil, fmt.Errorf("planning: candidate %q has an empty task", title)
			}
			if !project.ValidCapability(t.Capability) {
				return nil, fmt.Errorf("planning: candidate %q task %q has invalid capability %q", title, t.Title, t.Capability)
			}
			key := strings.ToLower(strings.TrimSpace(t.Title))
			if taskTitles[key] {
				return nil, fmt.Errorf("planning: candidate %q repeats task %q", title, t.Title)
			}
			taskTitles[key] = true
			if len(t.Criteria) == 0 {
				return nil, fmt.Errorf("planning: candidate %q task %q has no acceptance criterion", title, t.Title)
			}
		}
		out = append(out, c)
		if len(out) == 3 {
			break
		}
	}
	if len(out) < 3 {
		return nil, fmt.Errorf("planning: need 3 distinct titled candidates, got %d", len(out))
	}
	return out, nil
}

// Score fills a deterministic score for each candidate.
func Score(candidates []Candidate) {
	for i := range candidates {
		c := &candidates[i]
		// Clamp before scoring: an unclamped confidence would push the
		// bounded 0–100 score above 100 even though the UI labels it /100.
		if c.Confidence < 0 {
			c.Confidence = 0
		}
		if c.Confidence > 1 {
			c.Confidence = 1
		}
		tasks := len(c.Tasks)
		criteria := 0
		for _, t := range c.Tasks {
			criteria += len(t.Criteria)
		}
		specificity := 0
		for _, t := range c.Tasks {
			if len(t.Description) >= 40 {
				specificity++
			}
		}
		score := 0
		score += minInt(20, tasks*3)
		score += minInt(20, criteria*4)
		score += minInt(15, len(c.Risks)*5)
		score += minInt(15, len(c.Assumptions)*5)
		score += minInt(20, specificity*4)
		score += int(c.Confidence * 10)
		c.Score = score
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Score > candidates[j].Score })
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Store persists planning sessions.
type Store struct{ db *store.Store }

func NewStore(s *store.Store) *Store { return &Store{db: s} }

// Begin starts a durable planning session for one project and direction. The
// brief itself lives in project_briefs; the session records the option so a
// stale plan cannot be shown after the operator chooses another direction.
func (s *Store) Begin(ctx context.Context, projectID, optionID string) (Session, error) {
	id := ids.New()
	if _, err := s.db.DB().ExecContext(ctx,
		`INSERT INTO plan_sessions (id, project_id, option_id, status) VALUES (?, ?, ?, 'generating')`,
		id, projectID, optionID); err != nil {
		return Session{}, fmt.Errorf("creating plan session: %w", err)
	}
	sess, ok, err := s.getByID(ctx, id)
	if err != nil {
		return Session{}, err
	}
	if !ok {
		return Session{}, fmt.Errorf("planning: session disappeared after begin")
	}
	return sess, nil
}

func (s *Store) Complete(ctx context.Context, sessionID string, candidates []Candidate) error {
	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for i, c := range candidates {
		tasks, _ := json.Marshal(c.Tasks)
		risks, _ := json.Marshal(c.Risks)
		assumptions, _ := json.Marshal(c.Assumptions)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO plan_candidates (id, session_id, sort_order, title, summary, tasks_json, risks_json, assumptions_json, confidence, score)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ids.New(), sessionID, i, c.Title, c.Summary, string(tasks), string(risks), string(assumptions), c.Confidence, c.Score); err != nil {
			return fmt.Errorf("saving plan candidate: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE plan_sessions SET status = 'ready', error = '' WHERE id = ?`, sessionID); err != nil {
		return fmt.Errorf("marking plan ready: %w", err)
	}
	return tx.Commit()
}

func (s *Store) Fail(ctx context.Context, sessionID, msg string) {
	_, _ = s.db.DB().ExecContext(ctx,
		`UPDATE plan_sessions SET status = 'failed', error = ? WHERE id = ?`, msg, sessionID)
}

func (s *Store) Get(ctx context.Context, projectID string) (Session, bool, error) {
	var id string
	err := s.db.DB().QueryRowContext(ctx, `
		SELECT id FROM plan_sessions WHERE project_id = ?
		ORDER BY created_at DESC, rowid DESC LIMIT 1`, projectID).Scan(&id)
	if err == sql.ErrNoRows {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	return s.getByID(ctx, id)
}

// getByID loads one session and its candidates by primary key, so Begin and
// Generate cannot accidentally return a different concurrent session.
func (s *Store) getByID(ctx context.Context, sessionID string) (Session, bool, error) {
	var sess Session
	var created, updated string
	err := s.db.DB().QueryRowContext(ctx, `
		SELECT id, project_id, option_id, status, error, selected_index, created_at, updated_at
		FROM plan_sessions WHERE id = ?`, sessionID).
		Scan(&sess.ID, &sess.ProjectID, &sess.OptionID, &sess.Status, &sess.Error,
			&sess.SelectedIndex, &created, &updated)
	if err == sql.ErrNoRows {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	sess.CreatedAt, _ = time.Parse(time.RFC3339, created)
	sess.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	rows, err := s.db.DB().QueryContext(ctx, `
		SELECT title, summary, tasks_json, risks_json, assumptions_json, confidence, score
		FROM plan_candidates WHERE session_id = ? ORDER BY sort_order`, sess.ID)
	if err != nil {
		return Session{}, false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var c Candidate
		var tasks, risks, assumptions string
		if err := rows.Scan(&c.Title, &c.Summary, &tasks, &risks, &assumptions, &c.Confidence, &c.Score); err != nil {
			return Session{}, false, err
		}
		if err := json.Unmarshal([]byte(tasks), &c.Tasks); err != nil {
			return Session{}, false, fmt.Errorf("planning: decoding tasks for %q: %w", c.Title, err)
		}
		if err := json.Unmarshal([]byte(risks), &c.Risks); err != nil {
			return Session{}, false, fmt.Errorf("planning: decoding risks for %q: %w", c.Title, err)
		}
		if err := json.Unmarshal([]byte(assumptions), &c.Assumptions); err != nil {
			return Session{}, false, fmt.Errorf("planning: decoding assumptions for %q: %w", c.Title, err)
		}
		sess.Candidates = append(sess.Candidates, c)
	}
	return sess, true, rows.Err()
}

func (s *Store) Select(ctx context.Context, projectID string, index int) error {
	sess, ok, err := s.Get(ctx, projectID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("planning: no session for project")
	}
	if sess.Status != "ready" {
		return fmt.Errorf("planning: session is %s, not ready", sess.Status)
	}
	if index < 0 || index >= len(sess.Candidates) {
		return fmt.Errorf("planning: candidate index %d out of range", index)
	}
	_, err = s.db.DB().ExecContext(ctx,
		`UPDATE plan_sessions SET selected_index = ? WHERE id = ?`, index, sess.ID)
	return err
}

// Service ties a generator to the store.
type Service struct {
	gen   Generator
	store *Store
}

func NewService(gen Generator, st *Store) *Service { return &Service{gen: gen, store: st} }

func (s *Service) Generate(ctx context.Context, projectID, optionID string, req Request) (Session, error) {
	if s.gen == nil {
		return Session{}, fmt.Errorf("planning: no generator configured")
	}
	sess, err := s.store.Begin(ctx, projectID, optionID)
	if err != nil {
		return Session{}, err
	}
	fail := func(err error) (Session, error) {
		fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.store.Fail(fctx, sess.ID, err.Error())
		return Session{}, err
	}
	raw, err := s.gen.Generate(ctx, req)
	if err != nil {
		return fail(err)
	}
	candidates, err := Parse(raw)
	if err != nil {
		return fail(err)
	}
	candidates, err = Validate(candidates)
	if err != nil {
		return fail(err)
	}
	Score(candidates)
	if err := s.store.Complete(ctx, sess.ID, candidates); err != nil {
		return fail(err)
	}
	got, ok, err := s.store.getByID(ctx, sess.ID)
	if err != nil {
		return Session{}, err
	}
	if !ok {
		return Session{}, fmt.Errorf("planning: session disappeared after completion")
	}
	return got, nil
}

func (s *Service) Get(ctx context.Context, projectID string) (Session, bool, error) {
	return s.store.Get(ctx, projectID)
}

func (s *Service) Select(ctx context.Context, projectID string, index int) error {
	return s.store.Select(ctx, projectID, index)
}

// ToTaskSpecs converts a chosen candidate into dependency-ordered project
// tasks. Each task depends on the previous one, which is the smallest safe
// graph for an ordered 30-day plan; a future planner may emit explicit edges.
func ToTaskSpecs(c Candidate) []project.TaskSpec {
	specs := make([]project.TaskSpec, 0, len(c.Tasks))
	for i, t := range c.Tasks {
		key := fmt.Sprintf("task-%d", i+1)
		var deps []string
		if i > 0 {
			deps = []string{fmt.Sprintf("task-%d", i)}
		}
		specs = append(specs, project.TaskSpec{
			Key: key, Title: t.Title, Description: t.Description,
			Capability: t.Capability, Criteria: t.Criteria, DependsOn: deps,
		})
	}
	return specs
}
