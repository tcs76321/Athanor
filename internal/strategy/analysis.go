package strategy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/tcs76321/athanor/internal/ids"
)

// Insight statuses (§13.4 lifecycle).
const (
	InsightProposed = "proposed"
	InsightActive   = "active"
	InsightMuted    = "muted"
	InsightRetired  = "retired"
)

// Insight polarities (§13.4).
const (
	PolarityWinning = "winning"
	PolarityLosing  = "losing"
)

// Scopes.
const (
	ScopeGlobal  = "global"
	ScopeProject = "project"
)

// Pattern names the strategy feature an insight is about (§13.4).
type Pattern struct {
	Feature string `json:"feature"`
	Value   string `json:"value"`
	Context string `json:"context"`
}

// Evidence is an insight's inline, numeric support (§13.4).
type Evidence struct {
	CohortJobs         int     `json:"cohort_jobs"`
	AcceptRate         float64 `json:"accept_rate"`
	BaselineAcceptRate float64 `json:"baseline_accept_rate"`
	MedianScore        float64 `json:"median_score"`
	MedianRetries      int     `json:"median_retries"`
	MedianTokenCost    int     `json:"median_token_cost"`
}

// Insight is one persisted StrategyInsight.
type Insight struct {
	ID        string
	Scope     string
	Polarity  string
	Pattern   Pattern
	Evidence  Evidence
	Statement string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// MineOptions bounds insight detection (§13.4).
type MineOptions struct {
	MinCohortSize      int
	MinAcceptRateDelta float64
	BaselineArchetype  string // optional: restrict mining to one archetype
}

// outcomeRow is one outcome joined with its profile, for aggregation.
type outcomeRow struct {
	result     string
	confidence float64
	score      float64
	retries    int
	tokenCost  int
	archetype  string
	signature  []SignatureEntry
}

// isAccepted reports whether a §13.3 result counts as an acceptance.
func isAccepted(r string) bool {
	return r == ResultAcceptedNew || r == ResultAcceptedPrevious
}

// Mine runs §13.4's deterministic aggregation and returns the newly proposed
// insights. It invents no numbers: every field comes from the outcomes, and
// the statement is rendered from them.
func (r *Repo) Mine(ctx context.Context, opts MineOptions) ([]Insight, error) {
	if opts.MinCohortSize <= 0 {
		return nil, fmt.Errorf("strategy: min cohort size must be positive")
	}
	rows, err := r.outcomeRows(ctx)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	// Baseline accept rate per archetype.
	baselineByArch := map[string]float64{}
	confByArch := map[string]float64{}
	{
		sum := map[string]int{}
		acc := map[string]int{}
		conf := map[string]float64{}
		for _, o := range rows {
			sum[o.archetype]++
			if isAccepted(o.result) {
				acc[o.archetype]++
			}
			conf[o.archetype] += o.confidence
		}
		for arch, n := range sum {
			baselineByArch[arch] = float64(acc[arch]) / float64(n)
			confByArch[arch] = conf[arch] / float64(n)
		}
	}

	// Group outcomes by (feature, value, archetype).
	type key struct{ feature, value, archetype string }
	cohorts := map[key][]outcomeRow{}
	for _, o := range rows {
		for _, entry := range o.signature {
			cohorts[key{entry.Phase + ".persona", entry.Persona, o.archetype}] = append(
				cohorts[key{entry.Phase + ".persona", entry.Persona, o.archetype}], o)
			if entry.Candidates > 0 {
				cv := strconv.Itoa(entry.Candidates)
				cohorts[key{"diverging.candidates", cv, o.archetype}] = append(
					cohorts[key{"diverging.candidates", cv, o.archetype}], o)
			}
		}
	}

	keys := make([]key, 0, len(cohorts))
	for k := range cohorts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].feature != keys[j].feature {
			return keys[i].feature < keys[j].feature
		}
		if keys[i].value != keys[j].value {
			return keys[i].value < keys[j].value
		}
		return keys[i].archetype < keys[j].archetype
	})

	var proposed []Insight
	for _, k := range keys {
		if opts.BaselineArchetype != "" && k.archetype != opts.BaselineArchetype {
			continue
		}
		cohort := cohorts[k]
		if len(cohort) < opts.MinCohortSize {
			continue
		}
		acc := 0
		var conf float64
		for _, o := range cohort {
			if isAccepted(o.result) {
				acc++
			}
			conf += o.confidence
		}
		acceptRate := float64(acc) / float64(len(cohort))
		baseline := baselineByArch[k.archetype]
		delta := acceptRate - baseline
		if abs(delta) < opts.MinAcceptRateDelta {
			continue
		}
		// Directionally-consistent confidence: a winning cohort must not be
		// less confident than the baseline, and vice versa.
		meanConf := conf / float64(len(cohort))
		if (delta > 0 && meanConf+1e-9 < confByArch[k.archetype]) ||
			(delta < 0 && meanConf-1e-9 > confByArch[k.archetype]) {
			continue
		}
		polarity := PolarityWinning
		if delta < 0 {
			polarity = PolarityLosing
		}
		pattern := Pattern{Feature: k.feature, Value: k.value, Context: "archetype=" + k.archetype}
		// Dedup: skip an identical live insight.
		if r.insightExists(ctx, ScopeGlobal, pattern) {
			continue
		}
		statement := fmt.Sprintf("%s=%s on %s tasks: accept rate %.2f vs %.2f baseline (n=%d).",
			k.feature, k.value, k.archetype, acceptRate, baseline, len(cohort))
		ins, err := r.CreateInsight(ctx, Insight{
			Scope: ScopeGlobal, Polarity: polarity, Pattern: pattern,
			Evidence: Evidence{
				CohortJobs: len(cohort), AcceptRate: acceptRate, BaselineAcceptRate: baseline,
				MedianScore: medianScore(cohort), MedianRetries: medianInt(cohort, func(o outcomeRow) int { return o.retries }),
				MedianTokenCost: medianInt(cohort, func(o outcomeRow) int { return o.tokenCost }),
			},
			Statement: statement, Status: InsightProposed,
		})
		if err != nil {
			return proposed, err
		}
		proposed = append(proposed, ins)
	}
	return proposed, nil
}

// outcomeRows loads outcomes joined with their profiles.
func (r *Repo) outcomeRows(ctx context.Context) ([]outcomeRow, error) {
	rows, err := r.store.DB().QueryContext(ctx,
		`SELECT o.result, o.score, o.evaluator_confidence, o.retries, o.token_cost, p.archetype, p.signature_json
		 FROM strategy_outcomes o JOIN strategy_profiles p ON p.id = o.strategy_profile_id`)
	if err != nil {
		return nil, fmt.Errorf("loading outcomes for mining: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []outcomeRow
	for rows.Next() {
		var result, archetype, sigJSON string
		var o outcomeRow
		if err := rows.Scan(&result, &o.score, &o.confidence, &o.retries, &o.tokenCost, &archetype, &sigJSON); err != nil {
			return nil, err
		}
		o.result = result
		o.archetype = archetype
		if err := json.Unmarshal([]byte(sigJSON), &o.signature); err != nil {
			continue
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (r *Repo) insightExists(ctx context.Context, scope string, p Pattern) bool {
	raw, _ := json.Marshal(p)
	var n int
	err := r.store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM strategy_insights WHERE scope = ? AND pattern_json = ? AND status IN ('proposed','active')`,
		scope, string(raw)).Scan(&n)
	return err == nil && n > 0
}

// CreateInsight inserts a proposed insight.
func (r *Repo) CreateInsight(ctx context.Context, in Insight) (Insight, error) {
	if in.Status == "" {
		in.Status = InsightProposed
	}
	patternJSON, _ := json.Marshal(in.Pattern)
	evidenceJSON, _ := json.Marshal(in.Evidence)
	id := ids.New()
	if _, err := r.store.DB().ExecContext(ctx,
		`INSERT INTO strategy_insights (id, scope, polarity, pattern_json, evidence_json, statement, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, in.Scope, in.Polarity, string(patternJSON), string(evidenceJSON), in.Statement, in.Status,
	); err != nil {
		return Insight{}, fmt.Errorf("inserting strategy insight: %w", err)
	}
	return r.GetInsight(ctx, id)
}

// GetInsight loads one insight.
func (r *Repo) GetInsight(ctx context.Context, id string) (Insight, error) {
	row := r.store.DB().QueryRowContext(ctx,
		`SELECT id, scope, polarity, pattern_json, evidence_json, statement, status, created_at, updated_at
		 FROM strategy_insights WHERE id = ?`, id)
	ins, err := scanInsight(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Insight{}, fmt.Errorf("%w: no insight %s", ErrNotFound, id)
	}
	if err != nil {
		return Insight{}, err
	}
	return ins, nil
}

// ListInsights returns insights in the given status ("" means all), newest
// first.
func (r *Repo) ListInsights(ctx context.Context, status string) ([]Insight, error) {
	q := `SELECT id, scope, polarity, pattern_json, evidence_json, statement, status, created_at, updated_at FROM strategy_insights`
	var args []any
	if status != "" {
		q += ` WHERE status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC, id DESC`
	rows, err := r.store.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("listing insights: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Insight
	for rows.Next() {
		ins, err := scanInsight(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ins)
	}
	return out, rows.Err()
}

// SetInsightStatus moves an insight through its §13.4 lifecycle.
func (r *Repo) SetInsightStatus(ctx context.Context, id, status string) error {
	switch status {
	case InsightProposed, InsightActive, InsightMuted, InsightRetired:
	default:
		return fmt.Errorf("strategy: invalid insight status %q", status)
	}
	res, err := r.store.DB().ExecContext(ctx,
		`UPDATE strategy_insights SET status = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("setting insight status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: no insight %s", ErrNotFound, id)
	}
	return nil
}

// ActiveStatements returns the statements of *active* insights only — the
// §13.4 prompt channel. Proposed insights are deliberately excluded, which is
// what makes them provably inert until promoted.
func (r *Repo) ActiveStatements(ctx context.Context) ([]string, error) {
	list, err := r.ListInsights(ctx, InsightActive)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(list))
	for _, ins := range list {
		out = append(out, ins.Statement)
	}
	return out, nil
}

func scanInsight(row interface{ Scan(...any) error }) (Insight, error) {
	var ins Insight
	var patternJSON, evidenceJSON, createdAt, updatedAt string
	if err := row.Scan(&ins.ID, &ins.Scope, &ins.Polarity, &patternJSON, &evidenceJSON,
		&ins.Statement, &ins.Status, &createdAt, &updatedAt); err != nil {
		return Insight{}, err
	}
	if err := json.Unmarshal([]byte(patternJSON), &ins.Pattern); err != nil {
		return Insight{}, fmt.Errorf("decoding insight pattern: %w", err)
	}
	if err := json.Unmarshal([]byte(evidenceJSON), &ins.Evidence); err != nil {
		return Insight{}, fmt.Errorf("decoding insight evidence: %w", err)
	}
	var err error
	if ins.CreatedAt, err = parseTS(createdAt); err != nil {
		return Insight{}, err
	}
	if ins.UpdatedAt, err = parseTS(updatedAt); err != nil {
		return Insight{}, err
	}
	return ins, nil
}

func medianScore(rows []outcomeRow) float64 {
	if len(rows) == 0 {
		return 0
	}
	vals := make([]float64, 0, len(rows))
	for _, o := range rows {
		vals = append(vals, o.score)
	}
	sort.Float64s(vals)
	return vals[len(vals)/2]
}

func medianInt(rows []outcomeRow, pick func(outcomeRow) int) int {
	if len(rows) == 0 {
		return 0
	}
	vals := make([]int, 0, len(rows))
	for _, o := range rows {
		vals = append(vals, pick(o))
	}
	sort.Ints(vals)
	return vals[len(vals)/2]
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
