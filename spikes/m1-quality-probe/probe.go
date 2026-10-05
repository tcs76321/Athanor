// Package main is the M1-T8 quality probe helper. See probe_test.go for
// the test surface; this file is the production code.
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

// Result is one row of the probe's output table.
type Result struct {
	Number        int
	Archetype     string
	Goal          string
	Criteria      []string
	JobID         string
	JobWallTime   time.Duration
	PhaseDur      map[string]time.Duration
	TotalCalls    int
	TotalPrompt   int
	TotalCompl    int
	ArtifactBytes int
	Adherence     string
	Usefulness    string
	Notes         string
}

// llmCallSummary aggregates token counts and call counts by phase from
// the engine's `llm_call` events.
type llmCallSummary struct {
	TotalCalls         int
	TotalPromptTok     int
	TotalCompletionTok int
	PerPhase           map[string]phaseLLM
}

type phaseLLM struct {
	Calls     int
	PromptTok int
	ComplTok  int
}

// parseTransitionSequence extracts the "to" field of every
// "transition" event in id order.
func parseTransitionSequence(events []transitionEvent) []string {
	var out []string
	for _, e := range events {
		var d struct {
			Event string `json:"event"`
			To    string `json:"to"`
		}
		if err := json.Unmarshal([]byte(e.DataJSON), &d); err != nil {
			continue
		}
		if d.Event == "transition" {
			out = append(out, d.To)
		}
	}
	return out
}

// computePhaseDurations maps a phase name (planning, diverging,
// synthesizing, comparing) to the wall time spent executing it. The
// time spent is the gap between the transition INTO the phase and the
// transition OUT of it, which captures both the LLM call and any
// artifact persistence work. Phases without an LLM call (e.g. the
// deterministic comparison) are still measured this way.
//
// The M1 engine records the transition immediately after the LLM call
// returns, so this gap equals the LLM call duration plus a few
// milliseconds of overhead — i.e. the user-visible "time spent in
// phase X".
func computePhaseDurations(events []transitionEvent) map[string]time.Duration {
	durs := map[string]time.Duration{}
	if len(events) == 0 {
		return durs
	}
	// Index every event by its position so we can walk forward from a
	// transition to find the next transition (regardless of intervening
	// llm_call / comparison / context_floor_violation events).
	var lastTransitionTS time.Time
	var lastTransitionTo string
	for _, e := range events {
		var d struct {
			Event string `json:"event"`
			To    string `json:"to"`
		}
		if err := json.Unmarshal([]byte(e.DataJSON), &d); err != nil {
			continue
		}
		if d.Event != "transition" {
			continue
		}
		if !lastTransitionTS.IsZero() && lastTransitionTo != "" {
			// Time spent in lastTransitionTo = e.TS - lastTransitionTS.
			durs[lastTransitionTo] = e.TS.Sub(lastTransitionTS)
		}
		lastTransitionTS = e.TS
		lastTransitionTo = d.To
	}
	return durs
}

func summarizeLLMCalls(events []transitionEvent) llmCallSummary {
	s := llmCallSummary{PerPhase: map[string]phaseLLM{}}
	for _, e := range events {
		var d struct {
			Event            string `json:"event"`
			Phase            string `json:"phase"`
			Persona          string `json:"persona"`
			Model            string `json:"model"`
			PromptTokens     int    `json:"prompt_tokens"`
			CompletionTokens int    `json:"completion_tokens"`
		}
		if err := json.Unmarshal([]byte(e.DataJSON), &d); err != nil {
			continue
		}
		if d.Event != "llm_call" {
			continue
		}
		s.TotalCalls++
		s.TotalPromptTok += d.PromptTokens
		s.TotalCompletionTok += d.CompletionTokens
		ps := s.PerPhase[d.Phase]
		ps.Calls++
		ps.PromptTok += d.PromptTokens
		ps.ComplTok += d.CompletionTokens
		s.PerPhase[d.Phase] = ps
	}
	return s
}

func detectContextFloorViolation(events []transitionEvent) (found bool, recommendation string) {
	for _, e := range events {
		var d struct {
			Event          string `json:"event"`
			Recommendation string `json:"recommendation"`
		}
		if err := json.Unmarshal([]byte(e.DataJSON), &d); err != nil {
			continue
		}
		if d.Event == "context_floor_violation" {
			return true, d.Recommendation
		}
	}
	return false, ""
}

// renderMarkdownRow produces a fixed-width table row for one sample.
// Columns: # | archetype | goal | criteria | job_id | total_s |
// planning_s | diverging_s | synthesizing_s | comparing_s | calls |
// prompt_tok | compl_tok | artifact_bytes | adherence | usefulness | notes
func renderMarkdownRow(r Result) string {
	get := func(k string) time.Duration { return r.PhaseDur[k] }
	return strings.Join([]string{
		itoa(r.Number),
		r.Archetype,
		trunc(r.Goal, 50),
		trunc(strings.Join(r.Criteria, "; "), 60),
		trunc(r.JobID, 8),
		fmtDur(r.JobWallTime),
		fmtDur(get("planning")),
		fmtDur(get("diverging")),
		fmtDur(get("synthesizing")),
		fmtDur(get("comparing")),
		itoa(r.TotalCalls),
		itoa(r.TotalPrompt),
		itoa(r.TotalCompl),
		itoa(r.ArtifactBytes),
		r.Adherence,
		r.Usefulness,
		trunc(r.Notes, 60),
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
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func fmtDur(d time.Duration) string {
	if d == 0 {
		return "0"
	}
	return ftoa1(d.Seconds()) + "s"
}

func ftoa1(x float64) string {
	if x < 0 {
		return "-" + ftoa1(-x)
	}
	whole := int(x)
	frac := int((x - float64(whole)) * 10)
	return itoa(whole) + "." + itoa(frac)
}

// HTTP plumbing (integration-tested by running the probe).

const defaultAddr = "http://127.0.0.1:7420"

func daemonURL() string {
	if v := os.Getenv("ATHANOR_ADDR"); v != "" {
		return v
	}
	return defaultAddr
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
	ID         string `json:"id"`
	State      string `json:"state"`
	TaskID     string `json:"task_id"`
	ProjectID  string `json:"project_id"`
	ArtifactID string `json:"artifact_id"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	PausedFrom string `json:"paused_from"`
}

type eventsResp struct {
	Events []transitionEvent `json:"events"`
}

func apiCall(method, url string, body any, out any) error {
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = strings.NewReader(string(raw))
	}
	req, err := http.NewRequest(method, url, bodyReader)
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

func readArtifact(stateDir, artifactID string) ([]byte, string, error) {
	// Artifact content lives at <stateDir>/artifacts/<id>. The
	// daemon's API does not expose raw content (§21.8), so the probe
	// reads the file directly.
	path := stateDir + "/artifacts/" + artifactID
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("artifact %s not found at %s: %w", artifactID, path, err)
	}
	return data, path, nil
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
		case "paused":
			return j, fmt.Errorf("job %s paused (paused_from=%s)", jobID, j.PausedFrom)
		}
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("job %s did not reach a terminal state within %v", jobID, timeout)
}

// sample is one row of the protocol's sample-goal table.
type sample struct {
	Number    int
	Name      string
	Archetype string
	Goal      string
	Criteria  []string
}

// samples is the canonical 5-goal set from docs/probes/m1-quality-probe.md.
var samples = []sample{
	{1, "essay-local-first", "text",
		"Write a short essay about why local-first software matters.",
		[]string{"at least three arguments", "a conclusion"}},
	{2, "onboarding-email", "text",
		"Draft a friendly onboarding email for a new community member of a local software club.",
		[]string{"under 200 words", "one clear call to action"}},
	{3, "python-book-module", "code",
		"Write a Python module that manages a personal book collection with add, list, and search functions.",
		[]string{"pure stdlib", "docstrings on every public function", "a usage example"}},
	{4, "md2html-readme", "document",
		"Create a README for a small CLI tool that converts Markdown files to HTML.",
		[]string{"installation section", "usage examples", "license section"}},
	{5, "sunrise-alarm-brief", "document",
		"Write a one-page design brief for a weekend project that builds a sunrise alarm clock from a Raspberry Pi.",
		[]string{"parts list", "build steps", "at least two risks named"}},
}

func runOne(s sample, stateDir string) (Result, []byte, error) {
	r := Result{
		Number:    s.Number,
		Archetype: s.Archetype,
		Goal:      s.Goal,
		Criteria:  s.Criteria,
	}

	projectID, _, err := createProject(s.Name, s.Archetype, s.Goal, s.Criteria)
	if err != nil {
		return r, nil, fmt.Errorf("createProject: %w", err)
	}

	_, jobID, err := submitGoal(projectID, s.Goal, s.Criteria)
	if err != nil {
		return r, nil, fmt.Errorf("submitGoal: %w", err)
	}
	r.JobID = jobID

	j, err := waitTerminal(jobID, 10*time.Minute)
	if err != nil {
		return r, nil, fmt.Errorf("waitTerminal: %w", err)
	}

	events, err := getEvents(jobID)
	if err != nil {
		return r, nil, fmt.Errorf("getEvents: %w", err)
	}
	r.PhaseDur = computePhaseDurations(events)
	llmSum := summarizeLLMCalls(events)
	r.TotalCalls = llmSum.TotalCalls
	r.TotalPrompt = llmSum.TotalPromptTok
	r.TotalCompl = llmSum.TotalCompletionTok

	// Compute job wall time from started_at/finished_at if available,
	// else fall back to first-event → last-event delta.
	if j.StartedAt != "" && j.FinishedAt != "" {
		t0, e0 := time.Parse(time.RFC3339Nano, j.StartedAt)
		t1, e1 := time.Parse(time.RFC3339Nano, j.FinishedAt)
		if e0 == nil && e1 == nil {
			r.JobWallTime = t1.Sub(t0)
		}
	}
	if r.JobWallTime == 0 && len(events) > 1 {
		r.JobWallTime = events[len(events)-1].TS.Sub(events[0].TS)
	}

	var artifactContent []byte
	if j.ArtifactID != "" {
		content, _, rerr := readArtifact(stateDir, j.ArtifactID)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "WARN: readArtifact(%s) = %v (job %s)\n", j.ArtifactID, rerr, jobID)
		} else {
			artifactContent = content
			r.ArtifactBytes = len(content)
		}
	}

	return r, artifactContent, nil
}

func writeResults(path string, results []Result) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	header := "| # | archetype | goal | criteria | job_id | total_s | plan_s | diverge_s | synth_s | compare_s | calls | prompt_tok | compl_tok | artifact_bytes | adherence | usefulness | notes |\n"
	header += "|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n"
	if _, err := f.WriteString(header); err != nil {
		return err
	}
	for _, r := range results {
		if _, err := f.WriteString(renderMarkdownRow(r) + "\n"); err != nil {
			return err
		}
	}
	return nil
}

func writeArtifact(path string, n int, content []byte) error {
	return os.WriteFile(path, content, 0o600)
}

func main() {
	stateDir := os.Getenv("ATHANOR_STATE_DIR")
	if stateDir == "" {
		stateDir = "state-probe"
	}
	resultsDir := os.Getenv("PROBE_RESULTS_DIR")
	if resultsDir == "" {
		resultsDir = stateDir + "/probe"
	}
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Sanity check: daemon reachable.
	if err := apiCall("GET", daemonURL()+"/healthz", nil, nil); err != nil {
		fmt.Fprintf(os.Stderr, "daemon not reachable at %s/healthz: %v\n", daemonURL(), err)
		os.Exit(1)
	}
	fmt.Printf("daemon OK at %s; state dir %s; results dir %s\n", daemonURL(), stateDir, resultsDir)
	fmt.Printf("running %d sample goals sequentially...\n\n", len(samples))

	results := make([]Result, 0, len(samples))
	for _, s := range samples {
		fmt.Printf("=== sample %d: %s (%s) ===\n", s.Number, s.Name, s.Archetype)
		r, content, err := runOne(s, stateDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: sample %d: %v\n", s.Number, err)
			r.Notes = "FAILED: " + err.Error()
			results = append(results, r)
			continue
		}
		fmt.Printf("  job_id=%s wall=%v phase=%v calls=%d prompt_tok=%d compl_tok=%d artifact=%dB\n",
			r.JobID, r.JobWallTime, r.PhaseDur, r.TotalCalls, r.TotalPrompt, r.TotalCompl, r.ArtifactBytes)
		results = append(results, r)
		if len(content) > 0 {
			_ = writeArtifact(fmt.Sprintf("%s/artifact-%d-%s.txt", resultsDir, s.Number, s.Name), 0, content)
		}
	}

	tablePath := resultsDir + "/probe-results.md"
	if err := writeResults(tablePath, results); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("\nwrote results table to %s\n", tablePath)
	fmt.Println("Fill in the adherence/usefulness/notes columns manually; the")
	fmt.Println("findings will be recorded in docs/probes/m1-quality-probe.md.")
}
