// The `bundle` subcommand writes a human-readable, labeled view of every
// collected artifact so an operator can read and copy/paste results. The
// `packets/` output stays blind for the online judge; this is the labeled
// companion (goal, arm, run, scores, verbatim artifact).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// runBundle is the `bundle` subcommand.
func runBundle(args []string) {
	fs := flag.NewFlagSet("bundle", flag.ExitOnError)
	outDir := fs.String("out", filepath.Join("spikes", "m3-t7-probe", "results"), "results directory")
	_ = fs.Parse(args)

	metrics, err := loadAllMetrics(*outDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "m3-t7 bundle:", err)
		os.Exit(1)
	}
	if len(metrics) == 0 {
		fmt.Fprintf(os.Stderr, "m3-t7 bundle: no results.json under %s\n", *outDir)
		os.Exit(1)
	}
	dest := filepath.Join(*outDir, "artifacts.md")
	if err := os.WriteFile(dest, []byte(renderBundle(*outDir, metrics)), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "m3-t7 bundle:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s\n", dest)
}

// renderBundle renders every collected artifact, labeled and grouped by goal
// then arm/run. It re-reads artifact bytes from each arm's state dir
// (results.json deliberately does not embed artifact text).
func renderBundle(outDir string, metrics []jobMetrics) string {
	byGoal := make(map[string][]jobMetrics)
	var order []string
	for _, m := range metrics {
		if _, ok := byGoal[m.GoalName]; !ok {
			order = append(order, m.GoalName)
		}
		byGoal[m.GoalName] = append(byGoal[m.GoalName], m)
	}
	sort.Strings(order)

	var b strings.Builder
	b.WriteString("# M3-T7 artifact bundle\n\n")
	b.WriteString("Labeled view of every artifact (the online judge packets stay blind).\n\n")
	for _, goal := range order {
		g, _ := goalByName(goal)
		fmt.Fprintf(&b, "## %s (%s)\n\n", goal, g.Archetype)
		fmt.Fprintf(&b, "Goal: %s\n\nCriteria: %s\n\n", g.Goal, strings.Join(g.Criteria, "; "))
		rows := byGoal[goal]
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Arm != rows[j].Arm {
				return rows[i].Arm < rows[j].Arm
			}
			return rows[i].Run < rows[j].Run
		})
		for _, m := range rows {
			stateDir := filepath.Join(outDir, m.ModelLabel, m.Arm, "state")
			text, _ := readArtifact(stateDir, m.ArtifactID)
			fmt.Fprintf(&b, "### %s run %d — %s, winner=%s, score=%.2f, diversity=%.2f, tokens=%d\n\n",
				m.Arm, m.Run, m.State, dash(m.Winner), m.Score, m.Diversity, m.TokenCost)
			if m.Error != "" {
				fmt.Fprintf(&b, "_error: %s_\n\n", m.Error)
			}
			if text == "" {
				b.WriteString("_(no artifact captured)_\n\n")
				continue
			}
			b.WriteString("```\n")
			b.WriteString(text)
			if !strings.HasSuffix(text, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("```\n\n")
		}
	}
	return b.String()
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
