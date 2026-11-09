// Paired v3 report (ADR-0133): joins the single, cascade, and full arms by
// task, computes per-task pass rates, a task-clustered bootstrap interval for
// the paired delta, the non-inferiority claim, and the cascade early-stop
// metrics. It is pure over collected rows; it never calls a model.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sort"
	"strings"
)

type pairedOptions struct {
	Generator       string
	Suite           string
	MarginPoints    float64
	MaxTaskLossRuns int
	Iterations      int
	Seed            int64
}

type pairedArmSummary struct {
	Arm        string  `json:"arm"`
	Tasks      int     `json:"tasks"`
	Runs       int     `json:"runs"`
	Passes     int     `json:"passes"`
	Rate       float64 `json:"rate"`
	MeanTokens float64 `json:"mean_tokens"`
	MeanWallMS float64 `json:"mean_wall_ms"`
}

type pairedTaskRow struct {
	Task            string  `json:"task"`
	Suite           string  `json:"suite,omitempty"`
	Runs            int     `json:"runs"`
	SingleRate      float64 `json:"single_rate"`
	CascadeRate     float64 `json:"cascade_rate"`
	FullRate        float64 `json:"full_rate"`
	CascadeLossRuns int     `json:"cascade_loss_runs"`
	FullLossRuns    int     `json:"full_loss_runs"`
	HasCascade      bool    `json:"has_cascade"`
	HasFull         bool    `json:"has_full"`
}

type pairedNonInferiority struct {
	Baseline    string  `json:"baseline"`
	Treatment   string  `json:"treatment"`
	Tasks       int     `json:"tasks"`
	MeanDelta   float64 `json:"mean_delta"`
	CILow       float64 `json:"ci_low"`
	CIHigh      float64 `json:"ci_high"`
	Margin      float64 `json:"margin"`
	Regressions int     `json:"regressions"`
	Pass        bool    `json:"pass"`
}

type pairedEarlyStop struct {
	Tasks             int     `json:"tasks"`
	Runs              int     `json:"runs"`
	EarlyStops        int     `json:"early_stops"`
	StopRate          float64 `json:"stop_rate"`
	EarlyStopPassRate float64 `json:"early_stop_pass_rate"`
	Escalations       int     `json:"escalations"`
	EscalationRate    float64 `json:"escalation_rate"`
}

type pairedReport struct {
	Generator      string                 `json:"generator,omitempty"`
	Suite          string                 `json:"suite,omitempty"`
	Arms           []pairedArmSummary     `json:"arms"`
	Tasks          []pairedTaskRow        `json:"tasks"`
	NonInferiority []pairedNonInferiority `json:"non_inferiority"`
	EarlyStop      pairedEarlyStop        `json:"early_stop"`
}

// buildPairedReport joins rows by task and arm. Pairing is by task and run
// index; generation is unseeded, so the interval estimates task-level
// variance, not within-pair covariance.
func buildPairedReport(metrics []jobMetrics, opt pairedOptions) pairedReport {
	if opt.Iterations <= 0 {
		opt.Iterations = 10000
	}
	if opt.Seed == 0 {
		opt.Seed = 1
	}
	rows := make([]jobMetrics, 0, len(metrics))
	for _, m := range metrics {
		if opt.Generator != "" && m.ModelLabel != opt.Generator {
			continue
		}
		if opt.Suite != "" && m.Suite != opt.Suite {
			continue
		}
		rows = append(rows, m)
	}
	report := pairedReport{Generator: opt.Generator, Suite: opt.Suite}

	// Summary per arm.
	arms := map[string]*pairedArmSummary{}
	for _, m := range rows {
		a := arms[m.Arm]
		if a == nil {
			a = &pairedArmSummary{Arm: m.Arm}
			arms[m.Arm] = a
		}
		a.Runs++
		if jobPassed(m) {
			a.Passes++
		}
		a.MeanTokens += float64(m.TokenCost)
		a.MeanWallMS += float64(m.WallMS)
	}
	for _, a := range arms {
		if a.Runs > 0 {
			a.Rate = float64(a.Passes) / float64(a.Runs)
			a.MeanTokens /= float64(a.Runs)
			a.MeanWallMS /= float64(a.Runs)
		}
		report.Arms = append(report.Arms, *a)
	}
	sort.Slice(report.Arms, func(i, j int) bool { return report.Arms[i].Arm < report.Arms[j].Arm })

	// Group by (task, arm) and by run for the loss count.
	type key struct{ task, arm string }
	cells := map[key][]jobMetrics{}
	tasks := map[string]string{} // task -> suite
	for _, m := range rows {
		k := key{m.GoalName, m.Arm}
		cells[k] = append(cells[k], m)
		if m.Suite != "" {
			tasks[m.GoalName] = m.Suite
		} else {
			tasks[m.GoalName] = ""
		}
	}
	var taskNames []string
	for t := range tasks {
		taskNames = append(taskNames, t)
	}
	sort.Strings(taskNames)
	for _, t := range taskNames {
		single := cells[key{t, "single"}]
		cascade := cells[key{t, "cascade"}]
		full := cells[key{t, "full"}]
		if len(single) == 0 || (len(cascade) == 0 && len(full) == 0) {
			continue
		}
		row := pairedTaskRow{
			Task:            t,
			Suite:           tasks[t],
			Runs:            len(single),
			SingleRate:      taskPassRate(single),
			CascadeRate:     taskPassRate(cascade),
			FullRate:        taskPassRate(full),
			CascadeLossRuns: lossRuns(single, cascade),
			FullLossRuns:    lossRuns(single, full),
			HasCascade:      len(cascade) > 0,
			HasFull:         len(full) > 0,
		}
		report.Tasks = append(report.Tasks, row)
	}
	for i := range report.Arms {
		report.Arms[i].Tasks = len(report.Tasks)
	}

	// Non-inferiority: cascade and full vs single.
	for _, treatment := range []string{"cascade", "full"} {
		var deltas []float64
		regressions := 0
		for _, tr := range report.Tasks {
			var rate float64
			var loss int
			switch treatment {
			case "cascade":
				if !tr.HasCascade {
					continue
				}
				rate, loss = tr.CascadeRate, tr.CascadeLossRuns
			case "full":
				if !tr.HasFull {
					continue
				}
				rate, loss = tr.FullRate, tr.FullLossRuns
			}
			deltas = append(deltas, rate-tr.SingleRate)
			if loss >= opt.MaxTaskLossRuns {
				regressions++
			}
		}
		if len(deltas) == 0 {
			continue
		}
		mean, lo, hi := bootstrapMeanCI(deltas, opt.Iterations, opt.Seed)
		ni := pairedNonInferiority{
			Baseline:    "single",
			Treatment:   treatment,
			Tasks:       len(deltas),
			MeanDelta:   mean,
			CILow:       lo,
			CIHigh:      hi,
			Margin:      -opt.MarginPoints / 100.0,
			Regressions: regressions,
			Pass:        lo*100 > -opt.MarginPoints && regressions <= 0,
		}
		report.NonInferiority = append(report.NonInferiority, ni)
	}

	// Cascade early stop.
	total, early, earlyPass, escalated := 0, 0, 0, 0
	for _, m := range rows {
		if m.Arm != "cascade" || m.Candidates <= 0 {
			continue
		}
		total++
		if m.Candidates == 1 {
			early++
			if jobPassed(m) {
				earlyPass++
			}
		} else {
			escalated++
		}
	}
	report.EarlyStop = pairedEarlyStop{
		Tasks:             len(tasks),
		Runs:              total,
		EarlyStops:        early,
		Escalations:       escalated,
		EarlyStopPassRate: ratio(earlyPass, early),
	}
	if total > 0 {
		report.EarlyStop.StopRate = float64(early) / float64(total)
		report.EarlyStop.EscalationRate = float64(escalated) / float64(total)
	}
	return report
}

func taskPassRate(rows []jobMetrics) float64 {
	if len(rows) == 0 {
		return 0
	}
	passes := 0
	for _, m := range rows {
		if jobPassed(m) {
			passes++
		}
	}
	return float64(passes) / float64(len(rows))
}

// lossRuns counts runs where the baseline passed and the treatment failed.
// A missing treatment run is not counted as a loss; it is a coverage gap
// visible in the run counts.
func lossRuns(baseline, treatment []jobMetrics) int {
	byRun := map[int]bool{}
	for _, m := range treatment {
		byRun[m.Run] = jobPassed(m)
	}
	loss := 0
	for _, m := range baseline {
		if !jobPassed(m) {
			continue
		}
		if passed, ok := byRun[m.Run]; ok && !passed {
			loss++
		}
	}
	return loss
}

func ratio(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}

// bootstrapMeanCI returns the mean and the 2.5/97.5 percentiles of a
// task-clustered bootstrap over the paired deltas. It is deterministic for a
// fixed seed so a report can be reproduced.
func bootstrapMeanCI(values []float64, iterations int, seed int64) (mean, lo, hi float64) {
	if len(values) == 0 {
		return 0, 0, 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	mean = sum / float64(len(values))
	if iterations <= 0 {
		return mean, mean, mean
	}
	rng := rand.New(rand.NewSource(seed))
	samples := make([]float64, iterations)
	for i := 0; i < iterations; i++ {
		s := 0.0
		for j := 0; j < len(values); j++ {
			s += values[rng.Intn(len(values))]
		}
		samples[i] = s / float64(len(values))
	}
	sort.Float64s(samples)
	lo = samples[int(math.Floor(0.025*float64(iterations)))]
	hi = samples[int(math.Ceil(0.975*float64(iterations)))-1]
	return mean, lo, hi
}

func renderPairedMarkdown(r pairedReport) string {
	var b strings.Builder
	b.WriteString("# Paired v3 report\n\n")
	if r.Generator != "" {
		fmt.Fprintf(&b, "**Generator:** `%s`  \n", r.Generator)
	}
	if r.Suite != "" {
		fmt.Fprintf(&b, "**Suite:** `%s`  \n", r.Suite)
	}
	b.WriteString("\n## Arms\n\n")
	b.WriteString("| arm | tasks | runs | passes | rate | mean tok | mean wall (s) |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|\n")
	for _, a := range r.Arms {
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %.1f%% | %.0f | %.1f |\n",
			a.Arm, a.Tasks, a.Runs, a.Passes, a.Rate*100, a.MeanTokens, a.MeanWallMS/1000)
	}
	b.WriteString("\n## Non-inferiority (baseline: single)\n\n")
	b.WriteString("| treatment | tasks | mean Δ | 95% CI | margin | regressions | claim |\n")
	b.WriteString("|---|---:|---:|---|---:|---:|---|\n")
	for _, ni := range r.NonInferiority {
		verdict := "**PASS**"
		if !ni.Pass {
			verdict = "**FAIL**"
		}
		fmt.Fprintf(&b, "| %s | %d | %+.1f pp | [%+.1f, %+.1f] pp | %.1f pp | %d | %s |\n",
			ni.Treatment, ni.Tasks, ni.MeanDelta*100, ni.CILow*100, ni.CIHigh*100,
			-ni.Margin*100, ni.Regressions, verdict)
	}
	b.WriteString("\n## Cascade early stop\n\n")
	fmt.Fprintf(&b, "- runs: %d\n- early stops: %d (%.1f%%)\n- early-stop pass rate: %.1f%%\n- escalations: %d (%.1f%%)\n",
		r.EarlyStop.Runs, r.EarlyStop.EarlyStops, r.EarlyStop.StopRate*100,
		r.EarlyStop.EarlyStopPassRate*100, r.EarlyStop.Escalations, r.EarlyStop.EscalationRate*100)
	b.WriteString("\n## Tasks\n\n")
	b.WriteString("| task | runs | single | cascade | full | cascade losses | full losses |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|\n")
	for _, t := range r.Tasks {
		fmt.Fprintf(&b, "| %s | %d | %.1f%% | %.1f%% | %.1f%% | %d | %d |\n",
			t.Task, t.Runs, t.SingleRate*100, t.CascadeRate*100, t.FullRate*100,
			t.CascadeLossRuns, t.FullLossRuns)
	}
	return b.String()
}

// runPairedReport is the `paired-report` subcommand.
func runPairedReport(args []string) {
	fs := flag.NewFlagSet("paired-report", flag.ExitOnError)
	outDir := fs.String("out", "", "results tree containing one or more results.json files")
	generator := fs.String("generator", "", "generator label to include (empty = all)")
	suite := fs.String("suite", "", "suite to include (empty = all)")
	margin := fs.Float64("margin-points", 5, "non-inferiority margin in percentage points")
	maxLoss := fs.Int("max-task-loss-runs", 2, "a task with this many losses is a regression")
	iterations := fs.Int("iterations", 10000, "bootstrap resamples")
	seed := fs.Int64("seed", 1, "bootstrap seed")
	jsonOut := fs.String("json", "", "write the report as JSON to this path")
	mdOut := fs.String("md", "", "write the report as Markdown to this path")
	_ = fs.Parse(args)
	if *outDir == "" {
		fmt.Fprintln(os.Stderr, "paired-report: -out is required")
		os.Exit(2)
	}
	metrics, err := loadAllMetrics(*outDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "paired-report:", err)
		os.Exit(1)
	}
	report := buildPairedReport(metrics, pairedOptions{
		Generator:       *generator,
		Suite:           *suite,
		MarginPoints:    *margin,
		MaxTaskLossRuns: *maxLoss,
		Iterations:      *iterations,
		Seed:            *seed,
	})
	if *jsonOut != "" {
		if err := writeJSON(*jsonOut, report); err != nil {
			fmt.Fprintln(os.Stderr, "paired-report: write JSON:", err)
			os.Exit(1)
		}
	}
	md := renderPairedMarkdown(report)
	if *mdOut != "" {
		if err := os.WriteFile(*mdOut, []byte(md), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "paired-report: write Markdown:", err)
			os.Exit(1)
		}
	} else {
		fmt.Print(md)
	}
}

// jsonShim keeps encoding/json imported for the JSON report shape; the
// helper is used by tests that compare the serialized report.
func pairedJSON(r pairedReport) ([]byte, error) { return json.MarshalIndent(r, "", "  ") }
