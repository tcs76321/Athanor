// The M3-T7 runner. It is the only part of the probe that touches the
// network or the filesystem: it renders each arm's config, starts and
// stops the daemon with that config, submits every (goal × arm × run)
// job, and collects the captured outcomes. All interpretation is in
// analysis.go / report.go, so the runner stays a thin orchestrator.
package main

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
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
	onlyCSV    string
	modelsCSV  string
	timeout    time.Duration
	noReflect  bool
	// ollamaURL is queried (/api/ps) for the single-residency check.
	ollamaURL string
	// F4 follow-up run knobs.
	judgeMode  string
	judgeModel string
	altModel   string
	// gates enables the F5 code acceptance gates in the generated config.
	gates bool
	// dialecticalRuns overrides the dialectical arm's repetition (default 3).
	// A cross-model panel runs 1 to bound wall time; the core generator keeps
	// 3 for T-c stability.
	dialecticalRuns int
	// corpusPath, when set, loads eval/bench/tasks.yaml (M8-T16, ADR-0061)
	// and runs it instead of the locked sampleGoals. corpus holds the loaded
	// goals; empty means "use the locked M3-T7 set".
	corpusPath string
	corpus     []sampleGoal
	// ablation selects the four-arm component ablation (M8-T22) instead of
	// the default dialectical-vs-single pair; selfRefine enables
	// execution.self_refine in the ablation arms.
	ablation   bool
	selfRefine bool

	// Run guards (M3-T7 soak hardening).
	maxWall        time.Duration
	minFreeGB      int
	maxConsecFails int
	soakInterval   time.Duration
	start          time.Time
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
	fs.StringVar(&r.onlyCSV, "only", "", "comma-separated goal names to run (empty = all)")
	fs.StringVar(&r.modelsCSV, "models", "", "comma-separated model labels to run (empty = all)")
	fs.DurationVar(&r.timeout, "timeout", 45*time.Minute, "per-job wall-clock timeout")
	fs.DurationVar(&r.maxWall, "max-wall", 12*time.Hour, "abort the whole run after this wall time (0 = no limit)")
	fs.IntVar(&r.minFreeGB, "min-free-gb", 5, "abort if free disk drops below this many GB (0 = no check)")
	fs.IntVar(&r.maxConsecFails, "max-consecutive-errors", 5, "abort after this many consecutive job errors (0 = no limit)")
	fs.DurationVar(&r.soakInterval, "soak-interval", 30*time.Second, "RSS/DB/disk soak sampling interval")
	fs.BoolVar(&r.noReflect, "no-reflect", false, "disable reflection loops (pure single-shot baseline)")
	fs.StringVar(&r.ollamaURL, "ollama", "http://localhost:11434", "Ollama base URL for the /api/ps residency check")
	fs.StringVar(&r.judgeMode, "judge-mode", "", `execution.policy.judge_mode: "" (default) | llm | verifier`)
	fs.StringVar(&r.judgeModel, "judge-model", "", "personas.security.model override (cross-family judge)")
	fs.StringVar(&r.altModel, "alt-model", "", "personas.alternative.model override (multi-model generation)")
	fs.BoolVar(&r.gates, "gates", false, "enable the F5 code acceptance gates (require_tests_for_code, require_documentation_for_code); default off to isolate the loop")
	fs.IntVar(&r.dialecticalRuns, "dialectical-runs", 3, "repetitions of the dialectical arm (panel runs 1; the core generator keeps 3 for T-c)")
	fs.StringVar(&r.corpusPath, "corpus", "", "path to eval/bench/tasks.yaml; when set, run the harder corpus instead of the locked sampleGoals (M8-T16)")
	fs.BoolVar(&r.ablation, "ablation", false, "run the four-arm component ablation (single | bestof3 | reflect | full) instead of dialectical-vs-single (M8-T22)")
	fs.BoolVar(&r.selfRefine, "self-refine", false, "enable execution.self_refine in the ablation arms")
	_ = fs.Parse(args)

	if r.corpusPath != "" {
		goals, err := loadCorpus(r.corpusPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "m3-t7 run:", err)
			os.Exit(1)
		}
		r.corpus = goals
		fmt.Printf("corpus: %d tasks from %s\n", len(goals), r.corpusPath)
	}

	if err := r.run(); err != nil {
		fmt.Fprintln(os.Stderr, "m3-t7 run:", err)
		os.Exit(1)
	}
}

func (r *runnerConfig) baseURL() string { return "http://" + r.addr }

// errAbort signals that a run guard tripped; run() stops gracefully
// rather than returning a hard error.
var errAbort = errors.New("m3-t7: run guard tripped")

// guard checks the wall-clock, free-disk, and consecutive-error budgets
// before each job. A trip freezes the daemon and stops the run.
func (r *runnerConfig) guard(consecutiveErrors int, stateDir string) error {
	if r.maxWall > 0 && time.Since(r.start) > r.maxWall {
		return fmt.Errorf("%w: wall time exceeded %s", errAbort, r.maxWall)
	}
	if r.minFreeGB > 0 {
		if free, err := freeBytes(stateDir); err == nil && free < uint64(r.minFreeGB)<<30 {
			return fmt.Errorf("%w: free disk below %d GB", errAbort, r.minFreeGB)
		}
	}
	if r.maxConsecFails > 0 && consecutiveErrors >= r.maxConsecFails {
		return fmt.Errorf("%w: %d consecutive job errors", errAbort, consecutiveErrors)
	}
	return nil
}

// freezeDaemon asks the running daemon to stop accepting new work. Best
// effort, used on abort so the daemon halts cleanly.
func freezeDaemon(baseURL string) {
	_ = apiCall("POST", baseURL+"/freeze", nil, nil)
}

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

// armSet returns the arms for this run, honoring -dialectical-runs. A panel
// generator uses 1 (time-bounded); the core generator keeps the default 3 so
// T-c stability reads three winners.
func (r *runnerConfig) armSet() []arm {
	runs := r.dialecticalRuns
	if runs < 1 {
		runs = 1
	}
	if r.ablation {
		// M8-T22 component ablation: isolate divergence (bestof3), reflection
		// (reflect), and the full loop from the single-shot baseline.
		return []arm{
			{Name: "single", Candidates: 1, Runs: 1, Reflection: 0},
			{Name: "bestof3", Candidates: 3, Runs: runs, Reflection: 0},
			{Name: "reflect", Candidates: 1, Runs: runs, Reflection: -1},
			{Name: "full", Candidates: 3, Runs: runs, Reflection: -1, SelfRefine: r.selfRefine},
		}
	}
	return []arm{
		{Name: "dialectical", Candidates: 3, Runs: runs, Reflection: -1},
		{Name: "single", Candidates: 1, Runs: 1, Reflection: -1},
	}
}

// goals resolves the -only / -goals selection against the active goal set:
// the loaded bench corpus when -corpus was given (M8-T16), otherwise the
// locked M3-T7 sampleGoals.
func (r *runnerConfig) goals() []sampleGoal {
	base := sampleGoals
	if len(r.corpus) > 0 {
		base = r.corpus
	}
	if r.onlyCSV != "" {
		want := make(map[string]bool)
		for _, n := range strings.Split(r.onlyCSV, ",") {
			if n = strings.TrimSpace(n); n != "" {
				want[n] = true
			}
		}
		var out []sampleGoal
		for _, g := range base {
			if want[g.Name] {
				out = append(out, g)
			}
		}
		if len(out) == 0 {
			fmt.Fprintf(os.Stderr, "warning: -only %q matched no goal; running all\n", r.onlyCSV)
			return base
		}
		return out
	}
	if r.goalLimit <= 0 || r.goalLimit >= len(base) {
		return base
	}
	return base[:r.goalLimit]
}

func (r *runnerConfig) run() error {
	if _, err := os.Stat(r.binary); err != nil {
		return fmt.Errorf("athanor binary %q not found (run `make build`): %w", r.binary, err)
	}
	// Bind mounts (the per-job token dir) require an absolute host path:
	// Podman resolves a relative mount source inside the VM and fails with
	// "statfs ...: no such file or directory". Make the output dir absolute
	// so the daemon's -state-dir (and every token dir under it) is too.
	if abs, err := filepath.Abs(r.outDir); err == nil {
		r.outDir = abs
	}
	if err := os.MkdirAll(r.outDir, 0o755); err != nil {
		return err
	}
	if free, err := freeBytes(r.outDir); err == nil {
		fmt.Printf("results dir %s (%d GB free)\n", r.outDir, free>>30)
	}
	r.start = time.Now()
	for _, m := range r.models() {
		for _, a := range r.armSet() {
			err := r.runArm(m, a)
			if errors.Is(err, errAbort) {
				fmt.Fprintf(os.Stderr, "m3-t7: aborting run: %v\n", err)
				return nil
			}
			if err != nil {
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
	// A reused state dir makes the daemon recover the previous run's
	// non-terminal jobs, contaminating the measurement. Refuse rather than
	// silently mix runs.
	if _, err := os.Stat(filepath.Join(stateDir, "athanor.db")); err == nil {
		return fmt.Errorf("state dir %s already holds a database — use a fresh -out for a clean run", stateDir)
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	cfgPath := filepath.Join(runDir, "config.yaml")
	pol := probePolicy{JudgeMode: r.judgeMode, JudgeModel: r.judgeModel, AltModel: r.altModel, OllamaURL: r.ollamaURL, CodeGates: r.gates}
	if err := os.WriteFile(cfgPath, []byte(probeConfigYAML(m, a, r.seedPolicy, r.addr, r.noReflect, pol)), 0o644); err != nil {
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
	sampler := startSoak(runDir, r.soakInterval, proc.Process.Pid, stateDir, r.baseURL()+"/healthz")
	defer sampler.stopAndWait()

	consecutiveErrors := 0
	maxLoaded := 0
	metrics := make([]jobMetrics, 0, len(r.goals())*a.Runs)
	for _, g := range r.goals() {
		for run := 1; run <= a.Runs; run++ {
			if gerr := r.guard(consecutiveErrors, stateDir); gerr != nil {
				freezeDaemon(r.baseURL())
				return gerr
			}
			mx := r.runOne(g, m, a, run, stateDir)
			metrics = append(metrics, mx)
			// F4 follow-up D2: verify single-model residency (ARCHITECTURE
			// §12.5). A co-resident generator + judge invalidates the run.
			if names, lerr := loadedModels(r.ollamaURL); lerr == nil {
				if len(names) > maxLoaded {
					maxLoaded = len(names)
				}
				if len(names) > 1 {
					fmt.Printf("  WARN: %d models resident: %v\n", len(names), names)
				}
			}
			if mx.Error != "" {
				consecutiveErrors++
				fmt.Printf("  %-18s run %d: ERROR %s\n", g.Name, run, mx.Error)
			} else {
				consecutiveErrors = 0
				fmt.Printf("  %-18s run %d: state=%-9s winner=%-8s score=%.2f diversity=%.2f\n",
					g.Name, run, mx.State, mx.Winner, mx.Score, mx.Diversity)
			}
		}
	}
	sampler.stopAndWait()
	if s := soakSummary(filepath.Join(runDir, "soak.csv")); s != "" {
		fmt.Printf("  soak: %s\n", s)
	}
	if names := settleOrphanPods(15 * time.Second); len(names) > 0 {
		fmt.Printf("  orphan athanor-job pods after arm: %d %v\n", len(names), names)
	}

	// Backstop for the code-goal outcome lag: after the whole arm has run,
	// every job is long finished, so anything the live collector missed can
	// be read straight from the state DB before the results are written.
	if n, err := reconcileMetrics(stateDir, metrics); err == nil && n > 0 {
		fmt.Printf("  reconciled %d outcome(s) the live collector missed\n", n)
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
	if err := os.WriteFile(filepath.Join(runDir, "report.md"), []byte(renderReport(metrics)), 0o644); err != nil {
		return err
	}
	if maxLoaded > 1 {
		return fmt.Errorf("model residency violated: up to %d models resident at once; set OLLAMA_MAX_LOADED_MODELS=1 (ARCHITECTURE §12.5)", maxLoaded)
	}
	return nil
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
		GoalName: g.Name, GoalNumber: g.Number, Archetype: g.Archetype, Tier: tierOf(g.Source),
		ModelLabel: m.Label, Model: m.Model, Family: m.Family, Arm: a.Name, Run: run,
	}
	name := fmt.Sprintf("%s-%s-%s-r%d-%d", g.Name, m.Label, a.Name, run, time.Now().UnixNano()%1_000_000)

	var pr struct {
		ID string `json:"id"`
	}
	body := map[string]any{
		"name": name, "archetype": g.Archetype, "goal": g.Goal, "acceptance_criteria": g.Criteria,
	}
	// M8-T19 / M9: a repo-backed task indexes a throwaway copy of its source so
	// the MCE working set (Dormant Index + seeded active chunk, F5/ADR-0059)
	// carries it into the prompt, and git-as-undo commits into the copy — never
	// the checked-in fixture. A non-code fixture (the "provided module/source"
	// a document/text/data goal refers to) is indexed the same way; without
	// this the model never sees the source and the task can only fail.
	repoSource := repoSourceFor(g)
	if repoSource != "" {
		repoCopy := filepath.Join(r.outDir, "repos", name)
		if cerr := copyDir(repoSource, repoCopy); cerr != nil {
			mx.Error = "copy repo: " + cerr.Error()
			return mx
		}
		// Make the copy its OWN git repo. outDir lives inside the developer's
		// worktree, so a copy without .git makes git resolve the nearest
		// ancestor .git — the developer's repo — and Git-as-undo would branch
		// and check out there. Rooting the copy keeps the test meaningful and
		// contained. A failure is non-fatal: Git-as-undo then soft-fails.
		if ierr := initRepoCopy(repoCopy); ierr != nil {
			fmt.Printf("  %-24s git-init warning: %v\n", g.Name, ierr)
		}
		body["repository_path"] = repoCopy
	}
	// ADR-0070: send the bench task's deterministic checks (and, for `data`,
	// its JSON Schema) to the project's execution config so the engine's
	// *runtime* verifiers gate acceptance — not only the probe's independent
	// offline scorer (runChecks, M8-T21).
	exec := map[string]any{}
	if g.Archetype == "code" {
		// F4-T0: the Job Pod now materializes the candidate to
		// /tmp/solution.py and runs the test command with that as cwd, so
		// each code goal submits its real per-goal test. A code goal with no
		// test command is a matrix bug, not a silent no-op.
		tc := g.TestCommand
		if tc == "" {
			tc = "true"
		}
		exec["test_command"] = tc
		// ADR-0065: a bench code task with a starter tree passes its
		// fixture_path (absolute, so the daemon reads it regardless of cwd).
		if g.Fixture != "" {
			if abs, err := filepath.Abs(g.Fixture); err == nil {
				exec["fixture_path"] = abs
			}
		}
	}
	if c := checksToExecution(g.Checks); c != nil {
		exec["checks"] = c
	}
	if sp, ok := g.Checks["json_schema"].(string); ok && sp != "" {
		if abs, err := filepath.Abs(sp); err == nil {
			if b, rerr := os.ReadFile(abs); rerr == nil {
				exec["data_schema"] = string(b)
			}
		}
	}
	if len(exec) > 0 {
		body["execution"] = exec
	}
	if err := apiCall("POST", r.baseURL()+"/projects", body, &pr); err != nil {
		mx.Error = "create project: " + err.Error()
		return mx
	}

	// M8-T19: index the repository before the goal so the job's Dormant Index
	// is populated (best effort; a failure is a warning, not a job error).
	if repoSource != "" {
		var isum indexSummary
		if ierr := apiCall("POST", r.baseURL()+"/projects/"+pr.ID+"/index", map[string]any{}, &isum); ierr != nil {
			fmt.Printf("  %-24s index warning: %v\n", g.Name, ierr)
		} else {
			fmt.Printf("  %-24s indexed %d chunk(s), %d summar(ies)\n", g.Name, isum.Chunks, isum.Summarized)
		}
	}

	var gr struct {
		JobID  string   `json:"job_id"`
		JobIDs []string `json:"job_ids"`
	}
	if err := apiCall("POST", r.baseURL()+"/projects/"+pr.ID+"/goals", map[string]any{
		"goal": g.Goal, "acceptance_criteria": g.Criteria,
	}, &gr); err != nil {
		mx.Error = "submit goal: " + err.Error()
		return mx
	}
	// Single-task submit returns job_id; DAG mode (M6-T2, -dag) returns the
	// scheduled ready leaves in job_ids.
	jobIDs := gr.JobIDs
	if len(jobIDs) == 0 && gr.JobID != "" {
		jobIDs = []string{gr.JobID}
	}
	if len(jobIDs) == 0 {
		mx.Error = "submit goal: no jobs returned"
		return mx
	}
	mx.JobID = strings.Join(jobIDs, ",")

	state, artifactID, err := waitJobs(r.baseURL(), jobIDs, r.timeout)
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
	// The engine writes the strategy outcome just after the terminal
	// transition; a fast poll can observe `completed` before the row lands.
	// For a *code* goal the lag is much larger (~10s): `finished_at` is set
	// at the transition, but the outcome is written only after the Job Pod
	// finishes tearing down. Poll long enough to cover that teardown, and
	// reconcile at arm end as a backstop (reconcileMetrics).
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		o, ok, err := loadOutcome(db, jobIDs[0])
		if err == nil && ok {
			mx.Winner = winnerFromResult(o.Result)
			mx.Score = o.Score
			mx.Confidence = o.Confidence
			mx.TokenCost = o.TokenCost
			mx.WallMS = o.WallMS
			mx.Retries = o.Retries
			mx.ReflectionLoops = o.ReflectionLoops
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	_ = mx.fillCandidateMetrics(db, stateDir)
	mx.fillVerification(db)
	mx.fillTelemetry(db)
	if txt, err := readArtifact(stateDir, mx.ArtifactID); err == nil {
		mx.ArtifactText = txt
	}
	// M8-T21: apply the task's deterministic non-code checks to the accepted
	// artifact. Code tasks carry no `checks` (their test command is the check).
	if len(g.Checks) > 0 && mx.ArtifactText != "" {
		pass, results := runChecks(g.Checks, mx.ArtifactText)
		mx.CheckPass = &pass
		for _, r := range results {
			if r.Advisory {
				mx.CheckAdvisory = append(mx.CheckAdvisory, r.Kind)
			} else if !r.Pass {
				mx.CheckFailures = append(mx.CheckFailures, r.Kind)
			}
		}
	}
	return mx
}

// copyDir copies a repository tree to dst (creating it), skipping symlinks and
// non-regular files. A repo-backed task indexes and commits into the copy, so
// the checked-in fixture is never mutated.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// tierOf maps a goal's Source to a bench tier. A "bench:H" source is tier H;
// anything else is the E baseline (the locked M3-T7 set).
func tierOf(source string) string {
	if strings.HasPrefix(source, "bench:") {
		return strings.TrimPrefix(source, "bench:")
	}
	return "E"
}

// waitJobs waits for every job in ids to reach a terminal state, polling until
// all are done. It returns "completed" only when all completed, else "failed"
// (any failure/cancellation). artifactID is the last non-empty artifact among
// the jobs (DAG mode produces several).
func waitJobs(baseURL string, ids []string, timeout time.Duration) (state, artifactID string, err error) {
	deadline := time.Now().Add(timeout)
	pending := make(map[string]bool, len(ids))
	for _, id := range ids {
		pending[id] = true
	}
	states := make(map[string]string, len(ids))
	arts := make(map[string]string, len(ids))
	for time.Now().Before(deadline) {
		for id := range pending {
			var jr struct {
				State      string `json:"state"`
				ArtifactID string `json:"artifact_id"`
			}
			if err := apiCall("GET", baseURL+"/jobs/"+id, nil, &jr); err != nil {
				return "", "", err
			}
			switch jr.State {
			case "completed", "failed", "cancelled":
				states[id] = jr.State
				arts[id] = jr.ArtifactID
				delete(pending, id)
			}
		}
		if len(pending) == 0 {
			allCompleted := true
			last := ""
			for _, id := range ids {
				if states[id] != "completed" {
					allCompleted = false
				}
				if arts[id] != "" {
					last = arts[id]
				}
			}
			if allCompleted {
				return "completed", last, nil
			}
			return "failed", last, nil
		}
		time.Sleep(5 * time.Second)
	}
	return "", "", fmt.Errorf("jobs %v timed out after %s", ids, timeout)
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
		"confidence", "score", "token_cost", "wall_ms", "retries", "diversity", "candidates",
		"llm_calls", "prompt_tokens", "completion_tokens",
		"judge_called", "verifier_applied", "verifier_passed", "job_id", "error"}
	if err := w.Write(header); err != nil {
		return err
	}
	for _, m := range metrics {
		judgeCalled := ""
		if m.JudgeCalled != nil {
			judgeCalled = strconv.FormatBool(*m.JudgeCalled)
		}
		calls, promptTok, completionTok := 0, 0, 0
		if m.Telemetry != nil {
			calls, promptTok, completionTok = m.Telemetry.Calls, m.Telemetry.PromptTokens, m.Telemetry.CompletionTokens
		}
		if err := w.Write([]string{
			m.GoalName, m.Archetype, m.ModelLabel, m.Arm, strconv.Itoa(m.Run), m.State, m.Winner,
			strconv.FormatFloat(m.Confidence, 'f', 4, 64), strconv.FormatFloat(m.Score, 'f', 4, 64),
			strconv.Itoa(m.TokenCost), strconv.Itoa(m.WallMS), strconv.Itoa(m.Retries),
			strconv.FormatFloat(m.Diversity, 'f', 4, 64), strconv.Itoa(m.Candidates),
			strconv.Itoa(calls), strconv.Itoa(promptTok), strconv.Itoa(completionTok),
			judgeCalled, strconv.Itoa(m.VerifierApplied), strconv.FormatBool(m.VerifierPassed),
			m.JobID, m.Error,
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

// repoSourceFor returns the directory the probe indexes as the project
// repository for a task. A task with an explicit repo uses it. A non-code task
// with a fixture uses the fixture, so the corpus's "provided module/source"
// reaches the model through the MCE working set (M9). A code fixture is staged
// into the Job Pod via fixture_path (ADR-0065) and is not indexed.
func repoSourceFor(g sampleGoal) string {
	if g.Repo != "" {
		return g.Repo
	}
	if g.Archetype != "code" && g.Fixture != "" {
		return g.Fixture
	}
	return ""
}

// initRepoCopy turns a staged fixture/repo copy into its own git repository
// with an initial commit, so the engine's Git-as-undo operates on the copy
// rather than resolving the nearest ancestor .git (the developer's repo, since
// outDir is inside the worktree). The engine independently refuses a
// repository_path that is not a worktree root (ErrNotRepoRoot); this is the
// probe-side half. A failure is non-fatal — Git-as-undo then soft-fails.
func initRepoCopy(dir string) error {
	// No call here may sign or prompt. The operator's global config may set
	// commit.gpgsign=true; unattended, a pinentry prompt hangs the whole soak
	// (and hijacks the terminal). commit.gpgsign=false + --no-gpg-sign make the
	// fixture commit fail loudly instead; GIT_TERMINAL_PROMPT=0 does the same
	// for any credential prompt. The copy is throwaway, never the operator's
	// history, so signing it has no value.
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	run := func(args ...string) error {
		cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-C", dir}, args...)...)
		cmd.Env = env
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
		}
		return nil
	}
	if err := run("init", "-q"); err != nil {
		return err
	}
	if err := run("add", "-A"); err != nil {
		return err
	}
	// A fixture with no files makes the commit fail; tolerate it — the repo is
	// still rooted at dir, which is all the containment guard needs.
	_ = run("-c", "user.email=probe@athanor.invalid", "-c", "user.name=athanor-probe",
		"commit", "--no-gpg-sign", "-q", "-m", "fixture")
	return nil
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
	if sc := loadConformance(filepath.Join(*outDir, "scenarios.json")); len(sc) > 0 {
		report += "\n" + renderConformance(sc)
	}
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

// runCompare is the `compare` subcommand (M8-T30): it renders the guidance A/B
// delta between a baseline results tree (guidance off) and the current one
// (guidance on), writing guidance-delta.md into the current tree.
func runCompare(args []string) {
	fs := flag.NewFlagSet("compare", flag.ExitOnError)
	baseline := fs.String("baseline", "", "baseline results dir (guidance off)")
	outDir := fs.String("out", filepath.Join("spikes", "m3-t7-probe", "results"), "current results dir (guidance on)")
	_ = fs.Parse(args)
	if *baseline == "" {
		fmt.Fprintln(os.Stderr, "m3-t7 compare: -baseline is required")
		os.Exit(1)
	}
	base, err := loadAllMetrics(*baseline)
	if err != nil {
		fmt.Fprintln(os.Stderr, "m3-t7 compare:", err)
		os.Exit(1)
	}
	cur, err := loadAllMetrics(*outDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "m3-t7 compare:", err)
		os.Exit(1)
	}
	if len(base) == 0 || len(cur) == 0 {
		fmt.Fprintln(os.Stderr, "m3-t7 compare: need rows in both the baseline and current trees")
		os.Exit(1)
	}
	delta := renderGuidanceDelta(base, cur)
	fmt.Print(delta)
	if err := os.WriteFile(filepath.Join(*outDir, "guidance-delta.md"), []byte(delta), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "m3-t7 compare:", err)
		os.Exit(1)
	}
}

// printConfig is the `config` subcommand: it prints the generated config
// for one (model, arm) so an operator can inspect it or boot the daemon
// by hand for a pre-flight. It is also the pre-flight validity check —
// the generated YAML must be accepted by internal/config (see
// probeconfig_test.go).
func printConfig(args []string) {
	fs := flag.NewFlagSet("config", flag.ExitOnError)
	modelLabel := fs.String("model", probeModels[0].Label, "model label")
	armName := fs.String("arm", arms[0].Name, "arm name (single|dialectical|bestof3|reflect|full)")
	seed := fs.String("seed", "off", "judgment_seed policy: off|derived")
	addr := fs.String("addr", strings.TrimPrefix(daemonURL(), "http://"), "daemon listen address (must match the Host-header allowlist)")
	noReflect := fs.Bool("no-reflect", false, "disable reflection loops")
	judgeMode := fs.String("judge-mode", "", `execution.policy.judge_mode: "" (default) | llm | verifier`)
	judgeModel := fs.String("judge-model", "", "personas.security.model override")
	altModel := fs.String("alt-model", "", "personas.alternative.model override")
	ollamaURL := fs.String("ollama", "http://localhost:11434", "Ollama base URL written into the config")
	gates := fs.Bool("gates", false, "enable the F5 code acceptance gates")
	_ = fs.Parse(args)

	m, ok := modelByLabel(*modelLabel)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown model label %q\n", *modelLabel)
		os.Exit(2)
	}
	// namedArm covers the ablation/soak arms (bestof3|reflect|full) as well as
	// the headline pair, so the preflight accepts every arm the run/soak
	// subcommands do.
	a, ok := namedArm(*armName)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown arm %q (single|dialectical|bestof3|reflect|full)\n", *armName)
		os.Exit(2)
	}
	fmt.Print(probeConfigYAML(m, a, *seed, *addr, *noReflect,
		probePolicy{JudgeMode: *judgeMode, JudgeModel: *judgeModel, AltModel: *altModel, OllamaURL: *ollamaURL, CodeGates: *gates}))
}
