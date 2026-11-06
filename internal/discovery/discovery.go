// Package discovery turns an exploratory goal into a portfolio of concrete
// opportunities. It is deliberately separate from the dialectical engine: the
// engine's divergence/evaluation phases select one winner, while discovery must
// preserve many distinct options for a human to choose from.
//
// The package owns the structured option model, the prompt contract, a
// deterministic validator/scorer, and the durable session store. The actual
// model call lives in an adapter (cmd/athanor) so internal/ stays free of
// provider details.
package discovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/ids"
	"github.com/tcs76321/athanor/internal/store"
)

// Evidence is one supporting claim. Source may be empty when the model has no
// external evidence; the UI marks confidence accordingly.
type Evidence struct {
	Claim  string `json:"claim"`
	Source string `json:"source,omitempty"`
}

// Option is one opportunity in a discovery portfolio.
type Option struct {
	ID                     string         `json:"id,omitempty"`
	Title                  string         `json:"title"`
	OneLiner               string         `json:"one_liner"`
	Industry               string         `json:"industry"`
	Niche                  string         `json:"niche"`
	Customer               string         `json:"customer"`
	Pain                   string         `json:"pain"`
	Wedge                  string         `json:"wedge"`
	BusinessModel          string         `json:"business_model"`
	Pricing                string         `json:"pricing"`
	FirstOffer             string         `json:"first_offer"`
	ValidationExperiment   string         `json:"validation_experiment"`
	TimeToFirstRevenueDays int            `json:"time_to_first_revenue_days"`
	StartupCostUSD         int            `json:"startup_cost_usd"`
	Skills                 []string       `json:"skills_required"`
	Competition            string         `json:"competition"`
	Evidence               []Evidence     `json:"evidence"`
	Risks                  []string       `json:"risks"`
	Assumptions            []string       `json:"assumptions"`
	Confidence             float64        `json:"confidence"`
	Scores                 map[string]int `json:"scores,omitempty"`
}

// Session is one durable discovery run for a project.
type Session struct {
	ID         string
	ProjectID  string
	Status     string
	Brief      string
	Error      string
	SelectedID string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Options    []Option
}

// Request is the generator's input.
type Request struct {
	Goal        string
	Brief       string
	Constraints []string
	Sources     []string
	Count       int
	// Progress is an optional observer for a long discovery call. It is not
	// used by the pure prompt/parser/scorer; the UI and CLI can attach one to
	// make the single model call visible while it runs.
	Progress func(ProgressEvent)
}

// ProgressEvent is one observable step in a discovery run.
type ProgressEvent struct {
	Stage   string
	Detail  string
	Model   string
	Bytes   int
	Options int
	// Tail is the most recent slice of the model response, when the generator
	// can stream it. It is display-only and already bounded by the caller.
	Tail string
}

// Report emits one progress event when an observer is attached.
func (r Request) Report(ev ProgressEvent) {
	if r.Progress != nil {
		r.Progress(ev)
	}
}

// Generator produces a JSON option array from a request.
type Generator interface {
	Generate(ctx context.Context, req Request) (string, error)
}

// BuildPrompt is the closed prompt contract. It demands a JSON array and a
// spread of industries/niches; it also states the honesty rules explicitly so
// a local model does not invent market data.
func BuildPrompt(req Request) string {
	count := req.Count
	if count < 6 {
		count = 10
	}
	var b strings.Builder
	b.WriteString("You are Athanor's discovery planner. Produce a portfolio of concrete, distinct ways to pursue the goal below.\n\n")
	fmt.Fprintf(&b, "GOAL: %s\n", strings.TrimSpace(req.Goal))
	if strings.TrimSpace(req.Brief) != "" {
		fmt.Fprintf(&b, "\nBRIEF (the operator's answers):\n%s\n", strings.TrimSpace(req.Brief))
	}
	if len(req.Constraints) > 0 {
		b.WriteString("\nCONSTRAINTS / LIMITS:\n")
		for _, c := range req.Constraints {
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(c))
		}
	}
	if len(req.Sources) > 0 {
		b.WriteString("\nALLOWED SOURCES (do not claim to have read anything else):\n")
		for _, s := range req.Sources {
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(s))
		}
	}
	fmt.Fprintf(&b, "\nReturn EXACTLY a JSON array of %d objects. Cover at least 4 distinct industries and at least 6 distinct niches. Do not repeat a business model unless the customer and wedge differ substantially.\n", count)
	b.WriteString("Each object must have these keys (strings unless noted): title, one_liner, industry, niche, customer, pain, wedge, business_model, pricing, first_offer, validation_experiment, time_to_first_revenue_days (integer), startup_cost_usd (integer), skills_required (array of strings), competition, evidence (array of {claim, source}), risks (array of strings), assumptions (array of strings), confidence (number 0..1).\n")
	b.WriteString("RULES:\n")
	b.WriteString("- Be specific: name the customer, the painful job, and the first paid offer.\n")
	b.WriteString("- Prefer ideas that can be validated cheaply in under 30 days.\n")
	b.WriteString("- Separate facts from assumptions. If you have no source, say so in the claim and leave source empty.\n")
	b.WriteString("- Do NOT invent market sizes, revenue figures, or citations.\n")
	b.WriteString("- Include at least one low-capital, one service-first, and one productized idea.\n")
	b.WriteString("- Output only the JSON array, with no markdown fence or commentary.\n")
	return b.String()
}

// Parse extracts and decodes the option array from a model response. It
// tolerates surrounding prose and a fenced block; malformed JSON is a typed
// error so the caller can show a retry rather than a silent empty portfolio.
func Parse(raw string) ([]Option, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("discovery: empty response")
	}
	// The contract asks for an array. Try that first, including a fenced or
	// prose-wrapped array.
	if start := strings.Index(s, "["); start >= 0 {
		if end := strings.LastIndex(s, "]"); end > start {
			var out []Option
			if err := json.Unmarshal([]byte(s[start:end+1]), &out); err == nil && len(out) > 0 {
				return out, nil
			}
		}
		// A truncated array has no closing `]`; keep every complete object.
		if body := s[start:]; !strings.Contains(body, "]") {
			if last := strings.LastIndex(body, "}"); last >= 0 {
				var out []Option
				if err := json.Unmarshal([]byte(body[:last+1]+"]"), &out); err == nil && len(out) > 0 {
					return out, nil
				}
			}
		}
		if out := parseSequence(s[start:]); len(out) > 0 {
			return out, nil
		}
	}
	// Some models wrap the array in an object: {"options": [...]}.
	if start := strings.Index(s, "{"); start >= 0 {
		var wrapped struct {
			Options []Option `json:"options"`
		}
		if err := json.Unmarshal([]byte(s[start:]), &wrapped); err == nil && len(wrapped.Options) > 0 {
			return wrapped.Options, nil
		}
		// Some models emit a sequence of option objects without the outer
		// array. Accept that shape rather than failing a useful portfolio.
		if out := parseSequence(s[start:]); len(out) > 0 {
			return out, nil
		}
	}
	return nil, fmt.Errorf("discovery: response contains no JSON options")
}

// parseSequence decodes one or more complete JSON objects from a fragment. It
// is used for a truncated array and for a model that emits concatenated
// option objects without the enclosing brackets; Validate still enforces the
// portfolio's minimum size and spread.
func parseSequence(s string) []Option {
	dec := json.NewDecoder(strings.NewReader(s))
	var out []Option
	for {
		var o Option
		if err := dec.Decode(&o); err != nil {
			break
		}
		if strings.TrimSpace(o.Title) != "" {
			out = append(out, o)
		}
	}
	return out
}

// Dedupe removes obvious duplicate titles, keeping the first occurrence.
func Dedupe(in []Option) []Option {
	seen := map[string]bool{}
	out := make([]Option, 0, len(in))
	for _, o := range in {
		key := strings.ToLower(strings.TrimSpace(o.Title))
		key = strings.Join(strings.Fields(key), " ")
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, o)
	}
	return out
}

// Validate enforces the portfolio's non-negotiables. It is intentionally
// about structure and spread, not taste: the human chooses.
func Validate(opts []Option) error {
	if len(opts) < 6 {
		return fmt.Errorf("discovery: need at least 6 distinct options, got %d", len(opts))
	}
	if len(opts) > 16 {
		opts = opts[:16]
	}
	industries := map[string]bool{}
	for i, o := range opts {
		missing := []string{}
		if strings.TrimSpace(o.Title) == "" {
			missing = append(missing, "title")
		}
		if strings.TrimSpace(o.OneLiner) == "" {
			missing = append(missing, "one_liner")
		}
		if strings.TrimSpace(o.Industry) == "" {
			missing = append(missing, "industry")
		}
		if strings.TrimSpace(o.Customer) == "" {
			missing = append(missing, "customer")
		}
		if strings.TrimSpace(o.Wedge) == "" {
			missing = append(missing, "wedge")
		}
		if len(missing) > 0 {
			return fmt.Errorf("discovery: option %d is missing %s", i+1, strings.Join(missing, ", "))
		}
		industries[strings.ToLower(strings.TrimSpace(o.Industry))] = true
	}
	if len(industries) < 4 {
		return fmt.Errorf("discovery: portfolio covers only %d industries; need at least 4", len(industries))
	}
	return nil
}

// Score fills each option's deterministic score breakdown. The model's
// confidence is advisory; these dimensions are reproducible from the option
// text so the UI can explain a ranking.
func Score(opts []Option) {
	for i := range opts {
		o := &opts[i]
		clarity := 0
		if strings.TrimSpace(o.OneLiner) != "" {
			clarity += 5
		}
		if strings.TrimSpace(o.Customer) != "" {
			clarity += 5
		}
		if strings.TrimSpace(o.Pain) != "" {
			clarity += 5
		}
		if strings.TrimSpace(o.Wedge) != "" {
			clarity += 5
		}
		evidence := len(o.Evidence) * 5
		if evidence > 20 {
			evidence = 20
		}
		speed := 20 - o.TimeToFirstRevenueDays/10
		if speed < 0 {
			speed = 0
		}
		if speed > 20 {
			speed = 20
		}
		capital := 0
		switch {
		case o.StartupCostUSD <= 100:
			capital = 20
		case o.StartupCostUSD <= 500:
			capital = 16
		case o.StartupCostUSD <= 2000:
			capital = 10
		case o.StartupCostUSD <= 10000:
			capital = 4
		}
		risk := 20 - len(o.Risks)*3
		if risk < 0 {
			risk = 0
		}
		if risk > 20 {
			risk = 20
		}
		o.Scores = map[string]int{
			"clarity":  clarity,
			"evidence": evidence,
			"speed":    speed,
			"capital":  capital,
			"risk":     risk,
			"total":    clarity + evidence + speed + capital + risk,
		}
		if o.Confidence < 0 {
			o.Confidence = 0
		}
		if o.Confidence > 1 {
			o.Confidence = 1
		}
		if len(o.Skills) == 0 {
			o.Skills = []string{}
		}
		if o.Risks == nil {
			o.Risks = []string{}
		}
		if o.Assumptions == nil {
			o.Assumptions = []string{}
		}
	}
	sort.SliceStable(opts, func(i, j int) bool {
		return opts[i].Scores["total"] > opts[j].Scores["total"]
	})
}

// Store persists discovery sessions and options.
type Store struct{ db *store.Store }

func NewStore(s *store.Store) *Store { return &Store{db: s} }

// Begin creates a durable, in-progress discovery session before the model
// call. A Core restart therefore shows "in progress" (or a stale failure)
// instead of silently returning to the brief.
func (s *Store) Begin(ctx context.Context, projectID, brief string) (Session, error) {
	sessionID := ids.New()
	if _, err := s.db.DB().ExecContext(ctx,
		`INSERT INTO discovery_sessions (id, project_id, status, brief) VALUES (?, ?, 'generating', ?)`,
		sessionID, projectID, brief); err != nil {
		return Session{}, fmt.Errorf("creating discovery session: %w", err)
	}
	sess, ok, err := s.Get(ctx, projectID)
	if err != nil {
		return Session{}, err
	}
	if !ok {
		return Session{}, fmt.Errorf("discovery: session disappeared after begin")
	}
	return sess, nil
}

// Complete replaces a generating session with its scored options and marks it
// ready. It is transactional: a partial option set is never visible.
func (s *Store) Complete(ctx context.Context, sessionID string, opts []Option) error {
	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for i, o := range opts {
		id := ids.New()
		skills, _ := json.Marshal(o.Skills)
		evidence, _ := json.Marshal(o.Evidence)
		risks, _ := json.Marshal(o.Risks)
		assumptions, _ := json.Marshal(o.Assumptions)
		scores, _ := json.Marshal(o.Scores)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO discovery_options (
				id, session_id, sort_order, title, one_liner, industry, niche,
				customer, pain, wedge, business_model, pricing, first_offer,
				validation_experiment, time_to_first_revenue_days, startup_cost_usd,
				skills_json, competition, evidence_json, risks_json, assumptions_json,
				confidence, scores_json)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, sessionID, i, o.Title, o.OneLiner, o.Industry, o.Niche,
			o.Customer, o.Pain, o.Wedge, o.BusinessModel, o.Pricing, o.FirstOffer,
			o.ValidationExperiment, o.TimeToFirstRevenueDays, o.StartupCostUSD,
			string(skills), o.Competition, string(evidence), string(risks), string(assumptions),
			o.Confidence, string(scores)); err != nil {
			return fmt.Errorf("saving discovery option: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE discovery_sessions SET status = 'ready', error = '' WHERE id = ?`, sessionID); err != nil {
		return fmt.Errorf("marking discovery ready: %w", err)
	}
	return tx.Commit()
}

// Fail marks a generating session failed with a bounded, user-facing reason.
func (s *Store) Fail(ctx context.Context, sessionID, msg string) {
	_, _ = s.db.DB().ExecContext(ctx,
		`UPDATE discovery_sessions SET status = 'failed', error = ? WHERE id = ?`, msg, sessionID)
}

// Save is the convenience path used by tests and callers that already hold a
// complete portfolio: begin, complete, return the newest session.
func (s *Store) Save(ctx context.Context, projectID, brief string, opts []Option) (Session, error) {
	sess, err := s.Begin(ctx, projectID, brief)
	if err != nil {
		return Session{}, err
	}
	if err := s.Complete(ctx, sess.ID, opts); err != nil {
		return Session{}, err
	}
	got, ok, err := s.Get(ctx, projectID)
	if err != nil {
		return Session{}, err
	}
	if !ok {
		return Session{}, fmt.Errorf("discovery: session disappeared after save")
	}
	return got, nil
}

// Get returns the project's newest session with its options.
func (s *Store) Get(ctx context.Context, projectID string) (Session, bool, error) {
	var sess Session
	var createdAt, updatedAt string
	err := s.db.DB().QueryRowContext(ctx, `
		SELECT id, project_id, status, brief, error, selected_option_id, created_at, updated_at
		  FROM discovery_sessions WHERE project_id = ? ORDER BY created_at DESC LIMIT 1`,
		projectID).Scan(&sess.ID, &sess.ProjectID, &sess.Status, &sess.Brief,
		&sess.Error, &sess.SelectedID, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	sess.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	sess.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	rows, err := s.db.DB().QueryContext(ctx, `
		SELECT id, title, one_liner, industry, niche, customer, pain, wedge,
		       business_model, pricing, first_offer, validation_experiment,
		       time_to_first_revenue_days, startup_cost_usd, skills_json,
		       competition, evidence_json, risks_json, assumptions_json,
		       confidence, scores_json
		  FROM discovery_options WHERE session_id = ? ORDER BY sort_order`, sess.ID)
	if err != nil {
		return Session{}, false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var o Option
		var skills, evidence, risks, assumptions, scores string
		if err := rows.Scan(&o.ID, &o.Title, &o.OneLiner, &o.Industry, &o.Niche,
			&o.Customer, &o.Pain, &o.Wedge, &o.BusinessModel, &o.Pricing,
			&o.FirstOffer, &o.ValidationExperiment, &o.TimeToFirstRevenueDays,
			&o.StartupCostUSD, &skills, &o.Competition, &evidence, &risks,
			&assumptions, &o.Confidence, &scores); err != nil {
			return Session{}, false, err
		}
		_ = json.Unmarshal([]byte(skills), &o.Skills)
		_ = json.Unmarshal([]byte(evidence), &o.Evidence)
		_ = json.Unmarshal([]byte(risks), &o.Risks)
		_ = json.Unmarshal([]byte(assumptions), &o.Assumptions)
		_ = json.Unmarshal([]byte(scores), &o.Scores)
		sess.Options = append(sess.Options, o)
	}
	return sess, true, rows.Err()
}

// Select records the human's chosen option for the newest session.
func (s *Store) Select(ctx context.Context, projectID, optionID string) error {
	var sessionID string
	if err := s.db.DB().QueryRowContext(ctx,
		`SELECT id FROM discovery_sessions WHERE project_id = ? ORDER BY created_at DESC LIMIT 1`,
		projectID).Scan(&sessionID); err != nil {
		return fmt.Errorf("discovery: no session for project: %w", err)
	}
	var exists int
	if err := s.db.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM discovery_options WHERE id = ? AND session_id = ?`,
		optionID, sessionID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return fmt.Errorf("discovery: option %s is not in this session", optionID)
	}
	_, err := s.db.DB().ExecContext(ctx,
		`UPDATE discovery_sessions SET selected_option_id = ? WHERE id = ?`, optionID, sessionID)
	return err
}

// Service ties a generator to the store.
type Service struct {
	gen   Generator
	store *Store
}

func NewService(gen Generator, st *Store) *Service { return &Service{gen: gen, store: st} }

// Generate runs one discovery pass. A generator error or an invalid portfolio
// is returned to the caller; nothing partial is persisted.
func (s *Service) Generate(ctx context.Context, projectID string, req Request) (Session, error) {
	if s.gen == nil {
		return Session{}, fmt.Errorf("discovery: no generator configured")
	}
	req.Report(ProgressEvent{Stage: "session", Detail: "creating durable discovery session"})
	sess, err := s.store.Begin(ctx, projectID, req.Brief)
	if err != nil {
		return Session{}, err
	}
	fail := func(err error) (Session, error) {
		req.Report(ProgressEvent{Stage: "failed", Detail: err.Error()})
		fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.store.Fail(fctx, sess.ID, err.Error())
		return Session{}, err
	}
	req.Report(ProgressEvent{Stage: "model", Detail: "asking the model for a portfolio"})
	raw, err := s.gen.Generate(ctx, req)
	if err != nil {
		return fail(err)
	}
	req.Report(ProgressEvent{Stage: "response", Detail: "model response received", Bytes: len(raw)})
	opts, err := Parse(raw)
	if err != nil {
		return fail(err)
	}
	req.Report(ProgressEvent{Stage: "parse", Detail: "JSON parsed", Options: len(opts)})
	opts = Dedupe(opts)
	req.Report(ProgressEvent{Stage: "dedupe", Detail: "duplicate directions removed", Options: len(opts)})
	if err := Validate(opts); err != nil {
		return fail(err)
	}
	req.Report(ProgressEvent{Stage: "validate", Detail: "portfolio meets the spread rules", Options: len(opts)})
	Score(opts)
	req.Report(ProgressEvent{Stage: "score", Detail: "deterministic scores computed", Options: len(opts)})
	if err := s.store.Complete(ctx, sess.ID, opts); err != nil {
		return fail(err)
	}
	req.Report(ProgressEvent{Stage: "save", Detail: "portfolio persisted", Options: len(opts)})
	got, ok, err := s.store.Get(ctx, projectID)
	if err != nil {
		return Session{}, err
	}
	if !ok {
		return Session{}, fmt.Errorf("discovery: session disappeared after completion")
	}
	return got, nil
}

// Get and Select delegate to the store.
func (s *Service) Get(ctx context.Context, projectID string) (Session, bool, error) {
	return s.store.Get(ctx, projectID)
}

func (s *Service) Select(ctx context.Context, projectID, optionID string) error {
	return s.store.Select(ctx, projectID, optionID)
}
