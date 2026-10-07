// Package main is the M3-T7 dialectical-vs-single-shot
// quality probe.
//
// M3-T7 (ROADMAP §6 M3, §7 M3-T7-a/b/c) measures the
// dialectical loop against a single-shot baseline on
// three axes. The label mapping below is the
// authoritative one — it matches ROADMAP §7 and the
// runbook docs/probes/m3-t7-quality-probe.md. (An
// earlier scaffold had the mapping rotated; that was a
// stale copy, corrected in M3-T7.0.)
//
//  1. Diversity (T-a). Across the N candidate
//     artifacts produced in one dialectical run,
//     measure the average pairwise Jaccard distance.
//     A diverse candidate set is the value-add of
//     divergence; a low-diversity set means the
//     engine is just retrying the same output. The
//     metric already lands in internal/engine/diverge.go
//     as the `divergence_jaccard` event (commit
//     a11ab63); this probe aggregates it.
//
//  2. Calibration (T-b). For each sample, compare
//     the LLM's reported confidence in the winning
//     verdict against the actual outcome (the
//     rubric-graded EvaluationRecord's score). A
//     well-calibrated loop reports confidence that
//     matches observed accuracy; a miscalibrated one
//     over- or under-claims.
//
//  3. Stability at T=0 (T-c). Re-run the same sample
//     N times and check that the winning verdict (and
//     its score) is reproducible. A stable loop
//     produces the same winner across runs; an
//     unstable one is brittle to sampling and
//     backend/numeric noise (Ollama is not bit-exact
//     at T=0; see the runbook's determinism section).
//
// The headline experiment — does N-candidate
// divergence + deterministic evaluation/comparison
// beat a single-candidate (N=1) baseline? — is the
// reason this probe exists; T-a/b/c are the supporting
// diagnostics.
//
// This probe is the planning + scaffolding commit. The
// measurements land as separate spike commands in
// follow-up work (they need a running daemon, a model,
// and time — none of which a CI run can supply). The
// scaffold lays out the result-type contract and the
// per-sample runner so the follow-up work is a series
// of small `main` edits, not a redesign.
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

// main dispatches the probe subcommands. Both require a built daemon
// binary (`make build`): `run` manages the daemon lifecycle itself, and
// `report` aggregates whatever results have been collected. The probe
// never starts work without a binary and a clean results tree.
func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "run":
		runMatrix(os.Args[2:])
	case "soak":
		runSoak(os.Args[2:])
	case "report":
		runReport(os.Args[2:])
	case "config":
		printConfig(os.Args[2:])
	case "judge":
		runJudge(os.Args[2:])
	case "anchor":
		runAnchor(os.Args[2:])
	case "bundle":
		runBundle(os.Args[2:])
	case "reconcile":
		runReconcile(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Print(`usage: m3-t7-probe <command> [flags]

commands:
  run       run the measurement matrix (manages the daemon lifecycle)
  soak      combined endurance + quality soak: one daemon, resumable, early-stop (M8-T23)
  report    aggregate collected results into report.md
  config    print the generated daemon config for one model/arm
  judge     score the collected packets with the third-party judge models
  anchor    score the human-anchor set and report judge agreement
  bundle    write a labeled, human-readable artifact bundle (artifacts.md)
  reconcile re-read outcomes from the state DB to repair missed rows, then re-run report

Run each command with -h for its flags. Protocol:
docs/probes/m3-t7-quality-probe.md
`)
}
