// Conformance scenarios (ADR-0073 §2). The soak holds a live daemon; these
// drive subsystems that unit/integration tests exercise in isolation — the kill
// switch and the UI — end-to-end, and record a pass/fail matrix. The set grows
// incrementally: a scenario is added only when it proves something no existing
// gate already proves. Every scenario is offline (loopback only).
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// scenarioResult is one conformance scenario's outcome. Skipped is set when the
// run produced no input to exercise (e.g. no accepted repo-backed artifact), so
// "not exercised" is distinct from "failed".
type scenarioResult struct {
	Name    string `json:"name"`
	Pass    bool   `json:"pass"`
	Skipped bool   `json:"skipped,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// scenarioDeps is what a scenario needs beyond the daemon URL.
type scenarioDeps struct {
	baseURL  string
	stateDir string
	outDir   string
	binary   string
}

// runScenarios executes the conformance set against a live daemon. A failing
// scenario never aborts the run; it is recorded.
func runScenarios(d scenarioDeps) []scenarioResult {
	return []scenarioResult{
		scenarioFreeze(d.baseURL),
		scenarioUIPages(d.baseURL),
		scenarioUISSE(d.baseURL),
		scenarioPush(d),
	}
}

// scenarioFreeze drives the §22 kill switch end-to-end: freeze, a reasonless
// unfreeze must be rejected, and a reasoned unfreeze must clear it. It always
// leaves the daemon unfrozen so a later failure cannot wedge the run.
func scenarioFreeze(baseURL string) scenarioResult {
	const name = "freeze-unfreeze"
	defer func() {
		_ = apiCall("DELETE", baseURL+"/freeze", map[string]string{"reason": "soak conformance cleanup"}, nil)
	}()

	if err := apiCall("POST", baseURL+"/freeze", nil, nil); err != nil {
		return scenarioResult{Name: name, Detail: "freeze: " + err.Error()}
	}
	var st struct {
		Frozen bool `json:"frozen"`
	}
	if err := apiCall("GET", baseURL+"/freeze", nil, &st); err != nil {
		return scenarioResult{Name: name, Detail: "get freeze: " + err.Error()}
	}
	if !st.Frozen {
		return scenarioResult{Name: name, Detail: "not frozen after POST /freeze"}
	}
	// §22.2: unfreezing without a reason must be rejected.
	if err := apiCall("DELETE", baseURL+"/freeze", map[string]string{}, nil); err == nil {
		return scenarioResult{Name: name, Detail: "unfreeze without a reason was accepted"}
	}
	if err := apiCall("DELETE", baseURL+"/freeze", map[string]string{"reason": "soak conformance"}, nil); err != nil {
		return scenarioResult{Name: name, Detail: "unfreeze with reason: " + err.Error()}
	}
	if err := apiCall("GET", baseURL+"/freeze", nil, &st); err != nil {
		return scenarioResult{Name: name, Detail: "get freeze after unfreeze: " + err.Error()}
	}
	if st.Frozen {
		return scenarioResult{Name: name, Detail: "still frozen after a reasoned unfreeze"}
	}
	return scenarioResult{Name: name, Pass: true, Detail: "freeze → frozen; reasonless unfreeze rejected; reasoned unfreeze → running"}
}

// scenarioUIPages fetches the read-only UI pages and asserts each renders HTML.
func scenarioUIPages(baseURL string) scenarioResult {
	const name = "ui-pages"
	for _, path := range []string{"/ui", "/ui/statistics", "/ui/projects", "/ui/corrections"} {
		resp, err := apiClient.Get(baseURL + path)
		if err != nil {
			return scenarioResult{Name: name, Detail: path + ": " + err.Error()}
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return scenarioResult{Name: name, Detail: fmt.Sprintf("%s: HTTP %d", path, resp.StatusCode)}
		}
		if !bytes.Contains(body, []byte("<title>")) {
			return scenarioResult{Name: name, Detail: path + ": no <title> in body"}
		}
	}
	return scenarioResult{Name: name, Pass: true, Detail: "dashboard, statistics, projects, corrections render"}
}

// scenarioUISSE opens the dashboard SSE stream and asserts the streaming
// contract (200 + text/event-stream). Reading an event would need live activity;
// the open proves the subscribe path and headers.
func scenarioUISSE(baseURL string) scenarioResult {
	const name = "ui-sse"
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(baseURL + "/ui/events")
	if err != nil {
		return scenarioResult{Name: name, Detail: "open /ui/events: " + err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return scenarioResult{Name: name, Detail: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		return scenarioResult{Name: name, Detail: "content-type " + ct}
	}
	return scenarioResult{Name: name, Pass: true, Detail: "SSE stream opened (200, text/event-stream)"}
}

// scenarioRestore drives the §23.4 backup → restore cycle end-to-end. It runs
// after the daemon stops (restore replaces a database). It forces a snapshot of
// the run's state dir, restores it into a scratch dir, and reads the restored
// database back — the whole cycle on a real state dir, offline.
func scenarioRestore(d scenarioDeps) scenarioResult {
	const name = "backup-restore"
	if d.binary == "" || d.stateDir == "" {
		return scenarioResult{Name: name, Skipped: true, Detail: "no binary or state dir"}
	}
	if err := runBin(d.binary, "backup", "-state-dir", d.stateDir, "-keep", "5"); err != nil {
		return scenarioResult{Name: name, Detail: "backup: " + err.Error()}
	}
	snap, err := newestSnapshot(filepath.Join(d.stateDir, "backups"))
	if err != nil {
		return scenarioResult{Name: name, Detail: "find snapshot: " + err.Error()}
	}
	scratch := filepath.Join(d.outDir, "scenarios", "restore-state")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return scenarioResult{Name: name, Detail: "mkdir scratch: " + err.Error()}
	}
	if err := runBin(d.binary, "restore", "-state-dir", scratch, "-from", snap, "-force"); err != nil {
		return scenarioResult{Name: name, Detail: "restore: " + err.Error()}
	}
	db, err := openResultsDB(scratch)
	if err != nil {
		return scenarioResult{Name: name, Detail: "open restored db: " + err.Error()}
	}
	defer func() { _ = db.Close() }()
	var projects int
	if err := db.QueryRow(`SELECT count(*) FROM projects`).Scan(&projects); err != nil {
		return scenarioResult{Name: name, Detail: "read restored db: " + err.Error()}
	}
	return scenarioResult{Name: name, Pass: true,
		Detail: fmt.Sprintf("snapshot %s restored and read back (%d project(s))", filepath.Base(snap), projects)}
}

// newestSnapshot returns the lexically greatest athanor snapshot in dir. Names
// embed a UTC timestamp, so a lexical ordering is chronological.
func newestSnapshot(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	newest := ""
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "athanor-v") || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		if e.Name() > newest {
			newest = e.Name()
		}
	}
	if newest == "" {
		return "", fmt.Errorf("no snapshot in %s", dir)
	}
	return filepath.Join(dir, newest), nil
}

// runBin runs the athanor binary with args and returns a wrapped error.
func runBin(bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", filepath.Base(bin), strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// renderConformance renders the conformance matrix section for the report.
func renderConformance(results []scenarioResult) string {
	if len(results) == 0 {
		return ""
	}
	passed, skipped := 0, 0
	var b strings.Builder
	b.WriteString("## Conformance (ADR-0073 §2)\n\n")
	b.WriteString("| scenario | result | detail |\n|---|---|---|\n")
	for _, r := range results {
		mark := "FAIL"
		switch {
		case r.Skipped:
			mark = "SKIP"
			skipped++
		case r.Pass:
			mark = "PASS"
			passed++
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", r.Name, mark, r.Detail)
	}
	fmt.Fprintf(&b, "\n%d passed, %d failed, %d skipped.\n", passed, len(results)-passed-skipped, skipped)
	return b.String()
}

// scenarioPush drives the HITL-gated push (ADR-0036) end-to-end against a
// LOCAL bare repository — a real git push with no network. It reuses a project
// the workload already produced (a repository_path plus an accepted artifact on
// the agent branch), requests the push, approves it, and asserts the branch
// arrived.
func scenarioPush(d scenarioDeps) scenarioResult {
	const name = "push-local-remote"
	if d.stateDir == "" {
		return scenarioResult{Name: name, Skipped: true, Detail: "no state dir"}
	}
	db, err := openResultsDB(d.stateDir)
	if err != nil {
		return scenarioResult{Name: name, Skipped: true, Detail: "open state db: " + err.Error()}
	}
	defer func() { _ = db.Close() }()

	var projID, repoPath string
	err = db.QueryRow(
		`SELECT p.id, p.repository_path FROM projects p
		   JOIN artifacts a ON a.project_id = p.id
		  WHERE p.repository_path IS NOT NULL AND p.repository_path <> ''
		    AND a.git_commit IS NOT NULL AND a.git_commit <> ''
		  ORDER BY a.created_at DESC LIMIT 1`).Scan(&projID, &repoPath)
	if err != nil {
		return scenarioResult{Name: name, Skipped: true, Detail: "not exercised: no accepted repo-backed artifact with a git commit"}
	}
	branch := "athanor/" + projID

	remote := filepath.Join(d.outDir, "scenarios", "push-remote.git")
	if err := os.MkdirAll(filepath.Dir(remote), 0o755); err != nil {
		return scenarioResult{Name: name, Detail: "mkdir remote: " + err.Error()}
	}
	_ = os.RemoveAll(remote)
	if err := gitInitBare(remote); err != nil {
		return scenarioResult{Name: name, Detail: "git init --bare: " + err.Error()}
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := apiCall("POST", d.baseURL+"/projects/"+projID+"/push", map[string]string{"remote": remote}, &req); err != nil {
		return scenarioResult{Name: name, Detail: "request push: " + err.Error()}
	}
	if req.ID == "" {
		return scenarioResult{Name: name, Detail: "push request returned no id"}
	}
	if err := apiCall("POST", d.baseURL+"/hitl/"+req.ID+"/decision",
		map[string]string{"action": "approve", "note": "soak conformance"}, nil); err != nil {
		return scenarioResult{Name: name, Detail: "approve push: " + err.Error()}
	}
	if !gitBranchExists(remote, branch) {
		return scenarioResult{Name: name, Detail: fmt.Sprintf("branch %s absent from the remote after approval", branch)}
	}
	return scenarioResult{Name: name, Pass: true, Detail: "approved push delivered " + branch + " to a local bare remote"}
}

// gitInitBare creates a bare repository at dir.
func gitInitBare(dir string) error { return runGit("init", "--bare", "-q", dir) }

// gitBranchExists reports whether a bare repository has the named branch.
func gitBranchExists(gitDir, branch string) bool {
	cmd := exec.Command("git", "--git-dir", gitDir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd.Run() == nil
}

// runGit runs one git subcommand with prompts disabled (ADR-0071 §3).
func runGit(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// loadConformance reads the scenarios.json written by a soak (empty when the
// file is absent, so a non-soak results tree still reports).
func loadConformance(path string) []scenarioResult {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []scenarioResult
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}
