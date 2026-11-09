// Package planning turns a chosen discovery direction into a small set of
// AI-authored, reviewable plans. It is deliberately separate from the
// deterministic plan builder: the model proposes, the validator and scorer
// constrain, and the human selects before the DAG is created.
package planning

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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

// Candidate is one complete plan proposal. The three candidates differ by
// stance (smallest verifiable slice, deepest coverage, lowest-risk sequence),
// not by a synthetic score or the model's self-reported confidence.
type Candidate struct {
	Title       string   `json:"title"`
	Summary     string   `json:"summary"`
	Tasks       []Task   `json:"tasks"`
	Risks       []string `json:"risks"`
	Assumptions []string `json:"assumptions"`
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
	Audience    string
	Need        string
	Approach    string
	Deliverable string
	FirstCheck  string
	// Repair carries a bounded rejection reason on a retry after a parse or
	// validation failure.
	Repair string
}

// Generator produces the raw JSON plan array.
type Generator interface {
	Generate(ctx context.Context, req Request) (string, error)
}

// BuildPrompt is the closed plan-generation contract.
func BuildPrompt(req Request) string {
	var b strings.Builder
	b.WriteString("You are Athanor's planning agent. Produce exactly 3 distinct plans for the chosen direction below. A plan is a short, ordered first iteration, not a fixed-length schedule.\n\n")
	fmt.Fprintf(&b, "GOAL: %s\n\n", strings.TrimSpace(req.Goal))
	if req.Brief != "" {
		fmt.Fprintf(&b, "BRIEF:\n%s\n\n", strings.TrimSpace(req.Brief))
	}
	fmt.Fprintf(&b, "CHOSEN DIRECTION: %s — %s\n", req.OptionTitle, req.OneLiner)
	if req.Audience != "" {
		fmt.Fprintf(&b, "AUDIENCE: %s\n", req.Audience)
	}
	if req.Need != "" {
		fmt.Fprintf(&b, "NEED: %s\n", req.Need)
	}
	if req.Approach != "" {
		fmt.Fprintf(&b, "DISTINCTIVE APPROACH: %s\n", req.Approach)
	}
	if req.Deliverable != "" {
		fmt.Fprintf(&b, "FIRST CONCRETE DELIVERABLE: %s\n", req.Deliverable)
	}
	if req.FirstCheck != "" {
		fmt.Fprintf(&b, "FIRST CHECK: %s\n", req.FirstCheck)
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
	b.WriteString("\nReturn ONLY a JSON array of exactly 3 objects. Each object: {title, summary, tasks:[{title, description, capability, criteria:[...]}], risks:[...], assumptions:[...]}.\n")
	b.WriteString("RULES:\n")
	b.WriteString("- The plans must be genuinely different: one smallest verifiable slice, one deepest coverage, one lowest-risk sequence. Name the stance in the title or summary.\n")
	b.WriteString("- Tasks must be concrete and ordered; include 4–8 tasks per plan.\n")
	b.WriteString("- capability must be one of: code, data, document, text, media.\n")
	b.WriteString("- Every task must be small enough to finish in one job and have at least one acceptance criterion.\n")
	b.WriteString("- Do not estimate time, cost, human effort, or required skills; Athanor runs locally under its own budget.\n")
	b.WriteString("- Cite only the allowed sources; do not invent data or citations.\n")
	b.WriteString("- Output only the JSON array, no markdown or commentary.\n")
	if repair := strings.TrimSpace(req.Repair); repair != "" {
		if len(repair) > 500 {
			repair = repair[:500] + "…"
		}
		b.WriteString("\nREPAIR REQUIRED: the previous response was rejected: " + repair + "\n")
		b.WriteString("Return ONE JSON array of exactly three complete plan objects. Every object must include title, summary, tasks with title/description/capability/criteria, risks, and assumptions. Do not return two arrays, concatenated objects, or a partial object.\n")
	}
	return b.String()
}

// Parse extracts plan candidates from a model response. Models sometimes emit
// more than one top-level value — two arrays, concatenated objects, or a
// wrapped object followed by another array — which a single json.Unmarshal
// rejects with "invalid character ',' after top-level value". The parser
// scans complete top-level JSON values, parses each one, and merges the
// candidates. A truncated final array is salvaged for its complete objects.
func Parse(raw string) ([]Candidate, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	var out []Candidate
	for _, value := range topLevelValues(s) {
		out = append(out, parsePlanValue(value)...)
	}
	if len(out) == 0 {
		// Compatibility fallback for a response whose outer syntax is too
		// damaged for the scanner: keep the old bounded extraction, but only
		// as a last resort.
		if start := strings.Index(s, "["); start >= 0 {
			if end := strings.LastIndex(s, "]"); end > start {
				var arr []Candidate
				if err := json.Unmarshal([]byte(s[start:end+1]), &arr); err == nil && len(arr) > 0 {
					out = append(out, arr...)
				}
			}
			if len(out) == 0 {
				out = append(out, parseSequence(s[start:])...)
			}
		}
		if len(out) == 0 {
			if start := strings.Index(s, "{"); start >= 0 {
				out = append(out, parsePlanValue(s[start:])...)
			}
		}
	}
	out = Dedupe(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("planning: response contains no complete plan objects")
	}
	return out, nil
}

// topLevelValues scans complete top-level JSON arrays/objects. It skips prose,
// commas, and separators between values; a truncated final value is returned
// as the remainder so the caller can salvage its complete objects.
func topLevelValues(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		for i < len(s) {
			switch s[i] {
			case ' ', '\t', '\n', '\r', ',', ';':
				i++
				continue
			}
			break
		}
		if i >= len(s) {
			break
		}
		if s[i] != '[' && s[i] != '{' {
			// Prose: advance to the next plausible JSON value.
			for i < len(s) && s[i] != '[' && s[i] != '{' {
				i++
			}
			continue
		}
		start := i
		depth := 0
		inString := false
		escaped := false
		closed := false
		for ; i < len(s); i++ {
			c := s[i]
			if inString {
				if escaped {
					escaped = false
					continue
				}
				if c == '\\' {
					escaped = true
					continue
				}
				if c == '"' {
					inString = false
				}
				continue
			}
			switch c {
			case '"':
				inString = true
			case '[', '{':
				depth++
			case ']', '}':
				depth--
				if depth == 0 {
					i++
					closed = true
				}
			}
			if closed {
				break
			}
		}
		if closed {
			out = append(out, s[start:i])
			continue
		}
		// A truncated tail: keep it for salvage, then stop.
		out = append(out, s[start:])
		break
	}
	return out
}

// parsePlanValue parses one top-level value as an array of candidates, a
// wrapped {plans|[...]} object, or a single candidate object.
func parsePlanValue(raw string) []Candidate {
	t := strings.TrimSpace(raw)
	if t == "" {
		return nil
	}
	switch t[0] {
	case '[':
		var arr []Candidate
		if err := json.Unmarshal([]byte(t), &arr); err == nil {
			return arr
		}
		// A truncated or trailing-comma array: scan its complete object
		// values directly rather than relying on one Decode pass, which stops
		// at the comma before the next object.
		inner := strings.TrimSpace(strings.TrimPrefix(t, "["))
		inner = strings.TrimSuffix(inner, "]")
		var out []Candidate
		for _, value := range topLevelValues(inner) {
			out = append(out, parsePlanValue(value)...)
		}
		if len(out) > 0 {
			return out
		}
		return parseSequence(inner)
	case '{':
		var wrapped struct {
			Plans      []Candidate `json:"plans"`
			Candidates []Candidate `json:"candidates"`
		}
		if err := json.Unmarshal([]byte(t), &wrapped); err == nil {
			if len(wrapped.Plans) > 0 {
				return wrapped.Plans
			}
			if len(wrapped.Candidates) > 0 {
				return wrapped.Candidates
			}
		}
		var c Candidate
		if err := json.Unmarshal([]byte(t), &c); err == nil && strings.TrimSpace(c.Title) != "" {
			return []Candidate{c}
		}
	}
	return nil
}

// Dedupe removes duplicate plan titles, keeping the first occurrence.
func Dedupe(candidates []Candidate) []Candidate {
	seen := map[string]bool{}
	out := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		key := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(c.Title)), " "))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}

// parseSequence decodes concatenated candidate objects from a fragment. It is
// used for a truncated array and for a model that emits objects without the
// enclosing brackets.
func parseSequence(s string) []Candidate {
	dec := json.NewDecoder(strings.NewReader(s))
	var out []Candidate
	for {
		var c Candidate
		if err := dec.Decode(&c); err != nil {
			break
		}
		if strings.TrimSpace(c.Title) != "" {
			out = append(out, c)
		}
	}
	return out
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
			INSERT INTO plan_candidates (id, session_id, sort_order, title, summary, tasks_json, risks_json, assumptions_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			ids.New(), sessionID, i, c.Title, c.Summary, string(tasks), string(risks), string(assumptions)); err != nil {
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
		SELECT title, summary, tasks_json, risks_json, assumptions_json
		FROM plan_candidates WHERE session_id = ? ORDER BY sort_order`, sess.ID)
	if err != nil {
		return Session{}, false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var c Candidate
		var tasks, risks, assumptions string
		if err := rows.Scan(&c.Title, &c.Summary, &tasks, &risks, &assumptions); err != nil {
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
	accept := func(raw string) ([]Candidate, error) {
		candidates, err := Parse(raw)
		if err != nil {
			return nil, err
		}
		return Validate(candidates)
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			req.Repair = lastErr.Error()
		}
		raw, genErr := s.gen.Generate(ctx, req)
		if genErr != nil {
			return fail(genErr)
		}
		candidates, acceptErr := accept(raw)
		if acceptErr == nil {
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
		lastErr = acceptErr
	}
	return fail(lastErr)
}

func (s *Service) Get(ctx context.Context, projectID string) (Session, bool, error) {
	return s.store.Get(ctx, projectID)
}

func (s *Service) Select(ctx context.Context, projectID string, index int) error {
	return s.store.Select(ctx, projectID, index)
}

// ToTaskSpecs converts a chosen candidate into dependency-ordered project
// tasks. Each task depends on the previous one, which is the smallest safe
// graph for an ordered plan; a future planner may emit explicit edges.
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
