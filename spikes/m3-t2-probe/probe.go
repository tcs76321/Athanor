// Package main is the M3-T2 (commit 2.6) per-task probe.
// See probe_test.go for the test surface; this file is the
// production code.
//
// M3-T2's per-task probe runs 5 code-archetype goals through
// the live daemon and reports the rubric-coverage of the
// EvaluationRecords produced by the security persona. The
// goal is to confirm that:
//   - every EvaluationRecord has populated missing_criteria,
//     security_issues, and style_issues arrays (even if empty)
//   - rubric items the LLM should have flagged are actually
//     surfaced in the arrays
//   - the deterministic §19.3 guard (DecideWinner, commit 2.5)
//     correctly downgrades verdicts that lack a backing record
//
// The probe reuses the M1-T8 helper pattern
// (spikes/m1-quality-probe/) but is narrower: it focuses on
// the rubric contract, not the full M1 prose pipeline. The
// `package main` shape is the same; both probes talk to the
// daemon over the loopback HTTP API and never import
// `internal/*`.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// transitionEvent mirrors a subset of the events table.
type transitionEvent struct {
	ID       int64     `json:"id"`
	TS       time.Time `json:"ts"`
	Category string    `json:"category"`
	Level    string    `json:"level"`
	DataJSON string    `json:"data"`
}

// EvaluationRecordSummary is the audit-event shape the engine
// emits when an EvaluationRecord is persisted (see
// internal/evaluation/repo.go Create — the audit row's
// data_json carries every field the LLM produced). The probe
// reads this from the events log, not from a dedicated API
// endpoint, because the daemon's external API does not yet
// expose evaluation records directly (it will, in M3-T7).
type EvaluationRecordSummary struct {
	RecordID           string  `json:"record_id"`
	ArtifactID         string  `json:"artifact_id"`
	Score              float64 `json:"score"`
	PassedTests        bool    `json:"passed_tests"`
	BetterThanPrevious bool    `json:"better_than_previous"`
	Confidence         float64 `json:"confidence"`
}

// Result is one row of the probe's output table.
type Result struct {
	Number          int
	Goal            string
	Criteria        []string
	JobID           string
	JobState        string
	RecordsTotal    int
	RecordsPassed   int
	RecordsFailed   int
	BetterCount     int
	MissingCriteria []string
	SecurityIssues  []string
	StyleIssues     []string
	Notes           string
}

type projectCreateResp struct {
	ID     string `json:"id"`
	TaskID string `json:"task_id"`
}

type goalSubmitResp struct {
	TaskID string `json:"task_id"`
	JobID  string `json:"job_id"`
}

type jobGetResp struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

type eventsResp struct {
	Events []transitionEvent `json:"events"`
}

func daemonURL() string {
	if v := os.Getenv("ATHANOR_ADDR"); v != "" {
		return v
	}
	return "http://127.0.0.1:7420"
}

// apiCall posts/gets a single JSON request and decodes a
// single JSON response. The body is the standard
// `internal/api` response shape; errors are returned as
// `error` with a wrapped daemon-side message.
func apiCall(method, url string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = strings.NewReader(string(raw))
	}
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("daemon unreachable at %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("daemon %s %s: %s", resp.Status, url, string(raw))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func createProject(name, archetype, goal string, criteria []string) (string, string, error) {
	var out projectCreateResp
	err := apiCall("POST", daemonURL()+"/projects", map[string]any{
		"name":                name,
		"archetype":           archetype,
		"goal":                goal,
		"acceptance_criteria": criteria,
	}, &out)
	return out.ID, out.TaskID, err
}

func submitGoal(projectID, goal string, criteria []string) (string, string, error) {
	var out goalSubmitResp
	err := apiCall("POST", daemonURL()+"/projects/"+projectID+"/goals", map[string]any{
		"goal":                goal,
		"acceptance_criteria": criteria,
	}, &out)
	return out.TaskID, out.JobID, err
}

func getJob(jobID string) (*jobGetResp, error) {
	var out jobGetResp
	if err := apiCall("GET", daemonURL()+"/jobs/"+jobID, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func getEvents(jobID string) ([]transitionEvent, error) {
	var out eventsResp
	if err := apiCall("GET", daemonURL()+"/jobs/"+jobID+"/events", nil, &out); err != nil {
		return nil, err
	}
	return out.Events, nil
}

func waitTerminal(jobID string, timeout time.Duration) (*jobGetResp, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		j, err := getJob(jobID)
		if err != nil {
			return nil, err
		}
		switch j.State {
		case "completed", "failed", "cancelled":
			return j, nil
		}
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("timeout waiting for job %s to reach a terminal state", jobID)
}

// evaluationRecordAudit is the full shape of the
// `evaluation_record_created` audit row written by
// evaluation.Repo.Create. The probe reads the array fields
// directly so it can report rubric coverage.
type evaluationRecordAudit struct {
	Event              string   `json:"event"`
	RecordID           string   `json:"record_id"`
	ArtifactID         string   `json:"artifact_id"`
	Score              float64  `json:"score"`
	PassedTests        bool     `json:"passed_tests"`
	BetterThanPrevious bool     `json:"better_than_previous"`
	Confidence         float64  `json:"confidence"`
	MissingCriteria    []string `json:"missing_criteria"`
	SecurityIssues     []string `json:"security_issues"`
	StyleIssues        []string `json:"style_issues"`
}

// parseEvaluationRecords extracts every
// `evaluation_record_created` audit row from the events log
// and returns the full audit shape (so the probe can read
// missing_criteria / security_issues / style_issues
// directly, not just the summary fields).
func parseEvaluationRecords(events []transitionEvent) []evaluationRecordAudit {
	var out []evaluationRecordAudit
	for _, e := range events {
		var probe struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal([]byte(e.DataJSON), &probe); err != nil {
			continue
		}
		if probe.Event != "evaluation_record_created" {
			continue
		}
		var r evaluationRecordAudit
		if err := json.Unmarshal([]byte(e.DataJSON), &r); err != nil {
			continue
		}
		out = append(out, r)
	}
	return out
}

// rubricCoverage is the per-goal aggregate the probe
// reports. The fields are derived from the union of every
// record's array fields, with the per-record pass/fail and
// better-than-previous counts.
type rubricCoverage struct {
	Total           int
	Passed          int
	Better          int
	MissingCriteria []string // union, deduped, sorted
	SecurityIssues  []string // union, deduped, sorted
	StyleIssues     []string // union, deduped, sorted
	HasMissing      bool     // at least one record has any missing_criteria
	HasSecurity     bool     // at least one record has any security_issues
	HasStyle        bool     // at least one record has any style_issues
}

// computeRubricCoverage aggregates per-record arrays into a
// coverage report. The probe asserts that the LLM actually
// surfaces rubric items (vs. silently passing everything).
// For a §19 contract, the persona must echo every unmet
// rubric item into one of the three arrays; "everything
// passes" with empty arrays is the failure mode the probe
// is designed to catch.
func computeRubricCoverage(records []evaluationRecordAudit) rubricCoverage {
	uniq := func(s []string) []string {
		seen := map[string]struct{}{}
		out := make([]string, 0, len(s))
		for _, x := range s {
			if _, ok := seen[x]; ok {
				continue
			}
			seen[x] = struct{}{}
			out = append(out, x)
		}
		return out
	}
	var (
		missing, security, style []string
	)
	c := rubricCoverage{Total: len(records)}
	for _, r := range records {
		if r.PassedTests {
			c.Passed++
		}
		if r.BetterThanPrevious {
			c.Better++
		}
		if len(r.MissingCriteria) > 0 {
			c.HasMissing = true
		}
		if len(r.SecurityIssues) > 0 {
			c.HasSecurity = true
		}
		if len(r.StyleIssues) > 0 {
			c.HasStyle = true
		}
		missing = append(missing, r.MissingCriteria...)
		security = append(security, r.SecurityIssues...)
		style = append(style, r.StyleIssues...)
	}
	c.MissingCriteria = sortedStrings(uniq(missing))
	c.SecurityIssues = sortedStrings(uniq(security))
	c.StyleIssues = sortedStrings(uniq(style))
	return c
}

func sortedStrings(s []string) []string {
	out := make([]string, len(s))
	copy(out, s)
	// Insertion sort: the inputs are tiny (a few
	// entries per record, ≤ 3 records per goal).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

func renderMarkdownRow(r Result) string {
	missing := strings.Join(r.MissingCriteria, ";")
	if missing == "" {
		missing = "—"
	}
	security := strings.Join(r.SecurityIssues, ";")
	if security == "" {
		security = "—"
	}
	style := strings.Join(r.StyleIssues, ";")
	if style == "" {
		style = "—"
	}
	return strings.Join([]string{
		itoa(r.Number),
		trunc(r.Goal, 60),
		strings.Join(r.Criteria, ";"),
		trunc(r.JobID, 12),
		r.JobState,
		itoa(r.RecordsTotal),
		itoa(r.RecordsPassed),
		itoa(r.RecordsFailed),
		itoa(r.BetterCount),
		missing,
		security,
		style,
		r.Notes,
	}, " | ")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

// sample is the M3-T2 per-task probe's input. The 5 code-
// archetype samples are designed so every rubric item has
// at least one goal where it should fire:
//
//	1 fibonacci-clean: clean baseline. All rubric items
//	  should pass (empty arrays).
//	2 stringutils-no-docs: missing docstrings.
//	  DOCSTRINGS rubric item should fire.
//	3 cache-with-todo: has TODO. NO PLACEHOLDERS rubric
//	  item should fire.
//	4 always-reverse-impossible: returns reversed input.
//	  The "always" claim is impossible. TESTS PASS may be
//	  false, or the persona may flag the over-claim.
//	5 todo-list-clean: clean. All rubric items should pass.
type sample struct {
	Number   int
	Name     string
	Goal     string
	Criteria []string
}

var samples = []sample{
	{1, "fibonacci-clean", "Write a Python function that returns the n-th Fibonacci number using recursion.", []string{"pure stdlib", "docstrings on every public function", "a usage example"}},
	{2, "stringutils-no-docs", "Write a Python module with three utility functions for trimming, padding, and reversing strings.", []string{"pure stdlib", "docstrings on every public function"}},
	{3, "cache-with-todo", "Write a Python cache class with get, set, and evict methods.", []string{"pure stdlib", "no TODO or FIXME placeholders"}},
	{4, "always-reverse-impossible", "Write a Python function that returns its input reversed. The function must always succeed.", []string{"pure stdlib", "tests pass"}},
	{5, "todo-list-clean", "Write a Python class for managing a todo list, with add, complete, list_pending, and clear methods.", []string{"pure stdlib", "docstrings on every public function", "no TODO or FIXME placeholders"}},
}

func runOne(s sample) (Result, error) {
	r := Result{Number: s.Number, Goal: s.Goal, Criteria: s.Criteria}

	projectID, _, err := createProject(s.Name, "code", s.Goal, s.Criteria)
	if err != nil {
		return r, fmt.Errorf("createProject: %w", err)
	}

	_, jobID, err := submitGoal(projectID, s.Goal, s.Criteria)
	if err != nil {
		return r, fmt.Errorf("submitGoal: %w", err)
	}
	r.JobID = jobID

	j, err := waitTerminal(jobID, 10*time.Minute)
	if err != nil {
		return r, fmt.Errorf("waitTerminal: %w", err)
	}
	r.JobState = j.State

	events, err := getEvents(jobID)
	if err != nil {
		return r, fmt.Errorf("getEvents: %w", err)
	}
	records := parseEvaluationRecords(events)
	r.RecordsTotal = len(records)
	for _, rec := range records {
		if rec.PassedTests {
			r.RecordsPassed++
		} else {
			r.RecordsFailed++
		}
		if rec.BetterThanPrevious {
			r.BetterCount++
		}
	}
	cov := computeRubricCoverage(records)
	r.MissingCriteria = cov.MissingCriteria
	r.SecurityIssues = cov.SecurityIssues
	r.StyleIssues = cov.StyleIssues
	return r, nil
}

func writeResults(path string, results []Result) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	header := "| # | goal | criteria | job_id | state | records | passed | failed | better | missing | security | style | notes |\n"
	header += "|---|---|---|---|---|---|---|---|---|---|---|---|---|"
	if _, err := f.WriteString(header); err != nil {
		return err
	}
	for _, r := range results {
		if _, err := f.WriteString("\n| " + renderMarkdownRow(r) + " |"); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	resultsDir := os.Getenv("PROBE_RESULTS_DIR")
	if resultsDir == "" {
		resultsDir = "m3-t2-probe-results"
	}
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := apiCall("GET", daemonURL()+"/healthz", nil, nil); err != nil {
		fmt.Fprintf(os.Stderr, "daemon not reachable at %s/healthz: %v\n", daemonURL(), err)
		os.Exit(1)
	}
	fmt.Printf("daemon OK at %s; results dir %s\n", daemonURL(), resultsDir)
	fmt.Printf("running %d code-archetype sample goals sequentially...\n\n", len(samples))

	results := make([]Result, 0, len(samples))
	for _, s := range samples {
		fmt.Printf("=== sample %d: %s ===\n", s.Number, s.Name)
		r, err := runOne(s)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: sample %d: %v\n", s.Number, err)
			r.Notes = "FAILED: " + err.Error()
			results = append(results, r)
			continue
		}
		fmt.Printf("  job_id=%s state=%s records=%d passed=%d failed=%d better=%d\n",
			r.JobID, r.JobState, r.RecordsTotal, r.RecordsPassed, r.RecordsFailed, r.BetterCount)
		results = append(results, r)
	}

	tablePath := resultsDir + "/probe-results.md"
	if err := writeResults(tablePath, results); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("\nwrote results table to %s\n", tablePath)
	fmt.Println("The findings will be recorded in docs/probes/m3-t2-probe.md.")
}
