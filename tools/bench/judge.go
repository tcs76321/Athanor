// The offline judge pass. It reads the blind packets a `run` produced and
// scores each artifact with the independent third-party models in
// judgeModels, via Ollama. It is a separate subcommand so judging happens
// with the generator's daemon stopped (the judge gets the machine) and so
// judging can be re-run without regenerating artifacts.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// judgeResult is one judge's score for one artifact.
type judgeResult struct {
	Goal            string   `json:"goal"`
	ModelLabel      string   `json:"model_label"`
	Arm             string   `json:"arm"`
	Run             int      `json:"run"`
	PacketID        string   `json:"packet_id"`
	Judge           string   `json:"judge"`
	Score           float64  `json:"score"`
	CriteriaMet     []string `json:"criteria_met,omitempty"`
	CriteriaMissing []string `json:"criteria_missing,omitempty"`
	Notes           string   `json:"notes,omitempty"`
	Error           string   `json:"error,omitempty"`
}

// judgeResponse is the schema the judge is asked to emit.
type judgeResponse struct {
	Score           float64
	CriteriaMet     []string
	CriteriaMissing []string
	Notes           string
}

// ollamaChatRequest/Response are the subset of Ollama's /api/chat the judge
// pass uses.
type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Messages []map[string]string `json:"messages"`
	Stream   bool                `json:"stream"`
	Format   string              `json:"format"`
	Think    *bool               `json:"think,omitempty"`
	Options  map[string]any      `json:"options"`
}

// judgeThink, when non-nil, is sent as Ollama's `think` on anchor/judge
// calls. The engine disables thinking on judgment phases (F4-T8); a
// thinking-capable judge (e.g. the generator) must be measured the same way.
var judgeThink *bool

// judgeNumCtx, when > 0, caps the context Ollama loads for a judge call.
// The anchor artifacts are short; without this, a judge is loaded at its
// model-default context (e.g. 131072), which is far slower than needed.
var judgeNumCtx int

// judgeMaxPredict caps a judge's output tokens. A score JSON needs ~100
// tokens; the engine's judgment calls are bounded too (F4-T8). The default is
// generous because the judge may list criteria; runJudge lowers it.
var judgeMaxPredict = 2048

func judgeOptions(numPredict int, seed int64) map[string]any {
	opts := map[string]any{"temperature": 0.0, "seed": seed, "num_predict": numPredict}
	if judgeNumCtx > 0 {
		opts["num_ctx"] = judgeNumCtx
	}
	return opts
}

type ollamaChatResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Done bool `json:"done"`
}

// callJudge sends one blind judge packet to one model and parses the JSON
// score, retrying on failure. Some models return an *empty* body under JSON
// mode (observed with granite4.2:3b in the M3-T7 run: 19/37 empty), so
// alternate attempts drop the format hint; a judge that cannot answer in
// three tries is an error, never a silent zero.
func callJudge(baseURL, model, packet string, seed int64) (judgeResponse, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		jr, err := callJudgeOnce(baseURL, model, packet, seed, attempt%2 == 0)
		if err == nil {
			return jr, nil
		}
		lastErr = err
	}
	return judgeResponse{}, lastErr
}

// callJudgeOnce is one attempt. Temperature is pinned to 0 and the seed is
// derived from (packet, judge) for reproducibility (Ollama ignores the seed
// under greedy decoding but records the intent).
func callJudgeOnce(baseURL, model, packet string, seed int64, useFormat bool) (judgeResponse, error) {
	req := ollamaChatRequest{
		Model:    model,
		Messages: []map[string]string{{"role": "user", "content": packet}},
		Stream:   false,
		Think:    judgeThink,
		Options:  judgeOptions(judgeMaxPredict, seed),
	}
	if useFormat {
		req.Format = "json"
	}
	body, err := json.Marshal(req)
	if err != nil {
		return judgeResponse{}, err
	}
	// Bound the judge call so a degenerate response cannot hang the pass.
	client := &http.Client{Timeout: 3 * time.Minute}
	resp, err := client.Post(baseURL+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		return judgeResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return judgeResponse{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return judgeResponse{}, fmt.Errorf("ollama returned %s: %s", resp.Status, truncate(string(raw), 200))
	}
	var cr ollamaChatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return judgeResponse{}, err
	}
	return parseJudgeContent(cr.Message.Content)
}

// parseJudgeContent tolerantly decodes the judge's JSON score. Models
// drift types (a string score, a string instead of an array), so the
// decode coerces the same shapes the engine's verdict parser does.
func parseJudgeContent(content string) (judgeResponse, error) {
	obj, ok := firstJSONObject(content)
	if !ok {
		return judgeResponse{}, fmt.Errorf("no JSON object in judge response: %q", truncate(content, 120))
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(obj), &raw); err != nil {
		return judgeResponse{}, err
	}
	// A missing score is an error, not a zero: coercing an absent key to 0
	// would silently conflate "the judge failed" with "the judge scored 0".
	scoreRaw, hasScore := raw["score"]
	if !hasScore {
		return judgeResponse{}, fmt.Errorf("judge response has no score field: %q", truncate(content, 120))
	}
	out := judgeResponse{
		Score:           coerceFloat(scoreRaw),
		CriteriaMet:     coerceStrings(raw["criteria_met"]),
		CriteriaMissing: coerceStrings(raw["criteria_missing"]),
	}
	out.Notes, _ = raw["notes"].(string)
	return out, nil
}

// firstJSONObject returns the first balanced { ... } substring.
func firstJSONObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", false
	}
	depth, end := 0, -1
	inStr, escape := false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		if escape {
			escape = false
			continue
		}
		if c == '\\' && inStr {
			escape = true
			continue
		}
		if c == '"' {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return "", false
	}
	return s[start : end+1], true
}

func coerceFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f
	default:
		return 0
	}
}

func coerceStrings(v any) []string {
	switch x := v.(type) {
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			out = append(out, fmt.Sprint(e))
		}
		return out
	case string:
		if x == "" {
			return nil
		}
		return []string{x}
	default:
		return nil
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// judgeSeed derives a reproducible seed from a packet id and judge name.
func judgeSeed(packetID, judge string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(packetID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(judge))
	return int64(h.Sum64() & (1<<63 - 1))
}

// runJudge is the `judge` subcommand: it walks the results tree for blind
// packets and scores each with every judge model, writing judges.json.
func runJudge(args []string) {
	fs := flag.NewFlagSet("judge", flag.ExitOnError)
	outDir := fs.String("out", filepath.Join("spikes", "m3-t7-probe", "results"), "results directory")
	ollamaURL := fs.String("ollama", "http://localhost:11434", "Ollama base URL")
	judgesCSV := fs.String("judges", strings.Join(judgeModels, ","), "comma-separated judge models")
	minSuccess := fs.Float64("min-success", 0.8, "fail if any judge's success rate is below this fraction")
	thinkMode := fs.String("think", "false", "send Ollama think on judge calls: default | false | true")
	numCtx := fs.Int("num-ctx", 8192, "cap the judge's Ollama context (0 = model default)")
	maxPredict := fs.Int("max-predict", 512, "cap judge output tokens (a score JSON needs ~100)")
	_ = fs.Parse(args)

	judgeNumCtx = *numCtx
	judgeMaxPredict = *maxPredict
	switch *thinkMode {
	case "default":
	case "false":
		f := false
		judgeThink = &f
	case "true":
		t := true
		judgeThink = &t
	default:
		fmt.Fprintf(os.Stderr, "judge: unknown -think %q\n", *thinkMode)
		os.Exit(2)
	}

	judges := splitCSV(*judgesCSV)
	base := strings.TrimRight(*ollamaURL, "/")

	// Collect every packet first so judging can be ordered judge-outer: on a
	// single-residency server (ARCHITECTURE §12.5) that means len(judges)
	// model loads, not len(packets)*len(judges). The anchor protocol uses the
	// same ordering; packet-outer thrashes the model on every packet.
	type packet struct {
		id, goal, model, arm, content string
		run                           int
	}
	var packetList []packet
	err := filepath.WalkDir(*outDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "index.json" {
			return nil
		}
		packetsDir := filepath.Dir(path)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var index []struct {
			PacketID string `json:"packet_id"`
			Goal     string `json:"goal"`
			Model    string `json:"model"`
			Arm      string `json:"arm"`
			Run      int    `json:"run"`
		}
		if err := json.Unmarshal(b, &index); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		for _, row := range index {
			content, err := os.ReadFile(filepath.Join(packetsDir, row.PacketID+".md"))
			if err != nil {
				return err
			}
			packetList = append(packetList, packet{
				id: row.PacketID, goal: row.Goal, model: row.Model,
				arm: row.Arm, run: row.Run, content: string(content),
			})
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "m3-t7 judge:", err)
		os.Exit(1)
	}
	if len(packetList) == 0 {
		fmt.Fprintf(os.Stderr, "m3-t7 judge: no packets found under %s\n", *outDir)
		os.Exit(1)
	}

	var results []judgeResult
	for _, j := range judges {
		fmt.Printf("judge %s: scoring %d packets\n", j, len(packetList))
		for i, p := range packetList {
			res := judgeResult{
				Goal: p.goal, ModelLabel: p.model, Arm: p.arm,
				Run: p.run, PacketID: p.id, Judge: j,
			}
			jr, jerr := callJudge(base, j, p.content, judgeSeed(p.id, j))
			if jerr != nil {
				res.Error = jerr.Error()
			} else {
				res.Score = jr.Score
				res.CriteriaMet = jr.CriteriaMet
				res.CriteriaMissing = jr.CriteriaMissing
				res.Notes = jr.Notes
			}
			results = append(results, res)
			if (i+1)%10 == 0 || i+1 == len(packetList) {
				fmt.Printf("  %s: %d/%d\n", j, i+1, len(packetList))
			}
		}
	}
	packets := len(packetList)
	// Reliability gate: a judge whose calls mostly error is not a
	// measurement. Report per-judge success and fail loudly below the floor,
	// after writing the raw results so a failure is still inspectable.
	ok, total := map[string]int{}, map[string]int{}
	for _, r := range results {
		total[r.Judge]++
		if r.Error == "" {
			ok[r.Judge]++
		}
	}
	unreliable := false
	for _, j := range judges {
		rate := 0.0
		if total[j] > 0 {
			rate = float64(ok[j]) / float64(total[j])
		}
		fmt.Printf("judge %s: %d/%d ok (%.0f%%)\n", j, ok[j], total[j], rate*100)
		if total[j] > 0 && rate < *minSuccess {
			unreliable = true
		}
	}
	if err := writeJSON(filepath.Join(*outDir, "judges.json"), results); err != nil {
		fmt.Fprintln(os.Stderr, "m3-t7 judge:", err)
		os.Exit(1)
	}
	fmt.Printf("judged %d packets with %d judges (%d rows) → %s\n",
		packets, len(judges), len(results), filepath.Join(*outDir, "judges.json"))
	if unreliable {
		fmt.Fprintf(os.Stderr, "m3-t7 judge: a judge's success rate is below %.0f%% — the judgment layer is not trustworthy\n", *minSuccess*100)
		os.Exit(1)
	}
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
