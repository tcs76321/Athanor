// Package main is the M3-T7 dialectical-vs-single-shot
// quality probe.
//
// M3-T7 (ROADMAP §7, M3-T7-a/b/c) measures the
// dialectical loop against a single-shot baseline on
// three axes:
//
//  1. Calibration (T-a). For each sample, compare
//     the LLM's reported confidence in the winning
//     verdict against the actual outcome (the
//     rubric-graded EvaluationRecord's score). A
//     well-calibrated loop reports confidence that
//     matches observed accuracy; a miscalibrated one
//     over- or under-claims.
//
//  2. Stability at T=0 (T-b). Re-run the same sample
//     N times with a fixed seed and check that the
//     winning artifact's score has a low variance. A
//     stable loop produces comparable quality across
//     runs; an unstable one is brittle to sampling
//     noise.
//
//  3. Diversity (T-c). Across the N candidate
//     artifacts produced in one dialectical run,
//     measure the average pairwise Jaccard distance.
//     A diverse candidate set is the value-add of
//     divergence; a low-diversity set means the
//     engine is just retrying the same output.
//
// This probe is the planning + scaffolding commit. The
// three sub-measurements land as separate spike
// commands in follow-up work (the experiments need a
// running daemon, a model, and time — none of which a
// CI run can supply). The scaffold lays out the
// result-type contract and the per-sample runner so
// the follow-up work is a series of small `main` edits,
// not a redesign.
//
// The probe reuses the M1-T8 / M3-T2 helper pattern:
// talk to the daemon over loopback HTTP, never import
// internal/*.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

// dialecticalResult is one row of the probe's
// per-sample output. The three sub-measurements
// (calibration, stability, diversity) all read from
// this struct.
type dialecticalResult struct {
	Number   int
	Goal     string
	JobID    string
	JobState string
	// ReportedConfidence is the LLM's verdict-time
	// confidence (from the §13.1 comparison JSON).
	ReportedConfidence float64
	// ObservedScore is the EvaluationRecord's score
	// for the winning candidate (the rubric's
	// numerical grade, 0.0–1.0).
	ObservedScore float64
	// Candidates is the set of divergence candidates
	// the engine produced (used by the diversity
	// sub-measurement).
	Candidates []candidate
	// StabilityVariance is the per-sample variance of
	// ObservedScore across N re-runs (used by the
	// stability sub-measurement; filled by the
	// stability runner, not the per-sample one).
	StabilityVariance float64
	Notes             string
}

// candidate is one divergence candidate artifact, in
// the form the diversity sub-measurement consumes
// (truncated text + length).
type candidate struct {
	ArtifactID string
	Text       string
	Length     int
}

type healthResponse struct {
	OK bool `json:"ok"`
}

func daemonURL() string {
	if v := os.Getenv("ATHANOR_ADDR"); v != "" {
		return v
	}
	return "http://127.0.0.1:7420"
}

func apiCall(method, url string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func main() {
	resultsDir := os.Getenv("PROBE_RESULTS_DIR")
	if resultsDir == "" {
		resultsDir = "m3-t7-probe-results"
	}
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := apiCall("GET", daemonURL()+"/healthz", nil, &healthResponse{}); err != nil {
		fmt.Fprintf(os.Stderr, "daemon not reachable at %s/healthz: %v\n", daemonURL(), err)
		os.Exit(1)
	}
	fmt.Printf("M3-T7 probe (scaffold). Daemon OK at %s; results dir %s\n", daemonURL(), resultsDir)
	fmt.Println("Sub-measurements (T-a calibration, T-b stability at T=0, T-c diversity Jaccard)")
	fmt.Println("land in follow-up commits; this scaffold defines the result type contract only.")
	fmt.Println("See docs/probes/m3-t7-probe.md (also a follow-up commit).")
}
