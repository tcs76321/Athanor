// Package strategy implements §13.3 strategy capture (ROADMAP M6-T10):
// a StrategyProfile recorded at job start from the persona plan (zero
// inference) and an immutable StrategyOutcome at job end. The
// strategy_insights table (T11) is created here but mined in M6-T11.
package strategy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tcs76321/athanor/internal/cognitive"
	"github.com/tcs76321/athanor/internal/ids"
	"github.com/tcs76321/athanor/internal/store"
)

var ErrNotFound = errors.New("strategy: record not found")

// Results (§13.3).
const (
	ResultAcceptedNew      = "accepted_new"
	ResultAcceptedPrevious = "accepted_previous"
	ResultRejected         = "rejected"
	ResultFailed           = "failed"
	ResultCancelled        = "cancelled"
)

// ValidResult reports whether r is a §13.3 outcome result.
func ValidResult(r string) bool {
	switch r {
	case ResultAcceptedNew, ResultAcceptedPrevious, ResultRejected, ResultFailed, ResultCancelled:
		return true
	default:
		return false
	}
}

// SignatureEntry is one executed phase's strategy (§13.3).
type SignatureEntry struct {
	Phase          string  `json:"phase"`
	Persona        string  `json:"persona"`
	Temperature    float64 `json:"temperature"`
	PromptTemplate string  `json:"prompt_template,omitempty"`
	Candidates     int     `json:"candidates,omitempty"`
}

// TaskFeatures classifies the task (§13.3). Zero inference: fields the
// engine cannot derive are left empty.
type TaskFeatures struct {
	TaskType       string `json:"task_type,omitempty"`
	DifficultyHint string `json:"difficulty_hint,omitempty"`
}

// Profile is one persisted StrategyProfile.
type Profile struct {
	ID                string
	JobID             string
	ProjectID         string
	Archetype         string
	ExplorationPathID string
	Signature         []SignatureEntry
	TaskFeatures      TaskFeatures
	CreatedAt         time.Time
}

// Outcome is one persisted, immutable StrategyOutcome.
type Outcome struct {
	ID                  string
	JobID               string
	StrategyProfileID   string
	Result              string
	Score               float64
	EvaluatorConfidence float64
	Retries             int
	ReflectionLoops     int
	TokenCost           int
	WallTime            time.Duration
	// Operations is the executed cognitive trajectory (M8-T7): the ordered
	// operations that ran, with attributed cost and grounded verdicts.
	Operations []cognitive.Operation
	CreatedAt  time.Time
}

// Repo persists strategy records.
type Repo struct{ store *store.Store }

// NewRepo returns a repo backed by s.
func NewRepo(s *store.Store) *Repo { return &Repo{store: s} }

// CreateProfile records a job's profile. It is idempotent: a second call for
// the same job leaves the first record untouched (UNIQUE(job_id)).
func (r *Repo) CreateProfile(ctx context.Context, p Profile) (Profile, error) {
	if p.JobID == "" || p.ProjectID == "" {
		return Profile{}, fmt.Errorf("strategy: profile needs job_id and project_id")
	}
	sig, err := json.Marshal(nonNilSignature(p.Signature))
	if err != nil {
		return Profile{}, fmt.Errorf("marshalling signature: %w", err)
	}
	features, err := json.Marshal(p.TaskFeatures)
	if err != nil {
		return Profile{}, fmt.Errorf("marshalling task features: %w", err)
	}
	if _, err := r.store.DB().ExecContext(ctx,
		`INSERT INTO strategy_profiles (id, job_id, project_id, archetype, exploration_path_id, signature_json, task_features_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(job_id) DO NOTHING`,
		ids.New(), p.JobID, p.ProjectID, p.Archetype, nullIfEmpty(p.ExplorationPathID),
		string(sig), string(features),
	); err != nil {
		return Profile{}, fmt.Errorf("inserting strategy profile: %w", err)
	}
	return r.GetProfileByJob(ctx, p.JobID)
}

func nonNilSignature(s []SignatureEntry) []SignatureEntry {
	if s == nil {
		return []SignatureEntry{}
	}
	return s
}

// GetProfileByJob loads a job's profile.
func (r *Repo) GetProfileByJob(ctx context.Context, jobID string) (Profile, error) {
	row := r.store.DB().QueryRowContext(ctx,
		`SELECT id, job_id, project_id, archetype, COALESCE(exploration_path_id, ''), signature_json, task_features_json, created_at
		 FROM strategy_profiles WHERE job_id = ?`, jobID)
	var p Profile
	var sigJSON, featJSON, createdAt string
	err := row.Scan(&p.ID, &p.JobID, &p.ProjectID, &p.Archetype, &p.ExplorationPathID, &sigJSON, &featJSON, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, fmt.Errorf("%w: no profile for job %s", ErrNotFound, jobID)
	}
	if err != nil {
		return Profile{}, fmt.Errorf("loading strategy profile: %w", err)
	}
	if err := json.Unmarshal([]byte(sigJSON), &p.Signature); err != nil {
		return Profile{}, fmt.Errorf("decoding signature: %w", err)
	}
	if err := json.Unmarshal([]byte(featJSON), &p.TaskFeatures); err != nil {
		return Profile{}, fmt.Errorf("decoding task features: %w", err)
	}
	if p.CreatedAt, err = parseTS(createdAt); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// CreateOutcome records a job's immutable outcome, linked to its profile. It
// is idempotent (UNIQUE(job_id)); the profile must exist.
func (r *Repo) CreateOutcome(ctx context.Context, o Outcome) (Outcome, error) {
	if o.JobID == "" {
		return Outcome{}, fmt.Errorf("strategy: outcome needs job_id")
	}
	if !ValidResult(o.Result) {
		return Outcome{}, fmt.Errorf("strategy: invalid outcome result %q", o.Result)
	}
	profile, err := r.GetProfileByJob(ctx, o.JobID)
	if err != nil {
		return Outcome{}, fmt.Errorf("strategy: outcome requires a profile: %w", err)
	}
	ops, err := json.Marshal(nonNilOperations(o.Operations))
	if err != nil {
		return Outcome{}, fmt.Errorf("marshalling operations: %w", err)
	}
	if _, err := r.store.DB().ExecContext(ctx,
		`INSERT INTO strategy_outcomes
		   (id, job_id, strategy_profile_id, result, score, evaluator_confidence, retries, reflection_loops, token_cost, wall_time_ms, operations_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(job_id) DO NOTHING`,
		ids.New(), o.JobID, profile.ID, o.Result, o.Score, o.EvaluatorConfidence,
		o.Retries, o.ReflectionLoops, o.TokenCost, o.WallTime.Milliseconds(), string(ops),
	); err != nil {
		return Outcome{}, fmt.Errorf("inserting strategy outcome: %w", err)
	}
	return r.GetOutcomeByJob(ctx, o.JobID)
}

// GetOutcomeByJob loads a job's outcome.
func (r *Repo) GetOutcomeByJob(ctx context.Context, jobID string) (Outcome, error) {
	row := r.store.DB().QueryRowContext(ctx,
		`SELECT id, job_id, strategy_profile_id, result, score, evaluator_confidence, retries, reflection_loops, token_cost, wall_time_ms, operations_json, created_at
		 FROM strategy_outcomes WHERE job_id = ?`, jobID)
	var o Outcome
	var createdAt, opsJSON string
	var wallMS int64
	err := row.Scan(&o.ID, &o.JobID, &o.StrategyProfileID, &o.Result, &o.Score, &o.EvaluatorConfidence,
		&o.Retries, &o.ReflectionLoops, &o.TokenCost, &wallMS, &opsJSON, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Outcome{}, fmt.Errorf("%w: no outcome for job %s", ErrNotFound, jobID)
	}
	if err != nil {
		return Outcome{}, fmt.Errorf("loading strategy outcome: %w", err)
	}
	o.WallTime = time.Duration(wallMS) * time.Millisecond
	if err := json.Unmarshal([]byte(opsJSON), &o.Operations); err != nil {
		return Outcome{}, fmt.Errorf("decoding operations: %w", err)
	}
	if o.CreatedAt, err = parseTS(createdAt); err != nil {
		return Outcome{}, err
	}
	return o, nil
}

// ListOutcomes returns outcomes, newest first (limit <= 0 means no limit).
func (r *Repo) ListOutcomes(ctx context.Context, limit int) ([]Outcome, error) {
	q := `SELECT id, job_id, strategy_profile_id, result, score, evaluator_confidence, retries, reflection_loops, token_cost, wall_time_ms, operations_json, created_at
	      FROM strategy_outcomes ORDER BY created_at DESC, id DESC`
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, limit)
	}
	rows, err := r.store.DB().QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("listing strategy outcomes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Outcome
	for rows.Next() {
		var o Outcome
		var createdAt, opsJSON string
		var wallMS int64
		if err := rows.Scan(&o.ID, &o.JobID, &o.StrategyProfileID, &o.Result, &o.Score, &o.EvaluatorConfidence,
			&o.Retries, &o.ReflectionLoops, &o.TokenCost, &wallMS, &opsJSON, &createdAt); err != nil {
			return nil, err
		}
		o.WallTime = time.Duration(wallMS) * time.Millisecond
		if err := json.Unmarshal([]byte(opsJSON), &o.Operations); err != nil {
			return nil, err
		}
		t, err := parseTS(createdAt)
		if err != nil {
			return nil, err
		}
		o.CreatedAt = t
		out = append(out, o)
	}
	return out, rows.Err()
}

// RecentStats returns the number of recent outcomes for an archetype and the
// fraction that were accepted-new. It is the F4-T2 familiarity signal: a
// class with enough samples at a high accept rate can be treated as easy.
// limit <= 0 defaults to 20.
func (r *Repo) RecentStats(ctx context.Context, archetype string, limit int) (int, float64, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.store.DB().QueryContext(ctx,
		`SELECT o.result FROM strategy_outcomes o
		 JOIN strategy_profiles p ON p.id = o.strategy_profile_id
		 WHERE p.archetype = ?
		 ORDER BY o.created_at DESC, o.id DESC LIMIT ?`,
		archetype, limit)
	if err != nil {
		return 0, 0, fmt.Errorf("recent stats: %w", err)
	}
	defer func() { _ = rows.Close() }()
	samples, accepted := 0, 0
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return 0, 0, err
		}
		samples++
		if result == ResultAcceptedNew {
			accepted++
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if samples == 0 {
		return 0, 0, nil
	}
	return samples, float64(accepted) / float64(samples), nil
}

// Backfill creates a default profile and a derived outcome for terminal jobs
// that predate capture (§13.3). It returns how many outcomes it created.
//
// The store holds a single connection, so the job rows are drained before any
// insert.
func (r *Repo) Backfill(ctx context.Context) (int, error) {
	rows, err := r.store.DB().QueryContext(ctx,
		`SELECT j.id, j.project_id, j.state FROM jobs j
		 WHERE j.state IN ('completed','failed','cancelled')
		   AND NOT EXISTS (SELECT 1 FROM strategy_profiles p WHERE p.job_id = j.id)
		 ORDER BY j.created_at ASC, j.id ASC`)
	if err != nil {
		return 0, fmt.Errorf("listing backfill jobs: %w", err)
	}
	type legacy struct{ id, projectID, state string }
	var pending []legacy
	for rows.Next() {
		var l legacy
		if err := rows.Scan(&l.id, &l.projectID, &l.state); err != nil {
			_ = rows.Close()
			return 0, err
		}
		pending = append(pending, l)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	_ = rows.Close()

	created := 0
	for _, l := range pending {
		if _, err := r.CreateProfile(ctx, Profile{JobID: l.id, ProjectID: l.projectID, Archetype: "legacy"}); err != nil {
			return created, err
		}
		result := ResultRejected
		switch l.state {
		case "completed":
			result = ResultAcceptedNew
		case "failed":
			result = ResultFailed
		case "cancelled":
			result = ResultCancelled
		}
		if _, err := r.CreateOutcome(ctx, Outcome{JobID: l.id, Result: result}); err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}

func parseTS(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing strategy timestamp %q: %w", s, err)
	}
	return t, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
