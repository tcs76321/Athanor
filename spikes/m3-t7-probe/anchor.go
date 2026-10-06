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
	"bytes"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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

// anchorPacket100 is the finer-granularity pointwise packet (F4 follow-up
// C1): the TrustJudge finding is that a 5-point scale loses the information
// needed to discriminate.
func anchorPacket100(c anchorCase) string {
	return fmt.Sprintf(`You are an expert reviewer rating a single artifact produced for a task.
Rate the artifact's overall quality from 0 to 100, where 0 = fails the
criteria and 100 = fully meets the criteria with no defect. Be discriminating:
use the whole range, and do not give every artifact the same score.

TASK GOAL: %s

ACCEPTANCE CRITERIA: %s

ARTIFACT:
%s

Output JSON only: {"score": <0-100>, "criteria_met": [...], "criteria_missing": [...], "notes": "<one line>"}`,
		c.Goal, c.Criteria, c.Artifact)
}

// anchorPairPacket renders an A/B comparison packet (F4 follow-up C1). The
// caller runs it in both orders to expose position bias.
func anchorPairPacket(a, b anchorCase) string {
	return fmt.Sprintf(`You are comparing two artifacts (A and B) produced for the same task and
deciding which is better against the acceptance criteria. Be discriminating.
If they are genuinely equally good, answer TIE.

TASK GOAL: %s

ACCEPTANCE CRITERIA: %s

ARTIFACT A:
%s

ARTIFACT B:
%s

Output JSON only: {"winner": "A"|"B"|"TIE", "notes": "<one line>"}`,
		a.Goal, a.Criteria, a.Artifact, b.Artifact)
}

// callAnchorPair asks one judge to compare A and B, retrying on failure.
func callAnchorPair(baseURL, model, packet string, seed int64) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req := ollamaChatRequest{
			Model:    model,
			Messages: []map[string]string{{"role": "user", "content": packet}},
			Stream:   false,
			Options:  map[string]any{"temperature": 0.0, "seed": seed, "num_predict": 512},
		}
		if attempt%2 == 0 {
			req.Format = "json"
		}
		body, err := json.Marshal(req)
		if err != nil {
			return "", err
		}
		client := &http.Client{Timeout: 3 * time.Minute}
		resp, err := client.Post(baseURL+"/api/chat", "application/json", bytes.NewReader(body))
		if err != nil {
			lastErr = err
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("ollama returned %s: %s", resp.Status, truncate(string(raw), 200))
			continue
		}
		var cr ollamaChatResponse
		if err := json.Unmarshal(raw, &cr); err != nil {
			lastErr = err
			continue
		}
		obj, ok := firstJSONObject(cr.Message.Content)
		if !ok {
			lastErr = fmt.Errorf("no JSON object in pair response: %q", truncate(cr.Message.Content, 120))
			continue
		}
		var m struct {
			Winner string `json:"winner"`
		}
		if err := json.Unmarshal([]byte(obj), &m); err != nil {
			lastErr = err
			continue
		}
		switch w := strings.ToUpper(strings.TrimSpace(m.Winner)); w {
		case "A", "B", "TIE":
			return w, nil
		default:
			lastErr = fmt.Errorf("unexpected winner %q", m.Winner)
		}
	}
	return "", lastErr
}

// reconcilePair maps the forward and reverse winners to one verdict. forward
// is over (A=first, B=second); reverse is over (A=second, B=first).
func reconcilePair(forward, reverse string) string {
	rev := map[string]string{"A": "B", "B": "A", "TIE": "TIE"}[reverse]
	switch {
	case forward == "TIE" && rev == "TIE":
		return "TIE"
	case forward == "TIE":
		return rev
	case rev == "TIE":
		return forward
	case forward == rev:
		return forward
	default:
		return "INCONSISTENT"
	}
}

// pairRow is one judge's comparison of two anchor cases.
type pairRow struct {
	CaseA  string `json:"case_a"`
	CaseB  string `json:"case_b"`
	Judge  string `json:"judge"`
	Winner string `json:"winner,omitempty"` // A | B | TIE | INCONSISTENT
	Error  string `json:"error,omitempty"`
}

// runAnchor is the `anchor` subcommand.
func runAnchor(args []string) {
	fs := flag.NewFlagSet("anchor", flag.ExitOnError)
	dir := fs.String("dir", filepath.Join("eval", "anchor"), "anchor directory")
	outDir := fs.String("out", filepath.Join("spikes", "m3-t7-probe", "results", "anchor"), "output directory")
	ollamaURL := fs.String("ollama", "http://localhost:11434", "Ollama base URL")
	judgesCSV := fs.String("judges", strings.Join(judgeModels, ","), "comma-separated judge models")
	minSuccess := fs.Float64("min-success", 0.8, "fail if a judge's call success rate is below this")
	minAgreement := fs.Float64("min-agreement", 0.6, "fail if a judge's agreement is below this")
	protocol := fs.String("protocol", "pointwise5", "pointwise5 | pointwise100 | pairwise")
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
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "anchor:", err)
		os.Exit(1)
	}
	base := strings.TrimRight(*ollamaURL, "/")
	judges := splitCSV(*judgesCSV)

	switch *protocol {
	case "pointwise5", "pointwise100":
		runAnchorPointwise(base, *protocol, judges, cases, ratings, *outDir, *minSuccess, *minAgreement)
	case "pairwise":
		runAnchorPairwise(base, judges, cases, ratings, *outDir, *minSuccess, *minAgreement)
	default:
		fmt.Fprintf(os.Stderr, "anchor: unknown protocol %q\n", *protocol)
		os.Exit(2)
	}
}

// runAnchorPointwise runs the absolute-scoring protocol and reports
// reliability, Spearman, and Kendall tau.
func runAnchorPointwise(base, protocol string, judges []string, cases []anchorCase,
	ratings map[string]float64, outDir string, minSuccess, minAgreement float64) {

	packetFor := anchorPacket
	if protocol == "pointwise100" {
		packetFor = anchorPacket100
	}
	var rows []anchorRow
	for _, c := range cases {
		packet := packetFor(c)
		for _, j := range judges {
			row := anchorRow{CaseID: c.ID, Judge: j}
			jr, jerr := callJudge(base, j, packet, judgeSeed("anchor-"+protocol+"-"+c.ID, j))
			if jerr != nil {
				row.Error = jerr.Error()
			} else {
				row.Score = jr.Score
			}
			rows = append(rows, row)
		}
		fmt.Printf("anchored %s with %d judges\n", c.ID, len(judges))
	}
	if err := writeJSON(filepath.Join(outDir, "anchor.json"), rows); err != nil {
		fmt.Fprintln(os.Stderr, "anchor:", err)
		os.Exit(1)
	}

	fail := false
	fmt.Printf("%-16s %8s %8s %10s %10s\n", "judge", "ok", "rate", "spearman", "kendall")
	for _, j := range judges {
		var samples []judge.Sample
		var xs, ys []float64
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
				xs = append(xs, rating)
				ys = append(ys, r.Score)
			}
		}
		rate := 0.0
		if total > 0 {
			rate = float64(ok) / float64(total)
		}
		rho := judge.Spearman(samples)
		tau := judge.KendallTau(xs, ys)
		fmt.Printf("%-16s %8d %7.0f%% %10.2f %10.2f\n", j, ok, rate*100, rho, tau)
		if total > 0 && rate < minSuccess {
			fmt.Fprintf(os.Stderr, "anchor: judge %s success rate %.0f%% below %.0f%%\n", j, rate*100, minSuccess*100)
			fail = true
		}
		if !judge.MeetsAgreement(samples, minAgreement) {
			fmt.Fprintf(os.Stderr, "anchor: judge %s agreement %.2f below %.2f\n", j, rho, minAgreement)
			fail = true
		}
	}
	if fail {
		os.Exit(1)
	}
}

// runAnchorPairwise runs the A/B protocol in both orders and reports
// reliability and pair agreement.
func runAnchorPairwise(base string, judges []string, cases []anchorCase,
	ratings map[string]float64, outDir string, minSuccess, minAgreement float64) {

	var rows []pairRow
	for i := 0; i < len(cases); i++ {
		for j := i + 1; j < len(cases); j++ {
			ra, okA := ratings[cases[i].ID]
			rb, okB := ratings[cases[j].ID]
			if !okA || !okB || ra == rb {
				continue
			}
			for _, jd := range judges {
				row := pairRow{CaseA: cases[i].ID, CaseB: cases[j].ID, Judge: jd}
				fwd, e1 := callAnchorPair(base, jd, anchorPairPacket(cases[i], cases[j]),
					judgeSeed("pair-f-"+cases[i].ID+"-"+cases[j].ID, jd))
				rev, e2 := callAnchorPair(base, jd, anchorPairPacket(cases[j], cases[i]),
					judgeSeed("pair-r-"+cases[j].ID+"-"+cases[i].ID, jd))
				switch {
				case e1 != nil:
					row.Error = e1.Error()
				case e2 != nil:
					row.Error = e2.Error()
				default:
					row.Winner = reconcilePair(fwd, rev)
				}
				rows = append(rows, row)
			}
		}
	}
	if err := writeJSON(filepath.Join(outDir, "pairs.json"), rows); err != nil {
		fmt.Fprintln(os.Stderr, "anchor:", err)
		os.Exit(1)
	}

	fail := false
	fmt.Printf("%-16s %8s %8s %10s %10s\n", "judge", "ok", "rate", "pair-acc", "dedup-acc")
	for _, j := range judges {
		ok, total := 0, 0
		var votes []judge.PairVote
		for _, r := range rows {
			if r.Judge != j {
				continue
			}
			total++
			if r.Error != "" {
				continue
			}
			ok++
			if r.Winner == "TIE" || r.Winner == "INCONSISTENT" {
				continue
			}
			better := ratings[r.CaseA] > ratings[r.CaseB]
			votes = append(votes, judge.PairVote{AnchorABetter: better, JudgeChoseA: r.Winner == "A"})
		}
		rate := 0.0
		if total > 0 {
			rate = float64(ok) / float64(total)
		}
		acc := judge.PairAgreement(votes)
		fmt.Printf("%-16s %8d %7.0f%% %10.2f %10s\n", j, ok, rate*100, acc, "-")
		if total > 0 && rate < minSuccess {
			fmt.Fprintf(os.Stderr, "anchor: judge %s success rate %.0f%% below %.0f%%\n", j, rate*100, minSuccess*100)
			fail = true
		}
		if acc < minAgreement {
			fmt.Fprintf(os.Stderr, "anchor: judge %s pair agreement %.2f below %.2f\n", j, acc, minAgreement)
			fail = true
		}
	}
	if fail {
		os.Exit(1)
	}
}
