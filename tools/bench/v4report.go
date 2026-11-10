// v4 paired report (ADR-0135): joins the one-shot baseline rows with the cycle
// arm's scored rows by task and reports the pre-registered utility claims.
//
// The baseline is the existing soak runner's results.json (one engine task per
// goal). The cycle arm is the cycle probe's cycle-<model>.json. Both are
// deterministic labels; engine acceptance and judge scores are reported
// separately, never as the headline.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type v4ReportInput struct {
	baseline []jobMetrics
	cycle    cycleResults
	model    string
	margin   float64
}

type v4TaskRow struct {
	Task       string   `json:"task"`
	Baseline   bool     `json:"baseline_pass"`
	Cycle      bool     `json:"cycle_pass"`
	CycleState string   `json:"cycle_state"`
	Citation   bool     `json:"citation_pass"`
	Failures   []string `json:"cycle_failures,omitempty"`
}

type v4Report struct {
	Model            string      `json:"model"`
	Tasks            []v4TaskRow `json:"tasks"`
	BaselinePassRate float64     `json:"baseline_pass_rate"`
	CyclePassRate    float64     `json:"cycle_pass_rate"`
	CitationPassRate float64     `json:"citation_pass_rate"`
	DeltaPoints      float64     `json:"delta_points"`
	MarginPoints     float64     `json:"margin_points"`
	NonInferior      bool        `json:"non_inferior"`
	Sufficient       bool        `json:"claim_sufficient"`
	Note             string      `json:"note"`
}

func runV4Report(args []string) {
	fs := flag.NewFlagSet("v4-report", flag.ExitOnError)
	baseline := fs.String("baseline", "", "baseline results.json (one-shot arm)")
	cycleFile := fs.String("cycle", "", "cycle-<model>.json (cycle arm)")
	model := fs.String("model", "bonsai27b", "generator label")
	margin := fs.Float64("margin", 5, "non-inferiority margin in percentage points")
	jsonOut := fs.String("json", "", "write the report JSON here")
	mdOut := fs.String("md", "", "write the report Markdown here")
	_ = fs.Parse(args)

	if *baseline == "" || *cycleFile == "" {
		fatal("v4-report: -baseline and -cycle are required")
	}
	baseRows, err := readBaseline(*baseline)
	if err != nil {
		fatal("v4-report: %v", err)
	}
	raw, err := os.ReadFile(*cycleFile)
	if err != nil {
		fatal("v4-report: %v", err)
	}
	var cr cycleResults
	if err := json.Unmarshal(raw, &cr); err != nil {
		fatal("v4-report: %v", err)
	}
	rep := buildV4Report(v4ReportInput{
		baseline: baseRows, cycle: cr, model: *model, margin: *margin,
	})
	if *jsonOut != "" {
		if err := writeJSONFile(*jsonOut, rep); err != nil {
			fatal("v4-report: %v", err)
		}
	}
	md := renderV4Report(rep)
	if *mdOut != "" {
		if err := os.WriteFile(*mdOut, []byte(md), 0o644); err != nil {
			fatal("v4-report: %v", err)
		}
	}
	fmt.Print(md)
}

func readBaseline(path string) ([]jobMetrics, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rows []jobMetrics
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return rows, nil
}

func buildV4Report(in v4ReportInput) v4Report {
	// The baseline may contain several runs per task; the last run is the
	// declared baseline. The cycle arm runs once per task.
	baseline := map[string]bool{}
	for _, row := range in.baseline {
		if row.Arm != "" && row.Arm != "single" {
			continue
		}
		if row.CheckPass != nil {
			baseline[row.GoalName] = *row.CheckPass
		} else if row.CodeTestPassed != nil {
			baseline[row.GoalName] = *row.CodeTestPassed
		}
	}
	rep := v4Report{
		Model: in.model, MarginPoints: in.margin,
		Note: "Directional until the v4 freeze; the brief seed is small.",
	}
	basePass, cyclePass, citePass := 0, 0, 0
	paired := 0
	for _, t := range in.cycle.Tasks {
		row := v4TaskRow{
			Task: t.ID, Cycle: t.Pass, CycleState: t.State, Failures: t.Failures,
			Citation: citationChecksPassed(t.Checks),
		}
		if b, ok := baseline[t.ID]; ok {
			row.Baseline = b
			paired++
			if b {
				basePass++
			}
		}
		if t.Pass {
			cyclePass++
		}
		if row.Citation {
			citePass++
		}
		rep.Tasks = append(rep.Tasks, row)
	}
	sort.SliceStable(rep.Tasks, func(i, j int) bool { return rep.Tasks[i].Task < rep.Tasks[j].Task })
	n := len(rep.Tasks)
	if n > 0 {
		rep.CyclePassRate = float64(cyclePass) / float64(n)
		rep.CitationPassRate = float64(citePass) / float64(n)
		rep.Sufficient = rep.CyclePassRate >= 0.75 && rep.CitationPassRate >= 0.75
	}
	if paired > 0 {
		rep.BaselinePassRate = float64(basePass) / float64(paired)
		rep.DeltaPoints = (rep.CyclePassRate - rep.BaselinePassRate) * 100
		rep.NonInferior = rep.DeltaPoints >= -in.margin
	} else {
		rep.Note += " No baseline rows joined."
	}
	return rep
}

func citationChecksPassed(results []checkResult) bool {
	if len(results) == 0 {
		return false
	}
	want := map[string]bool{"allowed_urls": false, "cite_every_paragraph": false, "must_contain": false}
	for _, r := range results {
		if _, ok := want[r.Kind]; ok && !r.Pass && !r.Advisory {
			return false
		}
		if _, ok := want[r.Kind]; ok {
			want[r.Kind] = true
		}
	}
	for _, seen := range want {
		if !seen {
			return false
		}
	}
	return true
}

func renderV4Report(rep v4Report) string {
	var b strings.Builder
	b.WriteString("# v4 paired utility report\n\n")
	fmt.Fprintf(&b, "- Model: `%s`\n", rep.Model)
	fmt.Fprintf(&b, "- Baseline pass rate: %.0f%%\n", rep.BaselinePassRate*100)
	fmt.Fprintf(&b, "- Cycle pass rate: %.0f%%\n", rep.CyclePassRate*100)
	fmt.Fprintf(&b, "- Citation pass rate: %.0f%%\n", rep.CitationPassRate*100)
	fmt.Fprintf(&b, "- Delta: %+.1f points (margin -%.1f)\n", rep.DeltaPoints, rep.MarginPoints)
	fmt.Fprintf(&b, "- Non-inferior: %v\n", rep.NonInferior)
	fmt.Fprintf(&b, "- Utility floor met: %v\n\n", rep.Sufficient)
	b.WriteString("| task | one-shot | cycle | citation | cycle state |\n|---|---|---|---|---|\n")
	for _, t := range rep.Tasks {
		fmt.Fprintf(&b, "| %s | %v | %v | %v | %s |\n", t.Task, t.Baseline, t.Cycle, t.Citation, t.CycleState)
	}
	b.WriteString("\n## Failures\n\n")
	any := false
	for _, t := range rep.Tasks {
		if len(t.Failures) == 0 {
			continue
		}
		any = true
		fmt.Fprintf(&b, "- **%s**: %s\n", t.Task, strings.Join(t.Failures, "; "))
	}
	if !any {
		b.WriteString("None.\n")
	}
	fmt.Fprintf(&b, "\n> %s\n", rep.Note)
	return b.String()
}

func writeJSONFile(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}
