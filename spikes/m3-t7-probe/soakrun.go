// Combined endurance + quality soak (M8-T23). Unlike `run`, which starts and
// stops a daemon per (model, arm), `soak` holds ONE long-lived daemon and
// repeats a single arm's workload for a wall-time budget, so a 24h run tests
// steady-state endurance while producing a quality readout. It is built to be
// stoppable early and resumable:
//
//   - results accumulate in metrics.jsonl; a restart appends, it does not
//     reset;
//   - a rolling report.md/results.json/csv is written after every pass, so
//     stopping at hour 6 still yields a readout for what completed;
//   - SIGINT/SIGTERM finalizes cleanly (flush + report + stop daemon);
//   - -kill-after injects one SIGKILL + restart so crash recovery is exercised
//     and recorded in soak-events.log.
//
// The sleep/wake checkpoint stays human (the host cannot be put to sleep
// safely by the harness); the runbook records it alongside this run.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

type soakRunConfig struct {
	binary       string
	addr         string
	outDir       string
	corpusPath   string
	modelLabel   string
	armName      string
	hours        float64
	seedPolicy   string
	timeout      time.Duration
	soakInterval time.Duration
	killAfter    time.Duration
	gates        bool
	ollamaURL    string
	// judgeModel overrides personas.security.model so the deciding judge is a
	// different family from the generator (F4 cross-family guard).
	judgeModel string
	// onlyCSV / goalLimit select a subset of the goal set (smoke + partial
	// runs), reusing the `run` command's semantics.
	onlyCSV   string
	goalLimit int
	// anchorPath, when set, narrows the corpus to the ids in that file (the
	// 12-task onboarding/leaderboard anchor). It composes with -only.
	anchorPath string
	// passes caps the number of full corpus passes (0 = until the deadline).
	// `-passes 1` is the full-slice shape: one complete pass, then stop, even
	// if the wall-time budget has room for another.
	passes int
	// external attaches to an already-running Core Pod. The probe never starts,
	// stops, kills, or restores a daemon; it reads stateDir and submits to addr.
	external bool
	// stateDir is the external Core Pod's state dir (inside the Core Pod, the
	// mounted state volume). Empty in host-binary mode (defaults to <out>/state).
	stateDir string
	// rolesCSV is the heterogeneous per-role model map (M9): "main=tag,
	// tall=tag, security=tag, alternative=tag, wide=tag". Empty means every
	// generation role uses the -model, judge uses -judge-model.
	rolesCSV string
	// dag enables execution.dag_decomposition (M6-T2): each goal is decomposed
	// by the tall persona and run through the dependency scheduler.
	dag bool
	// until is a wall-clock deadline spec (M8-T31): "midnight", "HH:MM", or
	// RFC3339. When set it overrides -hours and is persisted so a resume keeps
	// the original target.
	until string
}

// parseRoleModels parses the -roles CSV ("main=tag,tall=tag,…") into a
// role→model map (M9). Empty input returns nil (single-model behavior). An
// unknown role or a missing "=" is an error, so a typo cannot silently fall
// back to the run model and corrupt the experiment.
func parseRoleModels(csv string) (map[string]string, error) {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil, nil
	}
	valid := map[string]bool{"main": true, "tall": true, "security": true, "alternative": true, "wide": true}
	out := map[string]string{}
	for _, part := range strings.Split(csv, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		role, tag, ok := strings.Cut(part, "=")
		role, tag = strings.TrimSpace(role), strings.TrimSpace(tag)
		if !ok || role == "" || tag == "" {
			return nil, fmt.Errorf("bad -roles entry %q (want role=model)", part)
		}
		if !valid[role] {
			return nil, fmt.Errorf("unknown role %q in -roles (want main|tall|security|alternative|wide)", role)
		}
		out[role] = tag
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func runSoak(args []string) {
	c := soakRunConfig{}
	fs := flag.NewFlagSet("soak", flag.ExitOnError)
	fs.StringVar(&c.binary, "bin", "bin/athanor", "path to the athanor binary (make build)")
	fs.StringVar(&c.addr, "addr", strings.TrimPrefix(daemonURL(), "http://"), "daemon listen address (loopback only)")
	fs.StringVar(&c.outDir, "out", filepath.Join("spikes", "m3-t7-probe", "results", "soak"), "results directory (resumable)")
	fs.StringVar(&c.corpusPath, "corpus", "", "path to eval/bench/tasks.yaml (recommended); empty uses the locked sampleGoals")
	fs.StringVar(&c.modelLabel, "model", "", "model label to soak (required; see matrix.go)")
	fs.StringVar(&c.armName, "arm", "full", "arm: single | dialectical | bestof3 | reflect | full")
	fs.Float64Var(&c.hours, "hours", 24, "target wall time in hours")
	fs.StringVar(&c.until, "until", "", "run until a wall-clock deadline: midnight | HH:MM | RFC3339 (overrides -hours; persisted for resume)")
	fs.StringVar(&c.seedPolicy, "seed", "off", "judgment_seed policy: off|derived")
	fs.DurationVar(&c.timeout, "timeout", 45*time.Minute, "per-job wall-clock timeout")
	fs.DurationVar(&c.soakInterval, "soak-interval", 30*time.Second, "RSS/DB/disk sampling interval")
	fs.DurationVar(&c.killAfter, "kill-after", 0, "kill -9 the daemon once after this elapsed time, then restart (0 = off)")
	fs.BoolVar(&c.gates, "gates", false, "enable the F5 code acceptance gates")
	fs.StringVar(&c.ollamaURL, "ollama", "http://localhost:11434", "Ollama base URL for the /api/ps residency check")
	fs.StringVar(&c.judgeModel, "judge-model", "", "personas.security.model override (use a cross-family judge)")
	fs.StringVar(&c.rolesCSV, "roles", "", "heterogeneous per-role models: main=tag,tall=tag,security=tag,alternative=tag,wide=tag (overrides -model per role)")
	fs.BoolVar(&c.dag, "dag", false, "enable execution.dag_decomposition: decompose each goal and run the M6 scheduler")
	fs.StringVar(&c.onlyCSV, "only", "", "comma-separated goal names to soak (empty = all)")
	fs.StringVar(&c.anchorPath, "anchor-tasks", "", "anchor YAML file whose task ids narrow the corpus (eval/bench/anchor.yaml)")
	fs.IntVar(&c.passes, "passes", 0, "cap full corpus passes (0 = until the deadline); 1 = one full slice")
	fs.BoolVar(&c.external, "external", false, "attach to an already-running Core Pod; do not start/stop/kill a daemon")
	fs.StringVar(&c.stateDir, "state-dir", "", "external mode: the Core Pod's state dir (required with -external)")
	fs.IntVar(&c.goalLimit, "goals", 0, "limit to the first N goals after loading (0 = all)")
	_ = fs.Parse(args)
	if err := c.run(); err != nil {
		fmt.Fprintln(os.Stderr, "m3-t7 soak:", err)
		os.Exit(1)
	}
}

func (c *soakRunConfig) run() error {
	if c.external {
		if c.stateDir == "" {
			return fmt.Errorf("-external requires -state-dir (the Core Pod's mounted state volume)")
		}
		if c.killAfter > 0 {
			return fmt.Errorf("-external cannot inject a daemon kill")
		}
	} else if _, err := os.Stat(c.binary); err != nil {
		return fmt.Errorf("athanor binary %q not found (run `make build`): %w", c.binary, err)
	}
	m, ok := modelByLabel(c.modelLabel)
	if !ok {
		return fmt.Errorf("unknown -model %q (see matrix.go)", c.modelLabel)
	}
	a, ok := namedArm(c.armName)
	if !ok {
		return fmt.Errorf("unknown -arm %q (single|dialectical|bestof3|reflect|full)", c.armName)
	}
	if c.passes < 0 {
		return fmt.Errorf("-passes must be >= 0, got %d", c.passes)
	}
	goals := sampleGoals
	if c.corpusPath != "" {
		g, err := loadCorpus(c.corpusPath)
		if err != nil {
			return err
		}
		goals = g
	}
	// Apply -only / -goals selection (same semantics as the `run` command).
	sel := &runnerConfig{corpus: goals, onlyCSV: c.onlyCSV, goalLimit: c.goalLimit}
	goals = sel.goals()
	// The anchor is an explicit subset used by onboarding/leaderboard; it is
	// never the headline run.
	filtered, err := filterByAnchor(goals, c.anchorPath)
	if err != nil {
		return err
	}
	goals = filtered
	abs, err := filepath.Abs(c.outDir)
	if err != nil {
		return err
	}
	c.outDir = abs
	stateDir := filepath.Join(c.outDir, "state")
	if c.external {
		stateDir = c.stateDir
	} else if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	roleModels, err := parseRoleModels(c.rolesCSV)
	if err != nil {
		return err
	}
	cfgPath := filepath.Join(c.outDir, "config.yaml")
	pol := probePolicy{
		OllamaURL: c.ollamaURL, CodeGates: c.gates, JudgeModel: c.judgeModel,
		RoleModels: roleModels, DAG: c.dag, Soak: true,
	}
	if err := os.WriteFile(cfgPath, []byte(probeConfigYAML(m, a, c.seedPolicy, c.addr, false, pol)), 0o644); err != nil {
		return err
	}

	metricsPath := filepath.Join(c.outDir, "metrics.jsonl")
	prior, err := loadMetricRows(metricsPath)
	if err != nil {
		return err
	}
	if len(prior) > 0 {
		fmt.Printf("soak: resuming — %d rows already collected\n", len(prior))
	}
	// A forced re-run clears a previous completion marker so the ladder does not
	// skip it; an interrupted run never writes one (see writeComplete).
	_ = os.Remove(filepath.Join(c.outDir, "complete"))

	r := &runnerConfig{
		binary:    c.binary,
		addr:      c.addr,
		outDir:    c.outDir,
		timeout:   c.timeout,
		ollamaURL: c.ollamaURL,
	}

	var interrupted atomic.Bool
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigc
		interrupted.Store(true)
	}()

	eventsPath := filepath.Join(c.outDir, "soak-events.log")
	logEvent := func(format string, args ...any) {
		line := time.Now().UTC().Format(time.RFC3339) + " " + fmt.Sprintf(format, args...) + "\n"
		f, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.WriteString(line)
			_ = f.Close()
		}
		fmt.Print("  " + line)
	}

	start := time.Now()
	// M8-T31: resolve the wall-clock deadline. -until wins over -hours; a
	// persisted deadline (written on the first start) wins over both so a
	// restart never slides the target.
	deadline := start.Add(time.Duration(c.hours * float64(time.Hour)))
	if c.until != "" {
		d, derr := resolveDeadline(c.until, start)
		if derr != nil {
			return derr
		}
		deadline = d
	}
	deadlinePath := filepath.Join(c.outDir, "deadline")
	if raw, rerr := os.ReadFile(deadlinePath); rerr == nil {
		if t, perr := time.Parse(time.RFC3339, strings.TrimSpace(string(raw))); perr == nil {
			deadline = t
			fmt.Printf("soak: resuming to persisted deadline %s\n", deadline.Local().Format(time.RFC3339))
		}
	} else if c.until != "" {
		_ = os.WriteFile(deadlinePath, []byte(deadline.UTC().Format(time.RFC3339)), 0o644)
	}
	if remaining := time.Until(deadline); remaining <= 0 {
		return fmt.Errorf("deadline %s is already in the past", deadline.Local().Format(time.RFC3339))
	} else if remaining < 15*time.Minute {
		fmt.Printf("soak: WARNING only %s until the deadline %s\n",
			remaining.Round(time.Minute), deadline.Local().Format(time.RFC3339))
	} else {
		fmt.Printf("soak: running until %s (%s)\n", deadline.Local().Format(time.RFC3339), remaining.Round(time.Minute))
	}

	var proc *exec.Cmd
	var logPath string
	if c.external {
		if err := waitHealthy(r.baseURL(), 30*time.Second); err != nil {
			return fmt.Errorf("external Core Pod not healthy at %s: %w", r.baseURL(), err)
		}
	} else {
		proc, logPath, err = r.startDaemon(cfgPath, stateDir)
		if err != nil {
			return err
		}
		if err := waitHealthy(r.baseURL(), 90*time.Second); err != nil {
			return fmt.Errorf("daemon not healthy (see %s): %w", logPath, err)
		}
	}
	logEvent("soak start: model=%s arm=%s goals=%d passes=%d external=%v until=%s", m.Label, a.Name, len(goals), c.passes, c.external, deadline.Local().Format(time.RFC3339))
	var sampler *soakSampler
	if !c.external {
		sampler = startSoak(c.outDir, c.soakInterval, proc.Process.Pid, stateDir, r.baseURL()+"/healthz")
	}
	killed := false
	pass := 0
	for passAllowed(pass, c.passes, deadline, time.Now(), interrupted.Load()) {
		pass++
		fmt.Printf("soak: pass %d at %s\n", pass, time.Now().Format(time.RFC3339))
		for _, g := range goals {
			if interrupted.Load() {
				break
			}
			if midPassDeadline(c.passes, deadline, time.Now()) {
				logEvent("hard stop: deadline reached before %s; run is incomplete", g.Name)
				interrupted.Store(true)
				break
			}
			if c.killAfter > 0 && !killed && !c.external && time.Since(start) >= c.killAfter {
				logEvent("injecting kill -9 after %s", time.Since(start).Round(time.Second))
				_ = proc.Process.Kill()
				_, _ = proc.Process.Wait()
				sampler.stopAndWait()
				proc, logPath, err = r.startDaemon(cfgPath, stateDir)
				if err != nil {
					return err
				}
				if err := waitHealthy(r.baseURL(), 120*time.Second); err != nil {
					return fmt.Errorf("daemon not healthy after kill/restart (see %s): %w", logPath, err)
				}
				sampler = startSoak(c.outDir, c.soakInterval, proc.Process.Pid, stateDir, r.baseURL()+"/healthz")
				logEvent("daemon restarted; state recovered")
				killed = true
			}
			mx := r.runOne(g, m, a, pass, stateDir)
			if err := appendMetricRow(metricsPath, mx); err != nil {
				return err
			}
			prior = append(prior, mx)
			if mx.Error != "" {
				fmt.Printf("  %-24s ERROR %s\n", g.Name, mx.Error)
			} else {
				fmt.Printf("  %-24s state=%-9s pass=%v\n", g.Name, mx.State, jobPassed(mx))
			}
		}
		if err := writeRollingReport(c.outDir, prior); err != nil {
			return err
		}
	}

	// ADR-0073 §2: conformance scenarios against the live daemon, after the
	// workload, so freezing cannot stall a job. External mode never runs the
	// destructive lifecycle/restore scenarios: the Core Pod is not ours to stop.
	deps := scenarioDeps{baseURL: r.baseURL(), stateDir: stateDir, outDir: c.outDir, binary: c.binary}
	var scenarios []scenarioResult
	if !c.external {
		scenarios = runScenarios(deps)
		sampler.stopAndWait()
		r.stopDaemon(proc)
		// The restore scenario needs the daemon stopped: it replaces a database.
		scenarios = append(scenarios, scenarioRestore(deps))
	}

	if err := writeJSON(filepath.Join(c.outDir, "scenarios.json"), scenarios); err != nil {
		return err
	}
	passed, skipped := 0, 0
	for _, s := range scenarios {
		switch {
		case s.Skipped:
			skipped++
		case s.Pass:
			passed++
		}
	}
	logEvent("conformance: %d passed, %d skipped, %d failed", passed, skipped, len(scenarios)-passed-skipped)

	if err := writeRollingReport(c.outDir, prior); err != nil {
		return err
	}
	if s := soakSummary(filepath.Join(c.outDir, "soak.csv")); s != "" {
		fmt.Printf("soak: %s\n", s)
	}
	if names := settleOrphanPods(30 * time.Second); len(names) > 0 {
		logEvent("WARNING: %d orphan job pod(s) at end: %v", len(names), names)
	}
	reason := "budget reached"
	if interrupted.Load() {
		reason = "interrupted (early stop)"
		_ = os.Remove(filepath.Join(c.outDir, "complete"))
	} else if err := writeComplete(c.outDir, len(prior), pass); err != nil {
		return err
	}
	logEvent("soak end: %s; %d rows, %d passes", reason, len(prior), pass)
	return nil
}

// loadMetricRows reads an existing metrics.jsonl (append-only) for resume.
func loadMetricRows(path string) ([]jobMetrics, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []jobMetrics
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m jobMetrics
		if json.Unmarshal([]byte(line), &m) == nil {
			out = append(out, m)
		}
	}
	return out, sc.Err()
}

// appendMetricRow appends one JSON row to metrics.jsonl.
func appendMetricRow(path string, m jobMetrics) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.Write(append(b, '\n'))
	return err
}

// midPassDeadline reports whether a capped run has hit its wall-time deadline
// between tasks. An uncapped endurance run (maxPasses == 0) deliberately
// finishes its pass; a capped full-slice/anchor run stops and is marked
// incomplete rather than overrunning the budget. An in-flight job can still
// overshoot by up to the per-job timeout.
func midPassDeadline(maxPasses int, deadline, now time.Time) bool {
	return maxPasses > 0 && now.After(deadline)
}

// passAllowed decides whether to start the next corpus pass. A positive
// maxPasses is a hard pass cap (the full-slice shape); zero keeps the original
// deadline behavior. The deadline is checked only between passes, so a pass
// that starts always finishes (bounded by the per-job timeout).
func passAllowed(pass, maxPasses int, deadline, now time.Time, interrupted bool) bool {
	if interrupted {
		return false
	}
	if maxPasses > 0 {
		return pass < maxPasses
	}
	return now.Before(deadline)
}

// writeComplete marks a soak as a normal budget-exhausted run. The ladder skips
// a model only when this marker exists: report.md is written after every pass
// (including an interrupted one), so it cannot distinguish a complete run from
// a stopped one. An interrupted run removes the marker.
func writeComplete(outDir string, rows, passes int) error {
	body := fmt.Sprintf("complete %s rows=%d passes=%d\n", time.Now().UTC().Format(time.RFC3339), rows, passes)
	return os.WriteFile(filepath.Join(outDir, "complete"), []byte(body), 0o644)
}

// soakComplete reports whether outDir holds a normal-completion marker.
func soakComplete(outDir string) bool {
	_, err := os.Stat(filepath.Join(outDir, "complete"))
	return err == nil
}

// writeRollingReport writes the aggregate artifacts (report.md, results.json,
// results.csv) from every row collected so far. It is called after each pass so
// a stopped run still has a complete-as-of-now readout. (The judge packets are
// not regenerated here; run `judge` afterwards.)
func writeRollingReport(outDir string, metrics []jobMetrics) error {
	if err := writeJSON(filepath.Join(outDir, "results.json"), metrics); err != nil {
		return err
	}
	if err := writeCSV(filepath.Join(outDir, "results.csv"), metrics); err != nil {
		return err
	}
	report := renderReport(metrics)
	// ADR-0073 §2: fold the conformance matrix in when a soak wrote one.
	if sc := loadConformance(filepath.Join(outDir, "scenarios.json")); len(sc) > 0 {
		report += "\n" + renderConformance(sc)
	}
	return os.WriteFile(filepath.Join(outDir, "report.md"), []byte(report), 0o644)
}

// resolveDeadline parses the -until spec into an absolute local-time deadline
// (M8-T31). "midnight" is the upcoming 00:00 (the end of today when run in the
// evening); "HH:MM" is the next occurrence of that local time; an RFC3339
// timestamp is used verbatim.
func resolveDeadline(spec string, now time.Time) (time.Time, error) {
	switch strings.TrimSpace(strings.ToLower(spec)) {
	case "midnight", "tonight":
		y, mo, d := now.Date()
		return time.Date(y, mo, d, 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1), nil
	}
	if t, err := time.ParseInLocation("15:04", strings.TrimSpace(spec), now.Location()); err == nil {
		d := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
		if !d.After(now) {
			d = d.AddDate(0, 0, 1)
		}
		return d, nil
	}
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(spec)); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("unrecognized -until %q (use midnight, HH:MM, or RFC3339)", spec)
}
