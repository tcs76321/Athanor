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
	Generators      []string       `json:"generators"`
	Judges          []string       `json:"judges"`
	Pairs           []pairwisePair `json:"pairs"`
	Tasks           []pairwiseTask `json:"tasks"`
	SingleWins      int            `json:"single_wins"`
	FullWins        int            `json:"full_wins"`
	Ties            int            `json:"ties"`
	Decisive        int            `json:"decisive"`
	WinRate         float64        `json:"win_rate"`
	Agreement       float64        `json:"agreement"`
	SelfConsistency float64        `json:"self_consistency"`
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
	for _, judge := range judges {
		for _, key := range keys {
			s := singleBy[key]
			f := fullBy[key]
			goalspec, ok := goalByID[s.GoalName]
			if !ok {
				continue
			}
			pair := pairwisePair{Goal: s.GoalName, Run: s.Run}
			fwd, err := pairwiseOnce(base, judge, goalspec, s.Text, f.Text, false)
			if err != nil {
				pair.Votes = append(pair.Votes, pairwiseVote{Goal: s.GoalName, Run: s.Run, Judge: judge, ErrorText: err.Error()})
				report.Pairs = append(report.Pairs, pair)
				continue
			}
			rev, err := pairwiseOnce(base, judge, goalspec, s.Text, f.Text, true)
			if err != nil {
				pair.Votes = append(pair.Votes, pairwiseVote{Goal: s.GoalName, Run: s.Run, Judge: judge, Forward: fwd, ErrorText: err.Error()})
				report.Pairs = append(report.Pairs, pair)
				continue
			}
			vote := pairwiseVote{Goal: s.GoalName, Run: s.Run, Judge: judge,
				Forward: fwd, Reverse: rev, Winner: reconcilePairWinner(fwd, rev)}
			pair.Votes = append(pair.Votes, vote)
			report.Pairs = append(report.Pairs, pair)
			fmt.Printf("  %-24s run %d %-20s %s\n", s.GoalName, s.Run, judge, vote.Winner)
		}
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
	report.Agreement, report.SelfConsistency = pairwiseAgreement(report.Pairs, len(judges))
	report.Generators = pairwiseGenerators(single)
	if err := writeJSON(*outPath, report); err != nil {
		fmt.Fprintln(os.Stderr, "quality-pairwise: write:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d pairs, full win rate %.2f, agreement %.2f)\n",
		*outPath, len(report.Pairs), report.WinRate, report.Agreement)
	if report.Agreement > 0 && report.Agreement < *minAgreement {
		fmt.Fprintf(os.Stderr, "quality-pairwise: agreement %.2f below %.2f; quality claim not supported\n",
			report.Agreement, *minAgreement)
		os.Exit(1)
	}
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

// pairwiseAgreement returns inter-judge majority agreement and per-judge
// forward/reverse self-consistency.
func pairwiseAgreement(pairs []pairwisePair, judges int) (agreement, self float64) {
	if judges < 2 {
		return 0, 0
	}
	agree, total := 0, 0
	for _, p := range pairs {
		if len(p.Votes) < 2 {
			continue
		}
		a, b := p.Votes[0].Winner, p.Votes[1].Winner
		if a == "" || b == "" || a == "inconsistent" || b == "inconsistent" {
			continue
		}
		total++
		if a == b {
			agree++
		}
	}
	if total > 0 {
		agreement = float64(agree) / float64(total)
	}
	selfAgree, selfTotal := 0, 0
	for _, p := range pairs {
		for _, v := range p.Votes {
			if v.Forward == "" || v.Reverse == "" {
				continue
			}
			selfTotal++
			if v.Forward == v.Reverse {
				selfAgree++
			}
		}
	}
	if selfTotal > 0 {
		self = float64(selfAgree) / float64(selfTotal)
	}
	return agreement, self
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
