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
	fs.StringVar(&c.seedPolicy, "seed", "off", "judgment_seed policy: off|derived")
	fs.DurationVar(&c.timeout, "timeout", 45*time.Minute, "per-job wall-clock timeout")
	fs.DurationVar(&c.soakInterval, "soak-interval", 30*time.Second, "RSS/DB/disk sampling interval")
	fs.DurationVar(&c.killAfter, "kill-after", 0, "kill -9 the daemon once after this elapsed time, then restart (0 = off)")
	fs.BoolVar(&c.gates, "gates", false, "enable the F5 code acceptance gates")
	fs.StringVar(&c.ollamaURL, "ollama", "http://localhost:11434", "Ollama base URL for the /api/ps residency check")
	fs.StringVar(&c.judgeModel, "judge-model", "", "personas.security.model override (use a cross-family judge)")
	fs.StringVar(&c.onlyCSV, "only", "", "comma-separated goal names to soak (empty = all)")
	fs.IntVar(&c.goalLimit, "goals", 0, "limit to the first N goals after loading (0 = all)")
	_ = fs.Parse(args)
	if err := c.run(); err != nil {
		fmt.Fprintln(os.Stderr, "m3-t7 soak:", err)
		os.Exit(1)
	}
}

func (c *soakRunConfig) run() error {
	if _, err := os.Stat(c.binary); err != nil {
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
	abs, err := filepath.Abs(c.outDir)
	if err != nil {
		return err
	}
	c.outDir = abs
	stateDir := filepath.Join(c.outDir, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	cfgPath := filepath.Join(c.outDir, "config.yaml")
	pol := probePolicy{OllamaURL: c.ollamaURL, CodeGates: c.gates, JudgeModel: c.judgeModel}
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

	r := &runnerConfig{addr: c.addr, timeout: c.timeout, outDir: c.outDir, ollamaURL: c.ollamaURL}

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
	proc, logPath, err := r.startDaemon(cfgPath, stateDir)
	if err != nil {
		return err
	}
	if err := waitHealthy(r.baseURL(), 90*time.Second); err != nil {
		return fmt.Errorf("daemon not healthy (see %s): %w", logPath, err)
	}
	logEvent("soak start: model=%s arm=%s goals=%d hours=%.2f", m.Label, a.Name, len(goals), c.hours)
	sampler := startSoak(c.outDir, c.soakInterval, proc.Process.Pid, stateDir)

	deadline := start.Add(time.Duration(c.hours * float64(time.Hour)))
	killed := false
	pass := 0
	for time.Now().Before(deadline) && !interrupted.Load() {
		pass++
		fmt.Printf("soak: pass %d at %s\n", pass, time.Now().Format(time.RFC3339))
		for _, g := range goals {
			if interrupted.Load() {
				break
			}
			if c.killAfter > 0 && !killed && time.Since(start) >= c.killAfter {
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
				sampler = startSoak(c.outDir, c.soakInterval, proc.Process.Pid, stateDir)
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

	sampler.stopAndWait()
	r.stopDaemon(proc)
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
	return os.WriteFile(filepath.Join(outDir, "report.md"), []byte(renderReport(metrics)), 0o644)
}
