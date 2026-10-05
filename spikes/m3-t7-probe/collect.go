package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// jobMetrics is everything the probe captures for one job (one goal, one
// model, one arm, one run). It is the row the analytics in analysis.go
// and report.go consume.
type jobMetrics struct {
	GoalName        string  `json:"goal_name"`
	GoalNumber      int     `json:"goal_number"`
	Archetype       string  `json:"archetype"`
	ModelLabel      string  `json:"model_label"`
	Model           string  `json:"model"`
	Arm             string  `json:"arm"`
	Run             int     `json:"run"`
	JobID           string  `json:"job_id"`
	State           string  `json:"state"`
	ArtifactID      string  `json:"artifact_id,omitempty"`
	Winner          string  `json:"winner"`
	Confidence      float64 `json:"confidence"`
	Score           float64 `json:"score"`
	TokenCost       int     `json:"token_cost"`
	WallMS          int     `json:"wall_ms"`
	Retries         int     `json:"retries"`
	ReflectionLoops int     `json:"reflection_loops"`
	Diversity       float64 `json:"diversity"`
	Candidates      int     `json:"candidates"`
	// ArtifactText is the final artifact's content. It is used to build
	// judge packets and is not serialized into results.json (the file is
	// read back from the state dir when packets are generated).
	ArtifactText string `json:"-"`
	// Error is set when the job could not be completed or collected.
	Error string `json:"error,omitempty"`
}

// openResultsDB opens the daemon's SQLite database read-only so the probe
// can read captured outcomes without disturbing the writer. WAL mode
// allows a concurrent read-only reader.
func openResultsDB(stateDir string) (*sql.DB, error) {
	path := filepath.Join(stateDir, "athanor.db")
	db, err := sql.Open("sqlite3", "file:"+path+"?mode=ro&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// outcomeRow mirrors the columns the probe reads from strategy_outcomes
// (M6-T10). It is the primary per-job signal: result, winning score,
// evaluator confidence, tokens, wall time, and loop counts.
type outcomeRow struct {
	Result          string
	Score           float64
	Confidence      float64
	TokenCost       int
	WallMS          int
	Retries         int
	ReflectionLoops int
}

// loadOutcome reads the strategy_outcomes row for a job, if present. The
// boolean is false when the row does not exist (e.g. the job failed
// before the terminal capture).
func loadOutcome(db *sql.DB, jobID string) (outcomeRow, bool, error) {
	var o outcomeRow
	row := db.QueryRow(
		`SELECT result, score, evaluator_confidence, token_cost, wall_time_ms, retries, reflection_loops
		   FROM strategy_outcomes WHERE job_id = ?`, jobID)
	if err := row.Scan(&o.Result, &o.Score, &o.Confidence, &o.TokenCost, &o.WallMS, &o.Retries, &o.ReflectionLoops); err != nil {
		if err == sql.ErrNoRows {
			return o, false, nil
		}
		return o, false, err
	}
	return o, true, nil
}

// winnerFromResult maps a strategy-outcome result to a §13.1 comparison
// winner. A failed/cancelled/rejected job is treated as winner "none".
func winnerFromResult(result string) string {
	switch result {
	case "accepted_new":
		return "new"
	case "accepted_previous":
		return "previous"
	default:
		return "none"
	}
}

// loadCandidateIDs returns a job's divergence candidates (proposal
// artifacts), oldest first, for the T-a diversity metric.
func loadCandidateIDs(db *sql.DB, jobID string) ([]string, error) {
	rows, err := db.Query(
		`SELECT id FROM artifacts WHERE job_id = ? AND kind = 'proposal' ORDER BY created_at`, jobID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// readArtifact reads an artifact's content from <stateDir>/artifacts/<id>.
func readArtifact(stateDir, id string) (string, error) {
	if id == "" {
		return "", nil
	}
	b, err := os.ReadFile(filepath.Join(stateDir, "artifacts", id))
	if err != nil {
		return "", fmt.Errorf("reading artifact %s: %w", id, err)
	}
	return string(b), nil
}

// loadDivergenceJaccard returns the most recent `divergence_jaccard`
// event's (avg_jaccard, candidates) for a job. The engine records one
// event per divergence cycle; the last cycle is the one whose candidates
// reached comparison. This is the correct per-cycle T-a metric — deriving
// it from all proposal artifacts would mix reflection cycles (the single
// arm can produce several one-candidate cycles, each with diversity 0).
func loadDivergenceJaccard(db *sql.DB, jobID string) (avg float64, candidates int, ok bool) {
	var data string
	err := db.QueryRow(
		`SELECT data_json FROM events
		  WHERE job_id = ? AND json_extract(data_json, '$.event') = 'divergence_jaccard'
		  ORDER BY id DESC LIMIT 1`, jobID).Scan(&data)
	if err != nil {
		return 0, 0, false
	}
	var m struct {
		AvgJaccard float64 `json:"avg_jaccard"`
		Candidates int     `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return 0, 0, false
	}
	return m.AvgJaccard, m.Candidates, true
}

// fillCandidateMetrics records the job's candidate count and diversity
// (T-a). It prefers the engine's per-cycle `divergence_jaccard` event and
// falls back to computing from the proposal artifacts when the event is
// absent (older jobs).
func (m *jobMetrics) fillCandidateMetrics(db *sql.DB, stateDir string) error {
	if avg, n, ok := loadDivergenceJaccard(db, m.JobID); ok {
		m.Diversity = avg
		m.Candidates = n
		return nil
	}
	ids, err := loadCandidateIDs(db, m.JobID)
	if err != nil {
		return err
	}
	cands := make([]candidate, 0, len(ids))
	for _, id := range ids {
		text, err := readArtifact(stateDir, id)
		if err != nil {
			return err
		}
		cands = append(cands, candidate{ArtifactID: id, Text: text, Length: len(text)})
	}
	m.Candidates = len(cands)
	m.Diversity = avgPairwiseJaccard(cands)
	return nil
}
