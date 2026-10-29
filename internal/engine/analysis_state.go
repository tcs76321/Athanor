package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/tcs76321/athanor/internal/analysis"
)

// analysisPrefix is the job-scoped `system_state` key for the planning
// phase's structured decomposition (ADR-0090). It lives beside the reflection
// counter so a resumed job keeps its analysis without a migration.
const analysisPrefix = "analysis:"

func (e *Engine) setAnalysis(ctx context.Context, jobID string, a analysis.Analysis) error {
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = e.db.DB().ExecContext(ctx,
		`INSERT INTO system_state (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		analysisPrefix+jobID, string(raw))
	return err
}

// getAnalysis returns the stored analysis. Missing or corrupt state degrades to
// the one-unit direct fallback; analysis must never block a job.
func (e *Engine) getAnalysis(ctx context.Context, jobID string) (analysis.Analysis, bool) {
	var raw string
	err := e.db.DB().QueryRowContext(ctx,
		`SELECT value FROM system_state WHERE key = ?`, analysisPrefix+jobID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return analysis.Default(), false
	}
	if err != nil {
		return analysis.Default(), false
	}
	var a analysis.Analysis
	if json.Unmarshal([]byte(raw), &a) != nil || len(a.Units) == 0 {
		return analysis.Default(), false
	}
	return a, true
}
