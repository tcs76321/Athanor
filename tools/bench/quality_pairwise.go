// Pairwise quality judging for v5. The pointwise rubric is unreliable at the
// v5 corpus size; this pass asks two blind judges to compare the single and
// full artifacts for the same task in both orders, then reports agreement and
// a task-clustered win rate. It is a claim instrument, not a leaderboard.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

type pairwiseVote struct {
	Goal      string `json:"goal"`
	Run       int    `json:"run"`
	Judge     string `json:"judge"`
	Forward   string `json:"forward"` // single | full | tie | inconsistent
	Reverse   string `json:"reverse"` // single | full | tie | inconsistent
	Winner    string `json:"winner"`  // reconciled, in single/full terms
	ErrorText string `json:"error,omitempty"`
}

type pairwiseTask struct {
	Goal         string `json:"goal"`
	Pairs        int    `json:"pairs"`
	SingleWins   int    `json:"single_wins"`
	FullWins     int    `json:"full_wins"`
	Ties         int    `json:"ties"`
	Inconsistent int    `json:"inconsistent"`
}

type pairwisePair struct {
	Goal   string         `json:"goal"`
	Run    int            `json:"run"`
	Votes  []pairwiseVote `json:"votes"`
	Winner string         `json:"winner"` // majority in single/full terms, or tie
}

type pairwiseReport struct {
	Generators       []string          `json:"generators"`
	Judges           []string          `json:"judges"`
	JudgeDiagnostics []judgeDiagnostic `json:"judge_diagnostics,omitempty"`
	Pairs            []pairwisePair    `json:"pairs"`
	Tasks            []pairwiseTask    `json:"tasks"`
	SingleWins       int               `json:"single_wins"`
	FullWins         int               `json:"full_wins"`
	Ties             int               `json:"ties"`
	Decisive         int               `json:"decisive"`
	UsablePairs      int               `json:"usable_pairs"`
	MinUsablePairs   int               `json:"min_usable_pairs"`
	WinRate          float64           `json:"win_rate"`
	Agreement        float64           `json:"agreement"`
	SelfConsistency  float64           `json:"self_consistency"`
}

// judgeDiagnostic is the per-judge evidence behind the agreement number. A
// judge that always picks the first artifact is not measuring quality, even if
// its votes look decisive.
type judgeDiagnostic struct {
	Judge                 string  `json:"judge"`
	Votes                 int     `json:"votes"`
	Errors                int     `json:"errors"`
	SelfConsistency       float64 `json:"self_consistency"`
	OrderInconsistentRate float64 `json:"order_inconsistent_rate"`
	Agreement             float64 `json:"agreement"`
}

func runQualityPairwise(args []string) {
	fs := flag.NewFlagSet("quality-pairwise", flag.ExitOnError)
	singlePath := fs.String("single", "", "single-arm artifacts.json")
	fullPath := fs.String("full", "", "full-arm artifacts.json")
	corpusPath := fs.String("corpus", "eval/bench/v5/v5.yaml", "v5 corpus for task text")
	ollamaURL := fs.String("ollama", "http://localhost:11434", "Ollama base URL")
	judgesCSV := fs.String("judges", "granite4.2:8b,mimo-v2.6:9b-mxfp8", "comma-separated judges")
	outPath := fs.String("out", "quality-pairwise.json", "output JSON")
	minAgreement := fs.Float64("min-agreement", 0.6, "fail below this pair-agreement floor")
	minUsablePairs := fs.Int("min-usable-pairs", 3, "fail below this many usable pairs")
	_ = fs.Parse(args)
	if *singlePath == "" || *fullPath == "" {
		fmt.Fprintln(os.Stderr, "quality-pairwise: -single and -full are required")
		os.Exit(2)
	}
	single, err := loadArtifacts(*singlePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "quality-pairwise:", err)
		os.Exit(1)
	}
	full, err := loadArtifacts(*fullPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "quality-pairwise:", err)
		os.Exit(1)
	}
	goals, err := loadCorpus(*corpusPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "quality-pairwise:", err)
		os.Exit(1)
	}
	goalByID := map[string]sampleGoal{}
	for _, g := range goals {
		goalByID[g.Name] = g
	}
	byKey := func(rows []collectedArtifact) map[string]collectedArtifact {
		out := map[string]collectedArtifact{}
		for _, r := range rows {
			out[fmt.Sprintf("%s|%d", r.GoalName, r.Run)] = r
		}
		return out
	}
	singleBy := byKey(single)
	fullBy := byKey(full)
	keys := make([]string, 0, len(singleBy))
	for k := range singleBy {
		if _, ok := fullBy[k]; ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		fmt.Fprintln(os.Stderr, "quality-pairwise: no paired artifacts")
		os.Exit(1)
	}
	judges := splitCSV(*judgesCSV)
	base := strings.TrimRight(*ollamaURL, "/")
	report := pairwiseReport{Judges: judges}
	for _, key := range keys {
		s := singleBy[key]
		f := fullBy[key]
		goalspec, ok := goalByID[s.GoalName]
		if !ok {
			continue
		}
		pair := pairwisePair{Goal: s.GoalName, Run: s.Run}
		for _, judge := range judges {
			fwd, err := pairwiseOnce(base, judge, goalspec, s.Text, f.Text, false)
			if err != nil {
				pair.Votes = append(pair.Votes, pairwiseVote{Goal: s.GoalName, Run: s.Run, Judge: judge, ErrorText: err.Error()})
				continue
			}
			rev, err := pairwiseOnce(base, judge, goalspec, s.Text, f.Text, true)
			if err != nil {
				pair.Votes = append(pair.Votes, pairwiseVote{Goal: s.GoalName, Run: s.Run, Judge: judge, Forward: fwd, ErrorText: err.Error()})
				continue
			}
			vote := pairwiseVote{Goal: s.GoalName, Run: s.Run, Judge: judge,
				Forward: fwd, Reverse: rev, Winner: reconcilePairWinner(fwd, rev)}
			pair.Votes = append(pair.Votes, vote)
			fmt.Printf("  %-24s run %d %-20s %s\n", s.GoalName, s.Run, judge, vote.Winner)
		}
		report.Pairs = append(report.Pairs, pair)
	}
	// Collapse each pair's judges into one majority winner.
	for i := range report.Pairs {
		seen := map[string]int{}
		for _, v := range report.Pairs[i].Votes {
			if v.Winner != "" && v.Winner != "inconsistent" {
				seen[v.Winner]++
			}
		}
		switch {
		case seen["full"] > seen["single"]:
			report.Pairs[i].Winner = "full"
			report.FullWins++
			report.Decisive++
		case seen["single"] > seen["full"]:
			report.Pairs[i].Winner = "single"
			report.SingleWins++
			report.Decisive++
		default:
			report.Pairs[i].Winner = "tie"
			report.Ties++
		}
	}
	report.Tasks = pairwiseTasks(report.Pairs)
	report.WinRate = 0
	if report.Decisive > 0 {
		report.WinRate = float64(report.FullWins) / float64(report.Decisive)
	}
	report.MinUsablePairs = *minUsablePairs
	report.Agreement, report.SelfConsistency, report.UsablePairs, report.JudgeDiagnostics =
		pairwiseDiagnostics(report.Pairs, judges)
	report.Generators = pairwiseGenerators(single)
	if err := writeJSON(*outPath, report); err != nil {
		fmt.Fprintln(os.Stderr, "quality-pairwise: write:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d pairs, %d usable, full win rate %.2f, agreement %.2f)\n",
		*outPath, len(report.Pairs), report.UsablePairs, report.WinRate, report.Agreement)
	if report.UsablePairs < *minUsablePairs {
		fmt.Fprintf(os.Stderr, "quality-pairwise: only %d usable pairs (min %d); quality claim not supported\n",
			report.UsablePairs, *minUsablePairs)
		os.Exit(1)
	}
	if !pairwiseClaimSupported(report.Agreement, report.UsablePairs, *minUsablePairs, *minAgreement) {
		fmt.Fprintf(os.Stderr, "quality-pairwise: agreement %.2f below %.2f; quality claim not supported\n",
			report.Agreement, *minAgreement)
		os.Exit(1)
	}
}

// pairwiseClaimSupported is the single floor rule. Zero agreement is a
// failure, never a "no data" pass.
func pairwiseClaimSupported(agreement float64, usable, minUsable int, minAgreement float64) bool {
	return usable >= minUsable && agreement >= minAgreement
}

// pairwiseOnce asks one judge to pick A or B. When reverse is true the full
// artifact is A and single is B; the returned value is already normalized to
// single/full.
func pairwiseOnce(base, judge string, g sampleGoal, single, full string, reverse bool) (string, error) {
	a, b := single, full
	if reverse {
		a, b = full, single
	}
	prompt := pairwisePrompt(g, a, b)
	raw, err := callPairwiseJudge(base, judge, prompt)
	if err != nil {
		return "", err
	}
	switch raw {
	case "A":
		if reverse {
			return "full", nil
		}
		return "single", nil
	case "B":
		if reverse {
			return "single", nil
		}
		return "full", nil
	case "TIE":
		return "tie", nil
	}
	return "inconsistent", nil
}

func pairwisePrompt(g sampleGoal, a, b string) string {
	capText := func(s string) string {
		if len(s) > 16000 {
			return s[:16000] + "\n[truncated]"
		}
		return s
	}
	var sb strings.Builder
	sb.WriteString("You are a blind quality judge. Two artifacts answer the same task.\n")
	sb.WriteString("Judge which artifact better serves the task and its acceptance criteria. ")
	sb.WriteString("Reward concrete, correct, maintainable work; penalize bloat and unsupported claims.\n\n")
	sb.WriteString("TASK\n")
	sb.WriteString(g.Goal)
	sb.WriteString("\n\nCRITERIA\n")
	for _, c := range g.Criteria {
		sb.WriteString("- ")
		sb.WriteString(c)
		sb.WriteString("\n")
	}
	sb.WriteString("\nARTIFACT A\n")
	sb.WriteString(capText(a))
	sb.WriteString("\n\nARTIFACT B\n")
	sb.WriteString(capText(b))
	sb.WriteString("\n\nReturn one JSON object only: {\"winner\":\"A|B|TIE\",\"reason\":\"one sentence\"}")
	return sb.String()
}

func callPairwiseJudge(base, model, prompt string) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		think := false
		req := ollamaChatRequest{
			Model:    model,
			Messages: []map[string]string{{"role": "user", "content": prompt}},
			Stream:   false,
			Think:    &think,
			Options:  judgeOptions(1024, 0),
		}
		if attempt%2 == 0 {
			req.Format = "json"
		}
		body, err := json.Marshal(req)
		if err != nil {
			return "", err
		}
		client := &http.Client{Timeout: 5 * time.Minute}
		resp, err := client.Post(base+"/api/chat", "application/json", bytes.NewReader(body))
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
			lastErr = fmt.Errorf("ollama returned %s", resp.Status)
			continue
		}
		var cr ollamaChatResponse
		if err := json.Unmarshal(raw, &cr); err != nil {
			lastErr = err
			continue
		}
		obj, ok := firstJSONObject(cr.Message.Content)
		if !ok {
			lastErr = fmt.Errorf("no JSON object in judge response")
			continue
		}
		var got struct {
			Winner string `json:"winner"`
		}
		if err := json.Unmarshal([]byte(obj), &got); err != nil {
			lastErr = err
			continue
		}
		w := strings.ToUpper(strings.TrimSpace(got.Winner))
		if w == "A" || w == "B" || w == "TIE" {
			return w, nil
		}
		lastErr = fmt.Errorf("unexpected winner %q", got.Winner)
	}
	return "", lastErr
}

// reconcilePairWinner normalizes the forward and reverse verdicts (already in
// single/full terms) to one verdict.
func reconcilePairWinner(forward, reverse string) string {
	switch {
	case forward == "tie" && reverse == "tie":
		return "tie"
	case forward == "tie":
		return reverse
	case reverse == "tie":
		return forward
	case forward == reverse:
		return forward
	default:
		return "inconsistent"
	}
}

// pairwiseDiagnostics returns inter-judge agreement, per-judge forward/reverse
// self-consistency, the number of usable pairs, and the per-judge evidence.
// A pair is usable for agreement only when both judges produced a consistent
// (non-"inconsistent") reconciled winner. A tiny denominator is reported, never
// silently treated as agreement.
func pairwiseDiagnostics(pairs []pairwisePair, judges []string) (agreement, self float64, usable int, diags []judgeDiagnostic) {
	type acc struct {
		votes, errors, selfOK, orderBad int
		agree, agreeTotal               int
	}
	accs := map[string]*acc{}
	for _, judge := range judges {
		accs[judge] = &acc{}
	}
	for _, p := range pairs {
		byJudge := map[string]pairwiseVote{}
		for _, v := range p.Votes {
			a := accs[v.Judge]
			if a == nil {
				a = &acc{}
				accs[v.Judge] = a
			}
			a.votes++
			if v.ErrorText != "" {
				a.errors++
			}
			if v.Forward != "" && v.Reverse != "" {
				if v.Forward == v.Reverse {
					a.selfOK++
				} else {
					a.orderBad++
				}
			}
			byJudge[v.Judge] = v
		}
		// Inter-judge agreement over the first two consistent judges.
		var consistent []string
		for _, v := range p.Votes {
			if v.Winner != "" && v.Winner != "inconsistent" {
				consistent = append(consistent, v.Winner)
			}
		}
		if len(consistent) >= 2 {
			usable++
			for i := 0; i < len(p.Votes); i++ {
				for j := i + 1; j < len(p.Votes); j++ {
					a, okA := byJudge[p.Votes[i].Judge]
					b, okB := byJudge[p.Votes[j].Judge]
					if !okA || !okB || a.Winner == "" || b.Winner == "" ||
						a.Winner == "inconsistent" || b.Winner == "inconsistent" {
						continue
					}
					accs[a.Judge].agreeTotal++
					accs[b.Judge].agreeTotal++
					if a.Winner == b.Winner {
						accs[a.Judge].agree++
						accs[b.Judge].agree++
					}
				}
			}
		}
	}
	selfCount, voteCount := 0, 0
	for _, judge := range judges {
		a := accs[judge]
		if a == nil {
			continue
		}
		voteCount += a.votes
		selfCount += a.selfOK
		d := judgeDiagnostic{Judge: judge, Votes: a.votes, Errors: a.errors}
		if a.votes > 0 {
			// Order consistency is over pairs with two non-empty verdicts.
			orderTotal := a.selfOK + a.orderBad
			if orderTotal > 0 {
				d.SelfConsistency = float64(a.selfOK) / float64(orderTotal)
				d.OrderInconsistentRate = float64(a.orderBad) / float64(orderTotal)
			}
		}
		if a.agreeTotal > 0 {
			d.Agreement = float64(a.agree) / float64(a.agreeTotal)
		}
		diags = append(diags, d)
	}
	// Inter-judge agreement is the mean of the per-judge agreement rates on
	// pairs where both were consistent.
	totalRate, rated := 0.0, 0
	for _, d := range diags {
		if d.Agreement > 0 || d.Votes > 0 {
			totalRate += d.Agreement
			rated++
		}
	}
	if rated > 0 {
		agreement = totalRate / float64(rated)
	}
	if voteCount > 0 {
		self = float64(selfCount) / float64(voteCount)
	}
	return agreement, self, usable, diags
}

func pairwiseTasks(pairs []pairwisePair) []pairwiseTask {
	byGoal := map[string]*pairwiseTask{}
	for _, p := range pairs {
		t := byGoal[p.Goal]
		if t == nil {
			t = &pairwiseTask{Goal: p.Goal}
			byGoal[p.Goal] = t
		}
		t.Pairs++
		switch p.Winner {
		case "single":
			t.SingleWins++
		case "full":
			t.FullWins++
		case "tie":
			t.Ties++
		default:
			t.Inconsistent++
		}
	}
	out := make([]pairwiseTask, 0, len(byGoal))
	for _, t := range byGoal {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Goal < out[j].Goal })
	return out
}

func pairwiseGenerators(rows []collectedArtifact) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		if r.ModelLabel != "" && !seen[r.ModelLabel] {
			seen[r.ModelLabel] = true
			out = append(out, r.ModelLabel)
		}
	}
	sort.Strings(out)
	return out
}
