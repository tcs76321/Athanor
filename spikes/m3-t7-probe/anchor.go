// anchor.go implements the F4-T4 human-anchor calibration pass. It scores
// the eval/anchor cases with the third-party judge models and reports, per
// judge, the reliability (success rate) and the Spearman rank agreement with
// the human ratings. A judge below either floor is reported as not
// trustworthy rather than silently averaged in.
//
// It is a separate subcommand so it can run without regenerating anything:
// the anchor set and its ratings are committed, and only the judge models
// need to be present.
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tcs76321/athanor/internal/judge"
)

// anchorCase is one rated artifact.
type anchorCase struct {
	ID       string
	Goal     string
	Criteria string
	Artifact string
}

// parseAnchorCases reads eval/anchor/cases.md. Format: `## <id>`, then
// `- **Goal:** ...` and `- **Criteria:** ...`, then a `~~~~`-fenced artifact.
func parseAnchorCases(path string) ([]anchorCase, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(raw), "\n")
	var out []anchorCase
	var cur *anchorCase
	var buf []string
	inFence := false
	flush := func() {
		if cur != nil {
			cur.Artifact = strings.TrimSpace(strings.Join(buf, "\n"))
			out = append(out, *cur)
		}
		cur, buf, inFence = nil, nil, false
	}
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "~~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			buf = append(buf, ln)
			continue
		}
		if strings.HasPrefix(t, "## ") {
			flush()
			cur = &anchorCase{ID: strings.TrimSpace(strings.TrimPrefix(t, "## "))}
			continue
		}
		if cur == nil {
			continue
		}
		if strings.HasPrefix(t, "- **Goal:**") {
			cur.Goal = strings.TrimSpace(strings.TrimPrefix(t, "- **Goal:**"))
			continue
		}
		if strings.HasPrefix(t, "- **Criteria:**") {
			cur.Criteria = strings.TrimSpace(strings.TrimPrefix(t, "- **Criteria:**"))
		}
	}
	flush()
	if len(out) == 0 {
		return nil, fmt.Errorf("no anchor cases parsed from %s", path)
	}
	return out, nil
}

// parseAnchorRatings reads ratings.csv into case_id → rating. A non-agent
// rater wins over the provisional agent rows; multiple humans average.
func parseAnchorRatings(path string) (map[string]float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, err
	}
	type acc struct {
		sum   float64
		n     int
		human bool
	}
	byID := map[string]*acc{}
	for i, row := range rows {
		if i == 0 || len(row) < 3 { // header
			continue
		}
		id, rater := strings.TrimSpace(row[0]), strings.TrimSpace(row[1])
		rating, err := strconv.ParseFloat(strings.TrimSpace(row[2]), 64)
		if err != nil {
			return nil, fmt.Errorf("row %d: bad rating %q", i+1, row[2])
		}
		a := byID[id]
		if a == nil {
			a = &acc{}
			byID[id] = a
		}
		human := rater != "agent"
		if human && !a.human {
			// A human row resets the provisional agent rows.
			a.sum, a.n, a.human = 0, 0, true
		}
		if human != a.human {
			continue
		}
		a.sum += rating
		a.n++
	}
	out := map[string]float64{}
	for id, a := range byID {
		if a.n > 0 {
			out[id] = a.sum / float64(a.n)
		}
	}
	return out, nil
}

// anchorPacket renders the blind rating packet for one case.
func anchorPacket(c anchorCase) string {
	return fmt.Sprintf(`You are an expert reviewer rating a single artifact produced for a task.
Rate the artifact's overall quality from 1 to 5, where 1 = fails the criteria
and 5 = fully meets the criteria with no defect. Be discriminating: use the
whole range, and do not give every artifact the same score.

TASK GOAL: %s

ACCEPTANCE CRITERIA: %s

ARTIFACT:
%s

Output JSON only: {"score": <1-5>, "criteria_met": [...], "criteria_missing": [...], "notes": "<one line>"}`,
		c.Goal, c.Criteria, c.Artifact)
}

// anchorRow is one judge's score for one anchor case.
type anchorRow struct {
	CaseID string  `json:"case_id"`
	Judge  string  `json:"judge"`
	Score  float64 `json:"score"`
	Error  string  `json:"error,omitempty"`
}

// runAnchor is the `anchor` subcommand.
func runAnchor(args []string) {
	fs := flag.NewFlagSet("anchor", flag.ExitOnError)
	dir := fs.String("dir", filepath.Join("eval", "anchor"), "anchor directory")
	outDir := fs.String("out", filepath.Join("spikes", "m3-t7-probe", "results", "anchor"), "output directory")
	ollamaURL := fs.String("ollama", "http://localhost:11434", "Ollama base URL")
	judgesCSV := fs.String("judges", strings.Join(judgeModels, ","), "comma-separated judge models")
	minSuccess := fs.Float64("min-success", 0.8, "fail if a judge's call success rate is below this")
	minAgreement := fs.Float64("min-agreement", 0.6, "fail if a judge's rank agreement is below this")
	_ = fs.Parse(args)

	cases, err := parseAnchorCases(filepath.Join(*dir, "cases.md"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "anchor:", err)
		os.Exit(1)
	}
	ratings, err := parseAnchorRatings(filepath.Join(*dir, "ratings.csv"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "anchor:", err)
		os.Exit(1)
	}
	base := strings.TrimRight(*ollamaURL, "/")
	judges := splitCSV(*judgesCSV)

	var rows []anchorRow
	for _, c := range cases {
		packet := anchorPacket(c)
		for _, j := range judges {
			row := anchorRow{CaseID: c.ID, Judge: j}
			jr, jerr := callJudge(base, j, packet, judgeSeed("anchor-"+c.ID, j))
			if jerr != nil {
				row.Error = jerr.Error()
			} else {
				row.Score = jr.Score
			}
			rows = append(rows, row)
		}
		fmt.Printf("anchored %s with %d judges\n", c.ID, len(judges))
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "anchor:", err)
		os.Exit(1)
	}
	if err := writeJSON(filepath.Join(*outDir, "anchor.json"), rows); err != nil {
		fmt.Fprintln(os.Stderr, "anchor:", err)
		os.Exit(1)
	}

	fail := false
	fmt.Printf("%-16s %8s %8s %10s\n", "judge", "ok", "rate", "spearman")
	for _, j := range judges {
		var samples []judge.Sample
		ok, total := 0, 0
		for _, r := range rows {
			if r.Judge != j {
				continue
			}
			total++
			if r.Error != "" {
				continue
			}
			ok++
			if rating, has := ratings[r.CaseID]; has {
				samples = append(samples, judge.Sample{AnchorRating: rating, JudgeScore: r.Score})
			}
		}
		rate, rho := 0.0, 0.0
		if total > 0 {
			rate = float64(ok) / float64(total)
		}
		rho = judge.Spearman(samples)
		fmt.Printf("%-16s %8d %7.0f%% %10.2f\n", j, ok, rate*100, rho)
		if total > 0 && rate < *minSuccess {
			fmt.Fprintf(os.Stderr, "anchor: judge %s success rate %.0f%% below %.0f%%\n", j, rate*100, *minSuccess*100)
			fail = true
		}
		if !judge.MeetsAgreement(samples, *minAgreement) {
			fmt.Fprintf(os.Stderr, "anchor: judge %s agreement %.2f below %.2f\n", j, rho, *minAgreement)
			fail = true
		}
	}
	if fail {
		os.Exit(1)
	}
}
