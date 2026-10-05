package main

import (
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
		if m.JobID == "" || m.Winner != "" {
			continue
		}
		o, ok, err := loadOutcome(db, m.JobID)
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
