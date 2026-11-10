// v4 cycle arm (ADR-0135): drive the product's actual authoring path — project
// → template draft → start — and score the produced brief against the corpus's
// deterministic checks. The probe runs inside the Core Pod, so it talks to the
// daemon over loopback and reads the artifact files from the state directory.
//
// This is deliberately a thin driver: creation, polling, collection, and
// scoring. It does not execute a block; the daemon's scheduler does.
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type cycleConfig struct {
	addr     string
	corpus   string
	stateDir string
	outDir   string
	model    string
	template string
	only     string
	suite    string
	timeout  time.Duration
	interval time.Duration
}

type cycleResults struct {
	Version   int               `json:"version"`
	Arm       string            `json:"arm"`
	Model     string            `json:"model"`
	StartedAt string            `json:"started_at"`
	Tasks     []cycleTaskResult `json:"tasks"`
}

type cycleTaskResult struct {
	ID              string        `json:"id"`
	Pass            bool          `json:"pass"`
	CycleID         string        `json:"cycle_id"`
	State           string        `json:"state"`
	ArtifactID      string        `json:"artifact_id,omitempty"`
	Blocks          int           `json:"blocks"`
	BlocksCompleted int           `json:"blocks_completed"`
	Checks          []checkResult `json:"checks,omitempty"`
	Failures        []string      `json:"failures,omitempty"`
	DurationSeconds float64       `json:"duration_s"`
}

func runCycle(args []string) {
	c := &cycleConfig{}
	fs := flag.NewFlagSet("cycle", flag.ExitOnError)
	fs.StringVar(&c.addr, "addr", "127.0.0.1:7420", "daemon listen address")
	fs.StringVar(&c.corpus, "corpus", "eval/bench/v4/brief.yaml", "v4 corpus file")
	fs.StringVar(&c.stateDir, "state-dir", "/var/lib/athanor/state", "Core state directory")
	fs.StringVar(&c.outDir, "out", "/bench/out", "results directory")
	fs.StringVar(&c.model, "model", "bonsai27b", "generator label (metadata; the Core config selects the model)")
	fs.StringVar(&c.template, "template", "", "fallback template when a task does not name one")
	fs.StringVar(&c.only, "only", "", "comma-separated task ids (empty = all)")
	fs.StringVar(&c.suite, "suite", "", "only run tasks whose suite field matches")
	fs.DurationVar(&c.timeout, "timeout", 30*time.Minute, "per-task wall-clock timeout")
	fs.DurationVar(&c.interval, "interval", 5*time.Second, "poll interval")
	_ = fs.Parse(args)

	goals, err := loadCorpus(c.corpus)
	if err != nil {
		fatal("cycle: %v", err)
	}
	only := map[string]bool{}
	for _, id := range strings.Split(c.only, ",") {
		if id = strings.TrimSpace(id); id != "" {
			only[id] = true
		}
	}
	results := cycleResults{
		Version: 4, Arm: "cycle", Model: c.model,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	base := "http://" + c.addr
	for _, g := range goals {
		if len(only) > 0 && !only[g.Name] {
			continue
		}
		if c.suite != "" && g.Suite != c.suite {
			continue
		}
		tpl := g.Template
		if tpl == "" {
			tpl = c.template
		}
		if tpl == "" {
			tpl = "daily-brief"
		}
		start := time.Now()
		res, err := runOneCycle(client, base, c, g.Name, g.Goal, g.Sources, tpl, g.Repo, g.Checks, g.ArtifactTitle)
		res.DurationSeconds = time.Since(start).Seconds()
		if err != nil {
			res.ID, res.Pass = g.Name, false
			res.Failures = append(res.Failures, err.Error())
		}
		results.Tasks = append(results.Tasks, res)
		logf("cycle: %s pass=%v state=%s blocks=%d/%d (%.0fs)",
			g.Name, res.Pass, res.State, res.BlocksCompleted, res.Blocks, res.DurationSeconds)
	}

	if err := os.MkdirAll(c.outDir, 0o755); err != nil {
		fatal("cycle: output dir: %v", err)
	}
	raw, _ := json.MarshalIndent(results, "", "  ")
	name := filepath.Join(c.outDir, "cycle-"+c.model+".json")
	if err := os.WriteFile(name, append(raw, '\n'), 0o644); err != nil {
		fatal("cycle: writing results: %v", err)
	}
	md := renderCycleReport(results, goals)
	if err := os.WriteFile(filepath.Join(c.outDir, "cycle-"+c.model+".md"), []byte(md), 0o644); err != nil {
		fatal("cycle: writing report: %v", err)
	}
	fmt.Printf("cycle: wrote %s (%d tasks)\n", name, len(results.Tasks))
}

func runOneCycle(client *http.Client, base string, c *cycleConfig, name, goal string, sources []string, template, repo string, checks map[string]any, artifactTitle string) (cycleTaskResult, error) {
	res := cycleTaskResult{ID: name}
	projectID, err := createProject(client, base, name, goal, sources, repo)
	if err != nil {
		return res, fmt.Errorf("create project: %w", err)
	}
	cycleID, err := createCycle(client, base, projectID, template)
	if err != nil {
		return res, fmt.Errorf("create cycle: %w", err)
	}
	res.CycleID = cycleID
	if err := startCycle(client, base, cycleID); err != nil {
		return res, fmt.Errorf("start cycle: %w", err)
	}
	state, err := waitCycle(client, base, cycleID, c.timeout, c.interval)
	res.State = state
	if err != nil {
		return res, err
	}
	db, err := openReadOnly(filepath.Join(c.stateDir, "athanor.db"))
	if err != nil {
		return res, fmt.Errorf("open state: %w", err)
	}
	defer func() { _ = db.Close() }()

	blocks, done, err := cycleBlocks(db, cycleID)
	if err != nil {
		return res, err
	}
	res.Blocks, res.BlocksCompleted = blocks, done
	if state != "completed" {
		res.Failures = append(res.Failures, "cycle state is "+state)
	}
	if blocks == 0 || done != blocks {
		res.Failures = append(res.Failures, fmt.Sprintf("blocks completed %d/%d", done, blocks))
	}
	artifactID, content, err := latestAcceptedDocument(db, c.stateDir, projectID, cycleID, artifactTitle)
	if err != nil {
		res.Failures = append(res.Failures, err.Error())
		return res, nil
	}
	res.ArtifactID = artifactID
	pass, results := runChecks(checks, content)
	res.Checks = results
	if !pass {
		for _, r := range results {
			if !r.Pass && !r.Advisory {
				res.Failures = append(res.Failures, r.Kind+": "+r.Detail)
			}
		}
	}
	res.Pass = pass && len(res.Failures) == 0
	return res, nil
}

// checkMapForTask was replaced by passing the task's checks directly.

func createProject(client *http.Client, base, name, goal string, sources []string, repo string) (string, error) {
	form := url.Values{
		"name":       {name},
		"goal":       {goal},
		"boundaries": {"Read only the declared sources; do not invent a source."},
		"sources":    {strings.Join(sources, "\n")},
	}
	if repo != "" {
		abs, err := filepath.Abs(repo)
		if err == nil {
			form.Set("repository_path", abs)
		}
	}
	resp, err := client.PostForm(base+"/ui/projects", form)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}
	loc := resp.Header.Get("Location")
	const prefix = "/ui/projects/"
	rest := strings.TrimPrefix(loc, prefix)
	if rest == loc {
		return "", fmt.Errorf("unexpected location %q", loc)
	}
	id := strings.SplitN(rest, "/", 2)[0]
	if id == "" {
		return "", fmt.Errorf("empty project id in %q", loc)
	}
	return id, nil
}

func createCycle(client *http.Client, base, projectID, template string) (string, error) {
	resp, err := client.PostForm(base+"/ui/projects/"+projectID+"/cycles",
		url.Values{"template_id": {template}})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("template %q: status %d: %s", template, resp.StatusCode, body)
	}
	loc := resp.Header.Get("Location")
	i := strings.Index(loc, "cycle=")
	if i < 0 {
		return "", fmt.Errorf("no cycle id in %q", loc)
	}
	id := loc[i+len("cycle="):]
	if j := strings.IndexAny(id, "&#"); j >= 0 {
		id = id[:j]
	}
	if id == "" {
		return "", fmt.Errorf("empty cycle id in %q", loc)
	}
	return id, nil
}

func startCycle(client *http.Client, base, cycleID string) error {
	resp, err := client.Post(base+"/ui/cycles/"+cycleID+"/start", "application/json",
		strings.NewReader("{}"))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}
	return nil
}

func waitCycle(client *http.Client, base, cycleID string, timeout, interval time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		state, err := cycleState(client, base, cycleID)
		if err != nil {
			return "", err
		}
		switch state {
		case "completed", "failed", "cancelled":
			return state, nil
		case "awaiting_review", "paused":
			// The brief template has no human gate; a pause is a stall, not a
			// pass. Fail fast rather than burning the timeout.
			return state, fmt.Errorf("cycle stalled in state %q", state)
		}
		if time.Now().After(deadline) {
			return state, fmt.Errorf("cycle timed out in state %q after %s", state, timeout)
		}
		time.Sleep(interval)
	}
}

func cycleState(client *http.Client, base, cycleID string) (string, error) {
	resp, err := client.Get(base + "/ui/cycles/" + cycleID + "/graph.json")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("graph status %d", resp.StatusCode)
	}
	var view struct {
		Cycle struct {
			State string `json:"state"`
		} `json:"cycle"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		return "", err
	}
	return view.Cycle.State, nil
}

func openReadOnly(path string) (*sql.DB, error) {
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

func cycleBlocks(db *sql.DB, cycleID string) (total, completed int, err error) {
	row := db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), 0)
		FROM tasks WHERE cycle_id = ?`, cycleID)
	if err := row.Scan(&total, &completed); err != nil {
		return 0, 0, fmt.Errorf("counting cycle blocks: %w", err)
	}
	return total, completed, nil
}

// latestAcceptedDocument returns the accepted document produced by the cycle's
// synthesize task. The artifact title is explicit so a verification report
// that is also a document is never mistaken for the brief.
func latestAcceptedDocument(db *sql.DB, stateDir, projectID, cycleID, title string) (string, string, error) {
	var id string
	var err error
	if title != "" {
		err = db.QueryRow(`SELECT a.id FROM artifacts a JOIN tasks t ON a.task_id = t.id
			WHERE t.cycle_id = ? AND t.title = ? AND a.kind = 'document' AND a.status = 'accepted'
			ORDER BY a.created_at DESC, a.id ASC LIMIT 1`, cycleID, title).Scan(&id)
	}
	if title == "" || err == sql.ErrNoRows {
		err = db.QueryRow(`SELECT id FROM artifacts
			WHERE project_id = ? AND kind = 'document' AND status = 'accepted'
			ORDER BY created_at DESC, id ASC LIMIT 1`, projectID).Scan(&id)
	}
	if err == sql.ErrNoRows {
		return "", "", fmt.Errorf("no accepted document artifact")
	}
	if err != nil {
		return "", "", fmt.Errorf("loading artifact: %w", err)
	}
	raw, err := os.ReadFile(filepath.Join(stateDir, "artifacts", id))
	if err != nil {
		return "", "", fmt.Errorf("reading artifact %s: %w", id, err)
	}
	return id, string(raw), nil
}

func renderCycleReport(r cycleResults, goals []sampleGoal) string {
	held := map[string]bool{}
	for _, g := range goals {
		held[g.Name] = g.HeldOut
	}
	passes := 0
	var b strings.Builder
	b.WriteString("# v4 cycle-arm report\n\n")
	fmt.Fprintf(&b, "- Model: `%s`\n- Started: %s\n- Tasks: %d\n\n", r.Model, r.StartedAt, len(r.Tasks))
	b.WriteString("| task | held out | pass | state | blocks | artifact |\n|---|---|---|---|---|---|\n")
	for _, t := range r.Tasks {
		if t.Pass {
			passes++
		}
		fmt.Fprintf(&b, "| %s | %v | %v | %s | %d/%d | `%s` |\n",
			t.ID, held[t.ID], t.Pass, t.State, t.BlocksCompleted, t.Blocks, t.ArtifactID)
	}
	if len(r.Tasks) > 0 {
		fmt.Fprintf(&b, "\nCycle pass rate: %d/%d (%.0f%%)\n", passes, len(r.Tasks),
			100*float64(passes)/float64(len(r.Tasks)))
	}
	b.WriteString("\n## Failures\n\n")
	for _, t := range r.Tasks {
		if len(t.Failures) == 0 {
			continue
		}
		fmt.Fprintf(&b, "- **%s**: %s\n", t.ID, strings.Join(t.Failures, "; "))
	}
	return b.String()
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func logf(format string, args ...any) {
	fmt.Printf(format+"\n", args...)
}
