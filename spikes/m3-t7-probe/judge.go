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
	Options  map[string]any      `json:"options"`
}

type ollamaChatResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Done bool `json:"done"`
}

// callJudge sends one blind judge packet to one model and parses the JSON
// score. Temperature is pinned to 0 and the seed is derived from
// (packet, judge) for reproducibility (Ollama ignores the seed under
// greedy decoding but records the intent).
func callJudge(baseURL, model, packet string, seed int64) (judgeResponse, error) {
	body, err := json.Marshal(ollamaChatRequest{
		Model:    model,
		Messages: []map[string]string{{"role": "user", "content": packet}},
		Stream:   false,
		Format:   "json",
		Options:  map[string]any{"temperature": 0.0, "seed": seed, "num_predict": 1024},
	})
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
	out := judgeResponse{
		Score:           coerceFloat(raw["score"]),
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
	_ = fs.Parse(args)

	judges := splitCSV(*judgesCSV)
	base := strings.TrimRight(*ollamaURL, "/")
	var results []judgeResult
	packets := 0

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
			packet, err := os.ReadFile(filepath.Join(packetsDir, row.PacketID+".md"))
			if err != nil {
				return err
			}
			for _, j := range judges {
				res := judgeResult{
					Goal: row.Goal, ModelLabel: row.Model, Arm: row.Arm,
					Run: row.Run, PacketID: row.PacketID, Judge: j,
				}
				jr, jerr := callJudge(base, j, string(packet), judgeSeed(row.PacketID, j))
				if jerr != nil {
					res.Error = jerr.Error()
				} else {
					res.Score = jr.Score
					res.CriteriaMet = jr.CriteriaMet
					res.CriteriaMissing = jr.CriteriaMissing
					res.Notes = jr.Notes
				}
				results = append(results, res)
			}
			packets++
			fmt.Printf("judged %s with %d judges\n", row.PacketID, len(judges))
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "m3-t7 judge:", err)
		os.Exit(1)
	}
	if packets == 0 {
		fmt.Fprintf(os.Stderr, "m3-t7 judge: no packets found under %s\n", *outDir)
		os.Exit(1)
	}
	if err := writeJSON(filepath.Join(*outDir, "judges.json"), results); err != nil {
		fmt.Fprintln(os.Stderr, "m3-t7 judge:", err)
		os.Exit(1)
	}
	fmt.Printf("judged %d packets with %d judges (%d rows) → %s\n",
		packets, len(judges), len(results), filepath.Join(*outDir, "judges.json"))
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
