package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// runReconcile repairs results.json files whose strategy outcomes were not
// captured live. The engine sets a job's `finished_at` at the terminal
// transition but writes the strategy outcome only after the Job Pod tears
// down — a ~10s lag for code goals that can exceed the collector's wait — so
// the DB can hold outcomes the results file is missing. Run after a matrix
// finishes, then re-run `report`.
func runReconcile(args []string) {
	fs := flag.NewFlagSet("reconcile", flag.ExitOnError)
	out := fs.String("out", "spikes/m3-t7-probe/results", "results dir to reconcile")
	_ = fs.Parse(args)
	if err := reconcileDir(*out); err != nil {
		fmt.Fprintln(os.Stderr, "reconcile:", err)
		os.Exit(1)
	}
}

// reconcileDir walks a results tree (one or more <model>/<arm>/results.json)
// and re-reads each job's outcome from the arm's state DB, rewriting the
// results.json and results.csv for anything the live collector missed.
func reconcileDir(outDir string) error {
	matches, err := filepath.Glob(filepath.Join(outDir, "*", "*", "results.json"))
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return fmt.Errorf("no results.json under %s", outDir)
	}
	for _, path := range matches {
		runDir := filepath.Dir(path)
		var metrics []jobMetrics
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &metrics); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		updated, err := reconcileMetrics(filepath.Join(runDir, "state"), metrics)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if err := writeJSON(path, metrics); err != nil {
			return err
		}
		if err := writeCSV(filepath.Join(runDir, "results.csv"), metrics); err != nil {
			return err
		}
		fmt.Printf("reconciled %s (%d rows, %d updated)\n", path, len(metrics), updated)
	}
	return nil
}

// reconcileMetrics fills any metric whose strategy outcome was not captured
// live, reading it from the arm's state DB. A row is a candidate when it has
// a job id but no recorded winner (rows collected live always have one:
// "new", "previous", or "none"). Returns the number of rows updated.
func reconcileMetrics(stateDir string, metrics []jobMetrics) (int, error) {
	db, err := openResultsDB(stateDir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	updated := 0
	for i := range metrics {
		m := &metrics[i]
		if m.JobID == "" {
			continue
		}
		// Refresh the T-a metrics too: the live collector's json_extract
		// always failed, so the stored diversity may be the artifact fallback
		// rather than the documented per-cycle value. Non-fatal.
		_ = m.fillCandidateMetrics(db, stateDir)
		if m.Winner != "" {
			continue
		}
		o, ok, err := loadOutcome(db, m.JobID)
		if err != nil || !ok {
			// The row can be missing entirely when the daemon is torn down
			// inside a code goal's ~10s capture window (the last job of an
			// arm). Rebuild it from the evaluation records and the
			// comparison event, mirroring the engine's captureOutcome.
			o, ok, err = deriveOutcome(db, m.JobID)
		}
		if err != nil || !ok {
			continue
		}
		m.Winner = winnerFromResult(o.Result)
		m.Score = o.Score
		m.Confidence = o.Confidence
		m.TokenCost = o.TokenCost
		m.WallMS = o.WallMS
		m.Retries = o.Retries
		m.ReflectionLoops = o.ReflectionLoops
		updated++
	}
	return updated, nil
}

// deriveOutcome rebuilds a job's outcome when the strategy_outcomes row was
// never written. It mirrors the engine's captureOutcome: the score is the max
// evaluation-record score, the winner and confidence come from the last
// `comparison` event, and token cost is the sum of the job's `llm_call`
// events. ok is false when there is no evaluation to derive from.
func deriveOutcome(db *sql.DB, jobID string) (outcomeRow, bool, error) {
	var o outcomeRow

	var score sql.NullFloat64
	if err := db.QueryRow(
		`SELECT MAX(score) FROM evaluation_records WHERE job_id = ?`, jobID).Scan(&score); err != nil {
		return o, false, err
	}
	if !score.Valid {
		return o, false, nil
	}
	o.Score = score.Float64

	// The probe's SQLite build has no JSON1 (`json_extract` errors), so scan
	// the job's events and filter in Go — the same way the engine's
	// eventStats does. Ordering by id DESC means the first comparison seen is
	// the last one recorded.
	rows, err := db.Query(
		`SELECT data_json FROM events
		  WHERE job_id = ? AND category = 'jobs'
		  ORDER BY id DESC`, jobID)
	if err != nil {
		return o, false, err
	}
	defer func() { _ = rows.Close() }()
	found := false
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return o, false, err
		}
		var ev struct {
			Event            string  `json:"event"`
			Winner           string  `json:"winner"`
			Confidence       float64 `json:"confidence"`
			PromptTokens     int     `json:"prompt_tokens"`
			CompletionTokens int     `json:"completion_tokens"`
		}
		if json.Unmarshal([]byte(d), &ev) != nil {
			continue
		}
		switch ev.Event {
		case "comparison":
			if !found {
				o.Confidence = ev.Confidence
				switch ev.Winner {
				case "new":
					o.Result = "accepted_new"
				case "previous":
					o.Result = "accepted_previous"
				default:
					o.Result = "rejected"
				}
				found = true
			}
		case "llm_call":
			o.TokenCost += ev.PromptTokens + ev.CompletionTokens
		}
	}
	if err := rows.Err(); err != nil {
		return o, false, err
	}
	if !found {
		return o, false, nil // no comparison — cannot name a winner
	}
	return o, true, nil
}
