// v4 cycle arm (ADR-0135): drive the product's actual authoring path — project
// → template draft → start — and score the produced brief against the corpus's
// deterministic checks. The probe runs inside the Core Pod, so it talks to the
// daemon over loopback and reads the artifact files from the state directory.
//
// This is deliberately a thin driver: creation, polling, collection, and
// scoring. It does not execute a block; the daemon's scheduler does.
package main

import (
	"bytes"
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

	"github.com/tcs76321/athanor/internal/workflow"
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
	EngineAccepted  bool          `json:"engine_accepted"`
	Complete        bool          `json:"complete"`
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
	// The task's declared old ids are the only precise old-id list. Inject them
	// into the draft synthesize block so the engine can reject an old id placed
	// under New. Without this the engine has only the generic overlap rule,
	// which cannot tell a new id from an old one.
	if section, ids := oldIDsFromChecks(checks); len(ids) > 0 {
		graph, gerr := fetchCycleGraph(client, base, cycleID)
		if gerr != nil {
			return res, fmt.Errorf("read draft graph: %w", gerr)
		}
		if !applyTaskChecks(&graph, section, ids) {
			return res, fmt.Errorf("cycle template %q has no synthesize block for the old-id rule", template)
		}
		if serr := saveCycleGraph(client, base, cycleID, graph); serr != nil {
			return res, fmt.Errorf("save draft graph: %w", serr)
		}
	}
	res.CycleID = cycleID
	goalID, err := startCycle(client, base, cycleID)
	if err != nil {
		return res, fmt.Errorf("start cycle: %w", err)
	}
	db, err := openReadOnly(filepath.Join(c.stateDir, "athanor.db"))
	if err != nil {
		return res, fmt.Errorf("open state: %w", err)
	}
	defer func() { _ = db.Close() }()

	state, err := waitCycle(client, base, cycleID, goalID, db, c.timeout, c.interval)
	res.State = state
	if err != nil {
		res.Failures = append(res.Failures, err.Error())
	}

	blocks, done, err := cycleBlocks(db, goalID)
	if err != nil {
		return res, err
	}
	res.Blocks, res.BlocksCompleted = blocks, done
	res.Complete = state == "completed" && blocks > 0 && done == blocks
	if state != "completed" {
		res.Failures = append(res.Failures, "cycle state is "+state)
	}
	if blocks == 0 || done != blocks {
		res.Failures = append(res.Failures, fmt.Sprintf("blocks completed %d/%d", done, blocks))
	}
	artifactID, content, accepted, err := bestArtifact(db, c.stateDir, projectID, goalID, artifactTitle)
	if err != nil {
		res.Failures = append(res.Failures, err.Error())
		return res, nil
	}
	res.ArtifactID = artifactID
	res.EngineAccepted = accepted
	pass, results := runChecks(checks, content)
	res.Checks = results
	if !pass {
		for _, r := range results {
			if !r.Pass && !r.Advisory {
				res.Failures = append(res.Failures, r.Kind+": "+r.Detail)
			}
		}
	}
	// The label is the external check outcome. Cycle completion is reported
	// separately (mechanism/utility coverage), never folded into the label.
	res.Pass = pass
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

// oldIDsFromChecks returns the task's precise old-id list from a
// `new_items_section` check. It returns the section name (default "New") and
// the ids; an empty list means the task does not declare this rule.
func oldIDsFromChecks(checks map[string]any) (string, []string) {
	raw, ok := checks["new_items_section"]
	if !ok {
		return "", nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return "", nil
	}
	section, _ := m["section"].(string)
	if strings.TrimSpace(section) == "" {
		section = "New"
	}
	var ids []string
	switch v := m["old_ids"].(type) {
	case []string:
		ids = append(ids, v...)
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				ids = append(ids, s)
			}
		}
	}
	return section, ids
}

// applyTaskChecks injects the precise old-id rule into the first synthesize
// block of a draft cycle graph. It returns false when the graph has no
// synthesize block, which is a template/probe mismatch.
func applyTaskChecks(graph *workflow.Graph, section string, ids []string) bool {
	if len(ids) == 0 {
		return false
	}
	for i := range graph.Blocks {
		if graph.Blocks[i].Kind != workflow.KindSynthesize {
			continue
		}
		cfg := graph.Blocks[i].Config
		if cfg == nil {
			cfg = map[string]any{}
		}
		checks, _ := cfg["checks"].(map[string]any)
		if checks == nil {
			checks = map[string]any{}
		}
		checks["forbidden_ids_in_sections"] = []map[string]any{
			{"section": section, "ids": ids},
		}
		cfg["checks"] = checks
		graph.Blocks[i].Config = cfg
		return true
	}
	return false
}

func fetchCycleGraph(client *http.Client, base, cycleID string) (workflow.Graph, error) {
	var graph workflow.Graph
	resp, err := client.Get(base + "/ui/cycles/" + cycleID + "/graph.json")
	if err != nil {
		return graph, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return graph, fmt.Errorf("graph status %d: %s", resp.StatusCode, body)
	}
	var view struct {
		Graph workflow.Graph `json:"graph"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		return graph, err
	}
	return view.Graph, nil
}

func saveCycleGraph(client *http.Client, base, cycleID string, graph workflow.Graph) error {
	data, err := json.Marshal(graph)
	if err != nil {
		return err
	}
	resp, err := client.Post(base+"/ui/cycles/"+cycleID+"/graph", "application/json",
		bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("save status %d: %s", resp.StatusCode, body)
	}
	return nil
}

func startCycle(client *http.Client, base, cycleID string) (string, error) {
	resp, err := client.Post(base+"/ui/cycles/"+cycleID+"/start", "application/json",
		strings.NewReader("{}"))
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}
	var out struct {
		Goal string `json:"goal"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decoding start response: %w", err)
	}
	return out.Goal, nil
}

func waitCycle(client *http.Client, base, cycleID, goalID string, db *sql.DB, timeout, interval time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	sawActive := false
	idle := 0
	for {
		state, err := cycleState(client, base, cycleID)
		if err != nil {
			return "", err
		}
		if cycleWaitTerminal(state) {
			return state, nil
		}
		if state == "paused" {
			// The brief/news templates have no human gate; a pause is a stall,
			// not a pass. Fail fast rather than burning the timeout.
			return state, fmt.Errorf("cycle paused in state %q", state)
		}
		active, aerr := activeTasks(db, goalID)
		if aerr == nil {
			if active > 0 {
				sawActive, idle = true, 0
			} else if sawActive {
				idle++
				if idle >= 3 {
					return state, fmt.Errorf("cycle stalled in state %q: no active tasks", state)
				}
			}
		}
		if time.Now().After(deadline) {
			return state, fmt.Errorf("cycle timed out in state %q after %s", state, timeout)
		}
		time.Sleep(interval)
	}
}

// cycleWaitTerminal reports whether waitCycle should stop polling. completed,
// failed, and cancelled are terminal outcomes; awaiting_review is terminal for
// the probe because a blocked-only cycle cannot make progress without an
// operator, but it is not a pass (runOneCycle records the state separately).
func cycleWaitTerminal(state string) bool {
	switch state {
	case "completed", "failed", "cancelled", "awaiting_review":
		return true
	}
	return false
}

// activeTasks counts tasks that can still make progress. A cycle with none and
// a non-terminal state has stalled (the scheduler blocked or exhausted retries).
func activeTasks(db *sql.DB, goalID string) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM tasks
		WHERE goal_id = ? AND status IN ('pending','ready','running','paused')`, goalID).Scan(&n)
	return n, err
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
	// mode=rw (not ro) so SQLite can attach the WAL/shm and see committed
	// rows while the daemon is running. The probe only issues SELECTs.
	db, err := sql.Open("sqlite3", "file:"+path+"?mode=rw&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func cycleBlocks(db *sql.DB, goalID string) (total, completed int, err error) {
	row := db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), 0)
		FROM tasks WHERE goal_id = ?`, goalID)
	if err := row.Scan(&total, &completed); err != nil {
		return 0, 0, fmt.Errorf("counting goal blocks: %w", err)
	}
	return total, completed, nil
}

// bestArtifact returns the artifact the external checks should score: the
// accepted document for the named synthesize task when it exists, otherwise
// the best candidate the engine produced. Engine acceptance is reported
// separately (ADR-0092); a rejected best candidate can still pass the frozen
// checks, and a stalled cycle is not silently treated as a pass.
func bestArtifact(db *sql.DB, stateDir, projectID, goalID, title string) (string, string, bool, error) {
	var id, status string
	var err error
	if title != "" {
		err = db.QueryRow(`SELECT a.id, a.status FROM artifacts a JOIN tasks t ON a.task_id = t.id
			WHERE t.goal_id = ? AND t.title = ? AND a.kind IN ('document','proposal')
			ORDER BY CASE a.status WHEN 'accepted' THEN 0 WHEN 'candidate' THEN 1 ELSE 2 END,
			         a.created_at DESC, a.id ASC LIMIT 1`, goalID, title).Scan(&id, &status)
	}
	if title == "" || err == sql.ErrNoRows {
		err = db.QueryRow(`SELECT a.id, a.status FROM artifacts a JOIN tasks t ON a.task_id = t.id
			WHERE t.goal_id = ? AND a.kind IN ('document','proposal')
			ORDER BY CASE a.status WHEN 'accepted' THEN 0 WHEN 'candidate' THEN 1 ELSE 2 END,
			         a.created_at DESC, a.id ASC LIMIT 1`, goalID).Scan(&id, &status)
	}
	if err == sql.ErrNoRows {
		err = db.QueryRow(`SELECT id, status FROM artifacts
			WHERE project_id = ? AND kind IN ('document','proposal')
			ORDER BY CASE status WHEN 'accepted' THEN 0 WHEN 'candidate' THEN 1 ELSE 2 END,
			         created_at DESC, id ASC LIMIT 1`, projectID).Scan(&id, &status)
	}
	if err == sql.ErrNoRows {
		return "", "", false, fmt.Errorf("no artifact produced")
	}
	if err != nil {
		return "", "", false, fmt.Errorf("loading artifact: %w", err)
	}
	raw, err := os.ReadFile(filepath.Join(stateDir, "artifacts", id))
	if err != nil {
		return "", "", false, fmt.Errorf("reading artifact %s: %w", id, err)
	}
	return id, string(raw), status == "accepted", nil
}

func renderCycleReport(r cycleResults, goals []sampleGoal) string {
	held := map[string]bool{}
	for _, g := range goals {
		held[g.Name] = g.HeldOut
	}
	passes := 0
	var b strings.Builder
	b.WriteString("# v5 cycle-arm report\n\n")
	fmt.Fprintf(&b, "- Model: `%s`\n- Started: %s\n- Tasks: %d\n\n", r.Model, r.StartedAt, len(r.Tasks))
	b.WriteString("| task | held out | pass | complete | engine | state | blocks | artifact |\n|---|---|---|---|---|---|---|---|\n")
	for _, t := range r.Tasks {
		if t.Pass {
			passes++
		}
		fmt.Fprintf(&b, "| %s | %v | %v | %v | %v | %s | %d/%d | `%s` |\n",
			t.ID, held[t.ID], t.Pass, t.Complete, t.EngineAccepted, t.State, t.BlocksCompleted, t.Blocks, t.ArtifactID)
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
