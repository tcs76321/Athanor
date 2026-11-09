// Quality delta report for benchmark v3 (ADR-0133). It turns the raw
// two-judge scores into the pre-registered quality claim: full vs single on
// the quality suite, per generator, with a task-clustered interval.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

type qualityDelta struct {
	Baseline   string  `json:"baseline"`
	Treatment  string  `json:"treatment"`
	Tasks      int     `json:"tasks"`
	Pairs      int     `json:"pairs"`
	MeanDelta  float64 `json:"mean_delta"`
	CILow      float64 `json:"ci_low"`
	CIHigh     float64 `json:"ci_high"`
	WinRate    float64 `json:"win_rate"`
	MinMean    float64 `json:"min_mean"`
	MinCILower float64 `json:"min_ci_lower"`
	MinWinRate float64 `json:"min_win_rate"`
	Pass       bool    `json:"pass"`
}

type qualityGeneratorDelta struct {
	Generator  string       `json:"generator"`
	Tasks      int          `json:"tasks"`
	SingleMean float64      `json:"single_mean"`
	FullMean   float64      `json:"full_mean"`
	Delta      qualityDelta `json:"delta"`
}

type qualityDeltaReport struct {
	Judges     []string                `json:"judges"`
	Agreement  float64                 `json:"agreement"`
	Generators []qualityGeneratorDelta `json:"generators"`
}

type qualityCell struct {
	single, full float64
	hasSingle    bool
	hasFull      bool
}

// buildQualityDelta joins the two judges per artifact (mean of successful
// scores), then pairs single and full by (generator, task, run). The task-level
// mean is the bootstrap unit so a task with more runs cannot dominate.
func buildQualityDelta(raw qualityReport, minMean, minCILower, minWinRate float64, iterations int, seed int64) qualityDeltaReport {
	type artifactKey struct {
		model, goal, arm string
		run              int
	}
	artifactScores := map[artifactKey][]float64{}
	for _, s := range raw.Scores {
		if s.Error != "" {
			continue
		}
		k := artifactKey{s.ModelLabel, s.GoalName, s.Arm, s.Run}
		artifactScores[k] = append(artifactScores[k], s.Overall)
	}
	meanOf := func(xs []float64) float64 {
		if len(xs) == 0 {
			return 0
		}
		sum := 0.0
		for _, x := range xs {
			sum += x
		}
		return sum / float64(len(xs))
	}
	// model -> goal -> run -> cell
	cells := map[string]map[string]map[int]*qualityCell{}
	modelSingle := map[string][]float64{}
	modelFull := map[string][]float64{}
	for k, xs := range artifactScores {
		v := meanOf(xs)
		if cells[k.model] == nil {
			cells[k.model] = map[string]map[int]*qualityCell{}
		}
		if cells[k.model][k.goal] == nil {
			cells[k.model][k.goal] = map[int]*qualityCell{}
		}
		c := cells[k.model][k.goal][k.run]
		if c == nil {
			c = &qualityCell{}
			cells[k.model][k.goal][k.run] = c
		}
		switch k.arm {
		case "single":
			c.single, c.hasSingle = v, true
			modelSingle[k.model] = append(modelSingle[k.model], v)
		case "full":
			c.full, c.hasFull = v, true
			modelFull[k.model] = append(modelFull[k.model], v)
		}
	}

	models := make([]string, 0, len(cells))
	for m := range cells {
		models = append(models, m)
	}
	sort.Strings(models)
	report := qualityDeltaReport{Judges: raw.Judges, Agreement: raw.Agreement}
	for _, model := range models {
		// Task-level deltas: average the per-run deltas inside a task.
		taskDeltas := map[string][]float64{}
		pairs, wins := 0, 0
		for goal, runs := range cells[model] {
			for _, c := range runs {
				if !c.hasSingle || !c.hasFull {
					continue
				}
				pairs++
				d := c.full - c.single
				if d > 0 {
					wins++
				}
				taskDeltas[goal] = append(taskDeltas[goal], d)
			}
		}
		var values []float64
		for _, ds := range taskDeltas {
			values = append(values, meanOf(ds))
		}
		mean, lo, hi := bootstrapMeanCI(values, iterations, seed)
		winRate := 0.0
		if pairs > 0 {
			winRate = float64(wins) / float64(pairs)
		}
		d := qualityDelta{
			Baseline: "single", Treatment: "full",
			Tasks: len(values), Pairs: pairs,
			MeanDelta: mean, CILow: lo, CIHigh: hi, WinRate: winRate,
			MinMean: minMean, MinCILower: minCILower, MinWinRate: minWinRate,
			Pass: mean >= minMean && lo >= minCILower && winRate >= minWinRate,
		}
		report.Generators = append(report.Generators, qualityGeneratorDelta{
			Generator: model, Tasks: len(values),
			SingleMean: meanOf(modelSingle[model]), FullMean: meanOf(modelFull[model]),
			Delta: d,
		})
	}
	return report
}

func renderQualityDelta(r qualityDeltaReport) string {
	var b strings.Builder
	b.WriteString("# v3 quality delta (full vs single)\n\n")
	fmt.Fprintf(&b, "Judges: %s  \nJudge agreement: %.3f\n\n", strings.Join(r.Judges, ", "), r.Agreement)
	b.WriteString("| generator | tasks | pairs | single | full | mean Δ | 95% CI | win rate | claim |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---|---:|---|\n")
	for _, g := range r.Generators {
		verdict := "**PASS**"
		if !g.Delta.Pass {
			verdict = "**FAIL**"
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %.2f | %.2f | %+.2f | [%+.2f, %+.2f] | %.1f%% | %s |\n",
			g.Generator, g.Tasks, g.Delta.Pairs, g.SingleMean, g.FullMean,
			g.Delta.MeanDelta, g.Delta.CILow, g.Delta.CIHigh, g.Delta.WinRate*100, verdict)
	}
	if len(r.Generators) == 0 {
		b.WriteString("\n_No paired quality artifacts were scored._\n")
		return b.String()
	}
	b.WriteString("\nThresholds: mean Δ ≥ ")
	fmt.Fprintf(&b, "%.2f, CI lower ≥ %.2f, win rate ≥ %.0f%%.\n",
		r.Generators[0].Delta.MinMean, r.Generators[0].Delta.MinCILower, r.Generators[0].Delta.MinWinRate*100)
	return b.String()
}

func runQualityReport(args []string) {
	fs := flag.NewFlagSet("quality-report", flag.ExitOnError)
	scoresPath := fs.String("scores", "quality-scores.json", "quality-scores.json from the quality subcommand")
	jsonOut := fs.String("json", "", "write the delta report as JSON")
	mdOut := fs.String("md", "", "write the delta report as Markdown")
	minMean := fs.Float64("min-mean", 0.5, "minimum mean rubric delta")
	minCILower := fs.Float64("min-ci-lower", 0.2, "minimum 95% CI lower bound")
	minWin := fs.Float64("min-win-rate", 0.6, "minimum fraction of pairs where full > single")
	minAgreement := fs.Float64("min-agreement", 0.6, "minimum inter-judge Spearman agreement")
	iterations := fs.Int("iterations", 10000, "bootstrap resamples")
	seed := fs.Int64("seed", 1, "bootstrap seed")
	_ = fs.Parse(args)

	raw, err := os.ReadFile(*scoresPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "quality-report:", err)
		os.Exit(1)
	}
	var scores qualityReport
	if err := json.Unmarshal(raw, &scores); err != nil {
		fmt.Fprintln(os.Stderr, "quality-report:", err)
		os.Exit(1)
	}
	report := buildQualityDelta(scores, *minMean, *minCILower, *minWin, *iterations, *seed)
	if *jsonOut != "" {
		if err := writeJSON(*jsonOut, report); err != nil {
			fmt.Fprintln(os.Stderr, "quality-report: write JSON:", err)
			os.Exit(1)
		}
	}
	md := renderQualityDelta(report)
	if *mdOut != "" {
		if err := os.WriteFile(*mdOut, []byte(md), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "quality-report: write Markdown:", err)
			os.Exit(1)
		}
	} else {
		fmt.Print(md)
	}
	if report.Agreement > 0 && report.Agreement < *minAgreement {
		fmt.Fprintf(os.Stderr, "quality-report: judge agreement %.3f is below the %.2f floor; the quality claim is not supported\n",
			report.Agreement, *minAgreement)
		os.Exit(1)
	}
}
