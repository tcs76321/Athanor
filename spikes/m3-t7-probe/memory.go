// Memory-sequence runner (benchmark v1, memory scripts).
//
// A memory script is a sequence in one project (or across projects): a task, a
// correction or an operator insight, then a second task. The deterministic
// measure is the later task's own test/checks; the mechanism measure is whether
// the correction/insight was actually injected into the later job's prompt
// (event log). The runner never starts a daemon: it attaches to the Core Pod,
// exactly like `soak -external`.
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type memoryScriptFile struct {
	Version int            `yaml:"version"`
	Scripts []memoryScript `yaml:"scripts"`
}

type memoryScript struct {
	ID              string       `yaml:"id"`
	Title           string       `yaml:"title"`
	Archetype       string       `yaml:"archetype"`
	Tier            string       `yaml:"tier"`
	HeldOut         bool         `yaml:"held_out"`
	ContextDemand   string       `yaml:"context_demand"`
	Project         string       `yaml:"project"`
	Scope           string       `yaml:"scope"`
	Steps           []memoryStep `yaml:"steps"`
	AcceptanceFloor string       `yaml:"acceptance_floor"`
}

type memoryStep struct {
	Kind        string   `yaml:"kind"`
	Goal        string   `yaml:"goal"`
	Criteria    []string `yaml:"criteria"`
	TestCommand string   `yaml:"test_command"`
	Project     string   `yaml:"project"`
	Category    string   `yaml:"category"`
	Severity    string   `yaml:"severity"`
	Reason      string   `yaml:"reason"`
	Desired     string   `yaml:"desired"`
	Outcome     string   `yaml:"outcome"`
	Purpose     string   `yaml:"purpose"`
	Notes       string   `yaml:"notes"`
	Turns       int      `yaml:"turns"`
}

type memoryStepResult struct {
	Kind         string `json:"kind"`
	JobID        string `json:"job_id,omitempty"`
	State        string `json:"state,omitempty"`
	ArtifactID   string `json:"artifact_id,omitempty"`
	Pass         *bool  `json:"pass,omitempty"`
	Injected     *bool  `json:"injected,omitempty"`
	CorrectionID string `json:"correction_id,omitempty"`
	InsightID    string `json:"insight_id,omitempty"`
	Supported    bool   `json:"supported"`
	Detail       string `json:"detail,omitempty"`
}

type memoryScriptResult struct {
	ID     string             `json:"id"`
	Title  string             `json:"title"`
	Status string             `json:"status"` // pass | fail | partial | invalid
	Detail string             `json:"detail,omitempty"`
	Steps  []memoryStepResult `json:"steps"`
}

func loadMemoryScripts(path string) ([]memoryScript, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading memory scripts %s: %w", path, err)
	}
	var f memoryScriptFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing memory scripts %s: %w", path, err)
	}
	if f.Version != 1 {
		return nil, fmt.Errorf("memory scripts %s: version = %d, want 1", path, f.Version)
	}
	if len(f.Scripts) == 0 {
		return nil, fmt.Errorf("memory scripts %s: no scripts", path)
	}
	for _, s := range f.Scripts {
		if s.ID == "" || len(s.Steps) == 0 {
			return nil, fmt.Errorf("memory scripts %s: a script is missing id or steps", path)
		}
	}
	return f.Scripts, nil
}

// runMemory is the `memory` subcommand.
func runMemory(args []string) {
	fs := flag.NewFlagSet("memory", flag.ExitOnError)
	scriptsPath := fs.String("scripts", "eval/bench/memory-scripts.yaml", "memory scripts YAML")
	addr := fs.String("addr", strings.TrimPrefix(daemonURL(), "http://"), "Core Pod address (127.0.0.1:7420)")
	stateDir := fs.String("state-dir", "", "Core Pod state dir for injection evidence (optional)")
	outDir := fs.String("out", filepath.Join("spikes", "m3-t7-probe", "results", "memory"), "output directory")
	only := fs.String("only", "", "comma-separated script ids (empty = all)")
	timeout := fs.Duration("timeout", 45*time.Minute, "per-job wall-clock timeout")
	_ = fs.Parse(args)

	scripts, err := loadMemoryScripts(*scriptsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "memory:", err)
		os.Exit(1)
	}
	if *only != "" {
		want := map[string]bool{}
		for _, id := range strings.Split(*only, ",") {
			want[strings.TrimSpace(id)] = true
		}
		var selected []memoryScript
		for _, s := range scripts {
			if want[s.ID] {
				selected = append(selected, s)
				delete(want, s.ID)
			}
		}
		if len(want) > 0 {
			missing := make([]string, 0, len(want))
			for id := range want {
				missing = append(missing, id)
			}
			fmt.Fprintf(os.Stderr, "memory: unknown script id(s): %s\n", strings.Join(missing, ", "))
			os.Exit(2)
		}
		scripts = selected
	}

	abs, err := filepath.Abs(*outDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "memory:", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "memory:", err)
		os.Exit(1)
	}

	var db *sql.DB
	if *stateDir != "" {
		db, err = openResultsDB(*stateDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "memory: warning: injection evidence unavailable: %v\n", err)
			db = nil
		} else {
			defer func() { _ = db.Close() }()
		}
	}

	baseURL := "http://" + *addr
	results := make([]memoryScriptResult, 0, len(scripts))
	for _, s := range scripts {
		fmt.Printf("memory: %s (%d steps)\n", s.ID, len(s.Steps))
		results = append(results, runMemoryScript(baseURL, db, s, *timeout))
	}
	if err := writeJSON(filepath.Join(abs, "memory-results.json"), results); err != nil {
		fmt.Fprintln(os.Stderr, "memory:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(abs, "memory-report.md"), []byte(renderMemoryReport(results)), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "memory:", err)
		os.Exit(1)
	}
	fmt.Printf("memory: wrote %s\n", filepath.Join(abs, "memory-report.md"))
}

// runMemoryScript drives one sequence. The last task is the deterministic
// measure; day-1 is setup (the insight script expects day-1 to fail its edge
// cases). A pending correction/insight is checked on the next task.
func runMemoryScript(baseURL string, db *sql.DB, s memoryScript, timeout time.Duration) memoryScriptResult {
	res := memoryScriptResult{ID: s.ID, Title: s.Title}
	projectID, projectName := "", s.Project
	pendingCorrection, pendingInsight := "", ""
	for _, step := range s.Steps {
		switch step.Kind {
		case "task":
			if step.Project != "" && step.Project != projectName {
				projectName, projectID = step.Project, ""
			}
			if projectID == "" {
				pid, err := createMemoryProject(baseURL, projectName, s.Archetype, step)
				if err != nil {
					res.Detail = "create project: " + err.Error()
					res.Status = "fail"
					return res
				}
				projectID = pid
			}
			if step.TestCommand != "" {
				if err := patchMemoryExecution(baseURL, projectID, step.TestCommand); err != nil {
					res.Detail = "patch execution: " + err.Error()
					res.Status = "fail"
					return res
				}
			}
			jobID, err := submitMemoryGoal(baseURL, projectID, step)
			if err != nil {
				res.Detail = "submit goal: " + err.Error()
				res.Status = "fail"
				return res
			}
			state, artifactID, err := waitJobs(baseURL, []string{jobID}, timeout)
			if err != nil {
				res.Detail = "wait job: " + err.Error()
				res.Status = "fail"
				return res
			}
			pass := state == "completed" && artifactID != ""
			sr := memoryStepResult{
				Kind: "task", JobID: jobID, State: state, ArtifactID: artifactID,
				Pass: &pass, Supported: true,
			}
			if pendingCorrection != "" {
				if db != nil {
					injected := correctionInjected(db, jobID, pendingCorrection)
					sr.Injected = &injected
				} else {
					sr.Detail = "injection unverified (no state dir)"
				}
				pendingCorrection = ""
			}
			if pendingInsight != "" {
				if db != nil {
					injected := insightInjected(db, jobID)
					sr.Injected = &injected
				} else {
					sr.Detail = "injection unverified (no state dir)"
				}
				pendingInsight = ""
			}
			res.Steps = append(res.Steps, sr)

		case "correction":
			id, err := postMemoryCorrection(baseURL, projectID, s.Scope, step)
			if err != nil {
				res.Detail = "post correction: " + err.Error()
				res.Status = "fail"
				return res
			}
			pendingCorrection = id
			res.Steps = append(res.Steps, memoryStepResult{
				Kind: "correction", CorrectionID: id, Supported: true,
				Detail: step.Desired,
			})

		case "insight":
			id, err := postMemoryInsight(baseURL, step.Desired)
			if err != nil {
				res.Detail = "post insight: " + err.Error()
				res.Status = "fail"
				return res
			}
			pendingInsight = id
			res.Steps = append(res.Steps, memoryStepResult{
				Kind: "insight", InsightID: id, Supported: true,
				Detail: step.Desired,
			})

		case "filler":
			// The engine compacts within a job's message window; the script's
			// filler is a multi-turn construct the probe does not yet drive.
			// Record it honestly and still run the later task for data.
			res.Steps = append(res.Steps, memoryStepResult{
				Kind: "filler", Supported: false,
				Detail: fmt.Sprintf("compaction filler (%d turns) is not implemented; the later task is unverified for compaction", step.Turns),
			})

		default:
			res.Steps = append(res.Steps, memoryStepResult{
				Kind: step.Kind, Supported: false, Detail: "unknown step kind",
			})
		}
	}

	// The last task is the memory measure.
	lastTask := -1
	unsupported := false
	for i, sr := range res.Steps {
		if sr.Kind == "task" {
			lastTask = i
		}
		if !sr.Supported {
			unsupported = true
		}
	}
	switch {
	case unsupported:
		// The script was not executed as specified (e.g. the compaction filler
		// has no driver yet). Do not report a pass/fail of an unrun script.
		res.Status = "partial"
		res.Detail = "unsupported filler/step; the later task is not a pass/fail of the script as specified"
	case lastTask < 0:
		res.Status = "invalid"
		res.Detail = "no task step"
	case res.Steps[lastTask].Pass == nil || !*res.Steps[lastTask].Pass:
		res.Status = "fail"
		res.Detail = "day-2 task did not pass"
	case res.Steps[lastTask].Injected != nil && !*res.Steps[lastTask].Injected:
		res.Status = "fail"
		res.Detail = "day-2 task passed but the correction/insight was not injected"
	default:
		res.Status = "pass"
		if res.Steps[lastTask].Injected == nil {
			res.Detail = "day-2 passed; injection unverified (no state dir)"
		}
	}
	return res
}

func createMemoryProject(baseURL, name, archetype string, step memoryStep) (string, error) {
	if name == "" {
		name = "memory"
	}
	var out struct {
		ID string `json:"id"`
	}
	body := map[string]any{
		"name": name, "archetype": archetype,
		"goal": step.Goal, "acceptance_criteria": step.Criteria,
	}
	if err := apiCall("POST", baseURL+"/projects", body, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("project create returned no id")
	}
	return out.ID, nil
}

func patchMemoryExecution(baseURL, projectID, testCommand string) error {
	return apiCall("PATCH", baseURL+"/projects/"+projectID+"/execution",
		map[string]any{"test_command": testCommand}, nil)
}

func submitMemoryGoal(baseURL, projectID string, step memoryStep) (string, error) {
	var out struct {
		JobID string `json:"job_id"`
	}
	body := map[string]any{"goal": step.Goal, "acceptance_criteria": step.Criteria}
	if err := apiCall("POST", baseURL+"/projects/"+projectID+"/goals", body, &out); err != nil {
		return "", err
	}
	if out.JobID == "" {
		return "", fmt.Errorf("goal submit returned no job_id")
	}
	return out.JobID, nil
}

func postMemoryCorrection(baseURL, projectID, scope string, step memoryStep) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	body := map[string]any{
		"source": "user_correction", "category": step.Category,
		"severity": step.Severity, "reason": step.Reason,
		"desired_behavior": step.Desired, "scope": scope,
	}
	if err := apiCall("POST", baseURL+"/projects/"+projectID+"/corrections", body, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("correction returned no id")
	}
	return out.ID, nil
}

func postMemoryInsight(baseURL, statement string) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	body := map[string]any{"statement": statement, "scope": "global"}
	if err := apiCall("POST", baseURL+"/strategy/insights", body, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("insight returned no id")
	}
	return out.ID, nil
}

// correctionInjected reports whether the job's event log names the correction
// in a corrections_injected audit row. A nil db (no state dir) is unknown, not
// false; the caller records nil.
func correctionInjected(db *sql.DB, jobID, correctionID string) bool {
	if db == nil {
		return false
	}
	rows, err := db.Query(`SELECT data_json FROM events WHERE job_id = ?`, jobID)
	if err != nil {
		return false
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			continue
		}
		if strings.Contains(data, "corrections_injected") && strings.Contains(data, correctionID) {
			return true
		}
	}
	return false
}

// insightInjected reports whether the job's prompt included the strategy-notes
// section (the active-insight channel). The llm_call audit row carries the
// assembled sections.
func insightInjected(db *sql.DB, jobID string) bool {
	if db == nil {
		return false
	}
	rows, err := db.Query(`SELECT data_json FROM events WHERE job_id = ?`, jobID)
	if err != nil {
		return false
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			continue
		}
		if strings.Contains(data, `"name":"strategy_notes"`) {
			return true
		}
	}
	return false
}

func renderMemoryReport(results []memoryScriptResult) string {
	var b strings.Builder
	b.WriteString("# Memory scripts\n\n")
	b.WriteString("A memory script is a sequence, not a single job. The **last task** is the\n")
	b.WriteString("deterministic measure; the injection column is the mechanism check.\n\n")
	b.WriteString("| script | status | day-2 | injection | detail |\n|---|---|---|---|---|\n")
	for _, r := range results {
		day2, injection := "—", "—"
		for _, sr := range r.Steps {
			if sr.Kind == "task" && sr.Pass != nil {
				if *sr.Pass {
					day2 = "pass"
				} else {
					day2 = "fail"
				}
			}
			if sr.Injected != nil {
				if *sr.Injected {
					injection = "yes"
				} else {
					injection = "no"
				}
			}
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
			r.ID, r.Status, day2, injection, r.Detail)
	}
	for _, r := range results {
		fmt.Fprintf(&b, "\n## %s — %s\n\n", r.ID, r.Title)
		for i, sr := range r.Steps {
			fmt.Fprintf(&b, "%d. **%s**", i+1, sr.Kind)
			if sr.JobID != "" {
				fmt.Fprintf(&b, " job=%s state=%s", sr.JobID, sr.State)
			}
			if sr.Pass != nil {
				fmt.Fprintf(&b, " pass=%v", *sr.Pass)
			}
			if sr.Injected != nil {
				fmt.Fprintf(&b, " injected=%v", *sr.Injected)
			}
			if sr.CorrectionID != "" {
				fmt.Fprintf(&b, " correction=%s", sr.CorrectionID)
			}
			if sr.InsightID != "" {
				fmt.Fprintf(&b, " insight=%s", sr.InsightID)
			}
			if sr.Detail != "" {
				fmt.Fprintf(&b, " — %s", sr.Detail)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}
