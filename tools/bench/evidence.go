package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// runEvidence is P2/F5's first slice: reduce an existing daemon state dir into
// the same jobMetrics/report shape the soak uses (docs/evidence-pipeline.md §7).
// It is read-only over the state DB and never submits work.
func runEvidence(args []string) {
	fs := flag.NewFlagSet("evidence", flag.ExitOnError)
	stateDir := fs.String("state-dir", "state", "daemon state dir containing athanor.db")
	outDir := fs.String("out", "", "output dir (default <state-dir>/evidence)")
	_ = fs.Parse(args)

	if *outDir == "" {
		*outDir = filepath.Join(*stateDir, "evidence")
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "evidence:", err)
		os.Exit(1)
	}
	db, err := openResultsDB(*stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "evidence: open state:", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	metrics, err := evidenceRows(db, *stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "evidence: reduce:", err)
		os.Exit(1)
	}
	if err := writeJSON(filepath.Join(*outDir, "results.json"), metrics); err != nil {
		fmt.Fprintln(os.Stderr, "evidence: write results.json:", err)
		os.Exit(1)
	}
	if err := writeCSV(filepath.Join(*outDir, "results.csv"), metrics); err != nil {
		fmt.Fprintln(os.Stderr, "evidence: write results.csv:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(*outDir, "report.md"), []byte(renderReport(metrics)), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "evidence: write report.md:", err)
		os.Exit(1)
	}
	fmt.Printf("evidence: %d job(s) -> %s\n", len(metrics), *outDir)
}

// evidenceRows reads completed/terminal jobs and their project/task context.
// The model label is the most frequent llm_call model for the job; a job with
// no calls is labelled "unknown".
func evidenceRows(db *sql.DB, stateDir string) ([]jobMetrics, error) {
	rows, err := db.Query(`
		SELECT j.id, j.state, j.project_id, p.archetype, t.title
		  FROM jobs j
		  JOIN projects p ON p.id = j.project_id
		  JOIN tasks t ON t.id = j.task_id
		 ORDER BY j.created_at, j.id`)
	if err != nil {
		return nil, err
	}
	// The daemon database is single-connection (ADR-0027). Drain the base rows
	// and close them before the per-job queries, or modelLabelFor waits forever
	// for the connection the open rows hold.
	type baseRow struct{ id, state, projectID, archetype, title string }
	var base []baseRow
	for rows.Next() {
		var b baseRow
		if err := rows.Scan(&b.id, &b.state, &b.projectID, &b.archetype, &b.title); err != nil {
			_ = rows.Close()
			return nil, err
		}
		base = append(base, b)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	out := make([]jobMetrics, 0, len(base))
	for _, b := range base {
		label := modelLabelFor(db, b.id)
		m := jobMetrics{
			GoalName: truncateLabel(b.title, 80), Archetype: b.archetype, State: b.state,
			Arm: "real", Run: 1, Tier: "IRL", JobID: b.id,
			ModelLabel: label, Model: label,
		}
		if o, ok, err := loadOutcome(db, b.id); err == nil && ok {
			m.Winner = winnerFromResult(o.Result)
			m.Score = o.Score
			m.Confidence = o.Confidence
			m.TokenCost = o.TokenCost
			m.WallMS = o.WallMS
			m.Retries = o.Retries
			m.ReflectionLoops = o.ReflectionLoops
		}
		_ = m.fillCandidateMetrics(db, stateDir)
		m.fillVerification(db)
		m.fillDecisionDiagnostics(db)
		m.fillTelemetry(db)
		out = append(out, m)
	}
	return out, nil
}

// truncateLabel keeps a state-dir task title readable in a report table.
func truncateLabel(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// modelLabelFor returns the most frequent llm_call model in a job's event log.
func modelLabelFor(db *sql.DB, jobID string) string {
	rows, err := db.Query(
		`SELECT data_json FROM events WHERE job_id = ? AND category = 'jobs' ORDER BY id`, jobID)
	if err != nil {
		return "unknown"
	}
	defer func() { _ = rows.Close() }()
	counts := map[string]int{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		var d struct {
			Event string `json:"event"`
			Model string `json:"model"`
		}
		if json.Unmarshal([]byte(raw), &d) != nil || d.Event != "llm_call" || d.Model == "" {
			continue
		}
		counts[d.Model]++
	}
	best, bestN := "unknown", 0
	for model, n := range counts {
		if n > bestN || (n == bestN && model < best) {
			best, bestN = model, n
		}
	}
	return best
}
