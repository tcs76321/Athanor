// The M3-T7 runner. It is the only part of the probe that touches the
// network or the filesystem: it renders each arm's config, starts and
// stops the daemon with that config, submits every (goal × arm × run)
// job, and collects the captured outcomes. All interpretation is in
// analysis.go / report.go, so the runner stays a thin orchestrator.
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// runnerConfig holds the resolved `run` options.
type runnerConfig struct {
	binary     string
	addr       string
	outDir     string
	seedPolicy string
	goalLimit  int
	modelsCSV  string
	timeout    time.Duration
}

// runMatrix is the `run` subcommand: it executes the locked matrix,
// managing the daemon lifecycle per arm. A per-job failure is recorded
// as an error row and does not abort the run.
func runMatrix(args []string) {
	r := &runnerConfig{}
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	fs.StringVar(&r.binary, "bin", "bin/athanor", "path to the athanor binary (make build)")
	fs.StringVar(&r.addr, "addr", strings.TrimPrefix(daemonURL(), "http://"), "daemon listen address (loopback only)")
	fs.StringVar(&r.outDir, "out", filepath.Join("spikes", "m3-t7-probe", "results"), "results directory")
	fs.StringVar(&r.seedPolicy, "seed", "off", "judgment_seed policy: off|derived")
	fs.IntVar(&r.goalLimit, "goals", 0, "limit to the first N sample goals (0 = all)")
	fs.StringVar(&r.modelsCSV, "models", "", "comma-separated model labels to run (empty = all)")
	fs.DurationVar(&r.timeout, "timeout", 45*time.Minute, "per-job wall-clock timeout")
	_ = fs.Parse(args)

	if err := r.run(); err != nil {
		fmt.Fprintln(os.Stderr, "m3-t7 run:", err)
		os.Exit(1)
	}
}

func (r *runnerConfig) baseURL() string { return "http://" + r.addr }

// models resolves the -models filter against the matrix.
func (r *runnerConfig) models() []probeModel {
	if r.modelsCSV == "" {
		return probeModels
	}
	want := make(map[string]bool)
	for _, s := range strings.Split(r.modelsCSV, ",") {
		want[strings.TrimSpace(s)] = true
	}
	var out []probeModel
	for _, m := range probeModels {
		if want[m.Label] {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		fmt.Fprintf(os.Stderr, "warning: -models %q matched no matrix model; running all\n", r.modelsCSV)
		return probeModels
	}
	return out
}

// goals applies the -goals smoke-test limit.
func (r *runnerConfig) goals() []sampleGoal {
	if r.goalLimit <= 0 || r.goalLimit >= len(sampleGoals) {
		return sampleGoals
	}
	return sampleGoals[:r.goalLimit]
}

func (r *runnerConfig) run() error {
	if _, err := os.Stat(r.binary); err != nil {
		return fmt.Errorf("athanor binary %q not found (run `make build`): %w", r.binary, err)
	}
	if err := os.MkdirAll(r.outDir, 0o755); err != nil {
		return err
	}
	for _, m := range r.models() {
		for _, a := range arms {
			if err := r.runArm(m, a); err != nil {
				return err
			}
		}
	}
	return nil
}

// runArm completes one (model, arm) session and writes its artifacts.
func (r *runnerConfig) runArm(m probeModel, a arm) error {
	runDir := filepath.Join(r.outDir, m.Label, a.Name)
	stateDir := filepath.Join(runDir, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	cfgPath := filepath.Join(runDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(probeConfigYAML(m, a, r.seedPolicy)), 0o644); err != nil {
		return err
	}
	fmt.Printf("== %s / %s (candidates=%d runs=%d)\n", m.Label, a.Name, a.Candidates, a.Runs)

	proc, logPath, err := r.startDaemon(cfgPath, stateDir)
	if err != nil {
		return err
	}
	defer r.stopDaemon(proc)
	if err := waitHealthy(r.baseURL(), 90*time.Second); err != nil {
		return fmt.Errorf("daemon not healthy (see %s): %w", logPath, err)
	}

	metrics := make([]jobMetrics, 0, len(r.goals())*a.Runs)
	for _, g := range r.goals() {
		for run := 1; run <= a.Runs; run++ {
			mx := r.runOne(g, m, a, run, stateDir)
			metrics = append(metrics, mx)
			if mx.Error != "" {
				fmt.Printf("  %-18s run %d: ERROR %s\n", g.Name, run, mx.Error)
			} else {
				fmt.Printf("  %-18s run %d: state=%-9s winner=%-8s score=%.2f diversity=%.2f\n",
					g.Name, run, mx.State, mx.Winner, mx.Score, mx.Diversity)
			}
		}
	}

	if err := writeJSON(filepath.Join(runDir, "results.json"), metrics); err != nil {
		return err
	}
	if err := writeCSV(filepath.Join(runDir, "results.csv"), metrics); err != nil {
		return err
	}
	if err := writePackets(filepath.Join(runDir, "packets"), metrics); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(runDir, "report.md"), []byte(renderReport(metrics)), 0o644)
}

// startDaemon launches `athanor serve` with the arm's config and state
// dir, sending its output to a per-arm log file.
func (r *runnerConfig) startDaemon(cfgPath, stateDir string) (*exec.Cmd, string, error) {
	logPath := filepath.Join(filepath.Dir(cfgPath), "daemon.log")
	logf, err := os.Create(logPath)
	if err != nil {
		return nil, "", err
	}
	cmd := exec.Command(r.binary, "serve", "-config", cfgPath, "-state-dir", stateDir, "-addr", r.addr)
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Start(); err != nil {
		_ = logf.Close()
		return nil, "", err
	}
	return cmd, logPath, nil
}

// stopDaemon asks the daemon to shut down gracefully, then kills it if it
// does not exit within the grace period.
func (r *runnerConfig) stopDaemon(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

// runOne submits a single job and collects its metrics. Collection
// failures are non-fatal: the row still records what the HTTP surface
// returned.
func (r *runnerConfig) runOne(g sampleGoal, m probeModel, a arm, run int, stateDir string) jobMetrics {
	mx := jobMetrics{
		GoalName: g.Name, GoalNumber: g.Number, Archetype: g.Archetype,
		ModelLabel: m.Label, Model: m.Model, Arm: a.Name, Run: run,
	}
	name := fmt.Sprintf("%s-%s-%s-r%d-%d", g.Name, m.Label, a.Name, run, time.Now().UnixNano()%1_000_000)

	var pr struct {
		ID string `json:"id"`
	}
	if err := apiCall("POST", r.baseURL()+"/projects", map[string]any{
		"name": name, "archetype": g.Archetype, "goal": g.Goal, "acceptance_criteria": g.Criteria,
	}, &pr); err != nil {
		mx.Error = "create project: " + err.Error()
		return mx
	}

	var gr struct {
		JobID string `json:"job_id"`
	}
	if err := apiCall("POST", r.baseURL()+"/projects/"+pr.ID+"/goals", map[string]any{
		"goal": g.Goal, "acceptance_criteria": g.Criteria,
	}, &gr); err != nil {
		mx.Error = "submit goal: " + err.Error()
		return mx
	}
	mx.JobID = gr.JobID

	state, artifactID, err := waitJob(r.baseURL(), gr.JobID, r.timeout)
	if err != nil {
		mx.Error = "wait job: " + err.Error()
		return mx
	}
	mx.State = state
	mx.ArtifactID = artifactID
	if state != "completed" && state != "failed" {
		mx.Error = "non-terminal state " + state
	}

	db, err := openResultsDB(stateDir)
	if err != nil {
		mx.Error = "open results db: " + err.Error()
		return mx
	}
	defer func() { _ = db.Close() }()
	if o, ok, err := loadOutcome(db, gr.JobID); err == nil && ok {
		mx.Winner = winnerFromResult(o.Result)
		mx.Score = o.Score
		mx.Confidence = o.Confidence
		mx.TokenCost = o.TokenCost
		mx.WallMS = o.WallMS
		mx.Retries = o.Retries
		mx.ReflectionLoops = o.ReflectionLoops
	}
	_ = mx.fillCandidateMetrics(db, stateDir)
	if txt, err := readArtifact(stateDir, mx.ArtifactID); err == nil {
		mx.ArtifactText = txt
	}
	return mx
}

// waitJob polls a job until it reaches a terminal state or the timeout
// expires.
func waitJob(baseURL, jobID string, timeout time.Duration) (state, artifactID string, err error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var jr struct {
			State      string `json:"state"`
			ArtifactID string `json:"artifact_id"`
		}
		if err := apiCall("GET", baseURL+"/jobs/"+jobID, nil, &jr); err != nil {
			return "", "", err
		}
		switch jr.State {
		case "completed", "failed", "cancelled":
			return jr.State, jr.ArtifactID, nil
		}
		time.Sleep(5 * time.Second)
	}
	return "", "", fmt.Errorf("job %s timed out after %s", jobID, timeout)
}

// waitHealthy polls /healthz until the daemon answers 200 or the timeout
// expires.
func waitHealthy(baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timed out after %s", timeout)
}

// writeJSON writes v as indented JSON.
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// writeCSV writes the metrics as a flat CSV for spreadsheet inspection.
func writeCSV(path string, metrics []jobMetrics) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	w := csv.NewWriter(f)
	defer w.Flush()
	header := []string{"goal", "archetype", "model_label", "arm", "run", "state", "winner",
		"confidence", "score", "token_cost", "wall_ms", "retries", "diversity", "candidates", "job_id", "error"}
	if err := w.Write(header); err != nil {
		return err
	}
	for _, m := range metrics {
		if err := w.Write([]string{
			m.GoalName, m.Archetype, m.ModelLabel, m.Arm, strconv.Itoa(m.Run), m.State, m.Winner,
			strconv.FormatFloat(m.Confidence, 'f', 4, 64), strconv.FormatFloat(m.Score, 'f', 4, 64),
			strconv.Itoa(m.TokenCost), strconv.Itoa(m.WallMS), strconv.Itoa(m.Retries),
			strconv.FormatFloat(m.Diversity, 'f', 4, 64), strconv.Itoa(m.Candidates), m.JobID, m.Error,
		}); err != nil {
			return err
		}
	}
	return w.Error()
}

// writePackets writes one blind judge packet per artifact-bearing row and
// an index mapping the opaque packet IDs back to their experiment
// condition.
func writePackets(dir string, metrics []jobMetrics) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	type indexRow struct {
		PacketID string `json:"packet_id"`
		Goal     string `json:"goal"`
		Model    string `json:"model"`
		Arm      string `json:"arm"`
		Run      int    `json:"run"`
		JobID    string `json:"job_id"`
	}
	var index []indexRow
	n := 0
	for _, m := range metrics {
		if m.ArtifactText == "" {
			continue
		}
		g, ok := goalByName(m.GoalName)
		if !ok {
			continue
		}
		pid := opaquePacketID(n)
		n++
		out := renderJudgePacket(judgePacket{ID: pid, Goal: g, ArtifactText: m.ArtifactText})
		if err := os.WriteFile(filepath.Join(dir, pid+".md"), []byte(out), 0o644); err != nil {
			return err
		}
		index = append(index, indexRow{PacketID: pid, Goal: m.GoalName, Model: m.ModelLabel, Arm: m.Arm, Run: m.Run, JobID: m.JobID})
	}
	return writeJSON(filepath.Join(dir, "index.json"), index)
}

// goalByName looks up a sample goal.
func goalByName(name string) (sampleGoal, bool) {
	for _, g := range sampleGoals {
		if g.Name == name {
			return g, true
		}
	}
	return sampleGoal{}, false
}

// runReport is the `report` subcommand: it merges every results.json
// under the output tree and renders report.md.
func runReport(args []string) {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	outDir := fs.String("out", filepath.Join("spikes", "m3-t7-probe", "results"), "results directory")
	_ = fs.Parse(args)

	metrics, err := loadAllMetrics(*outDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "m3-t7 report:", err)
		os.Exit(1)
	}
	if len(metrics) == 0 {
		fmt.Fprintf(os.Stderr, "m3-t7 report: no results.json found under %s\n", *outDir)
		os.Exit(1)
	}
	report := renderReport(metrics)
	fmt.Print(report)
	if err := os.WriteFile(filepath.Join(*outDir, "report.md"), []byte(report), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "m3-t7 report:", err)
		os.Exit(1)
	}
}

// loadAllMetrics walks a results tree and merges every results.json.
func loadAllMetrics(dir string) ([]jobMetrics, error) {
	var all []jobMetrics
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "results.json" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var rows []jobMetrics
		if err := json.Unmarshal(b, &rows); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		all = append(all, rows...)
		return nil
	})
	return all, err
}

// printConfig is the `config` subcommand: it prints the generated config
// for one (model, arm) so an operator can inspect it or boot the daemon
// by hand for a pre-flight. It is also the pre-flight validity check —
// the generated YAML must be accepted by internal/config (see
// probeconfig_test.go).
func printConfig(args []string) {
	fs := flag.NewFlagSet("config", flag.ExitOnError)
	modelLabel := fs.String("model", probeModels[0].Label, "model label")
	armName := fs.String("arm", arms[0].Name, "arm name (dialectical|single)")
	seed := fs.String("seed", "off", "judgment_seed policy: off|derived")
	_ = fs.Parse(args)

	m, ok := modelByLabel(*modelLabel)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown model label %q\n", *modelLabel)
		os.Exit(2)
	}
	a, ok := armByName(*armName)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown arm %q\n", *armName)
		os.Exit(2)
	}
	fmt.Print(probeConfigYAML(m, a, *seed))
}
