// Offline quality scoring for benchmark v3 (ADR-0133). Deterministic checks
// decide correctness; this pass asks two pinned judges to score the
// non-deterministic dimensions (depth, originality, structure, robustness,
// maintainable complexity) and reports their agreement. It never changes a
// deterministic pass.
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
	"sort"
	"strings"
	"time"

	judgeq "github.com/tcs76321/athanor/internal/judge"
	"gopkg.in/yaml.v3"
)

type qualityDimension struct {
	Weight      int            `yaml:"weight"`
	Description string         `yaml:"description"`
	Anchors     map[int]string `yaml:"anchors"`
}

type qualityRubric struct {
	ID           string                      `yaml:"id"`
	Dimensions   map[string]qualityDimension `yaml:"dimensions"`
	BloatPenalty struct {
		Trigger string `yaml:"trigger"`
		Effect  string `yaml:"effect"`
	} `yaml:"bloat_penalty"`
}

type qualityScore struct {
	GoalName   string             `json:"goal_name"`
	ModelLabel string             `json:"model_label"`
	Arm        string             `json:"arm"`
	Run        int                `json:"run"`
	ArtifactID string             `json:"artifact_id"`
	Judge      string             `json:"judge"`
	Scores     map[string]float64 `json:"scores"`
	Overall    float64            `json:"overall"`
	Rationale  string             `json:"rationale,omitempty"`
	Raw        string             `json:"raw,omitempty"`
	Error      string             `json:"error,omitempty"`
}

type qualityReport struct {
	Rubric    string         `json:"rubric"`
	Judges    []string       `json:"judges"`
	Artifacts int            `json:"artifacts"`
	Scores    []qualityScore `json:"scores"`
	Agreement float64        `json:"agreement"`
}

func loadQualityRubric(path string) (qualityRubric, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return qualityRubric{}, err
	}
	var r qualityRubric
	if err := yaml.Unmarshal(data, &r); err != nil {
		return qualityRubric{}, fmt.Errorf("parsing rubric %s: %w", path, err)
	}
	if r.ID == "" || len(r.Dimensions) == 0 {
		return qualityRubric{}, fmt.Errorf("rubric %s has no id or dimensions", path)
	}
	return r, nil
}

// qualityPrompt asks for a strict JSON object. The artifact is capped so one
// huge document cannot blow the judge's context; the cap is reported by the
// caller when it triggers.
func qualityPrompt(r qualityRubric, a collectedArtifact) string {
	var b strings.Builder
	b.WriteString("You are a blind quality judge. Score the artifact against the rubric.\n")
	b.WriteString("Deterministic correctness has already been checked; do not re-litigate it.\n")
	b.WriteString("Reward complexity only when it serves the brief; penalize bloat.\n\n")
	b.WriteString("RUBRIC\n")
	dims := make([]string, 0, len(r.Dimensions))
	for name := range r.Dimensions {
		dims = append(dims, name)
	}
	sort.Strings(dims)
	for _, name := range dims {
		d := r.Dimensions[name]
		fmt.Fprintf(&b, "- %s (weight %d): %s\n", name, d.Weight, d.Description)
		keys := make([]int, 0, len(d.Anchors))
		for k := range d.Anchors {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "    %d: %s\n", k, d.Anchors[k])
		}
	}
	if r.BloatPenalty.Trigger != "" {
		fmt.Fprintf(&b, "\nBLOAT PENALTY: %s %s\n", r.BloatPenalty.Trigger, r.BloatPenalty.Effect)
	}
	b.WriteString("\nTASK\n")
	fmt.Fprintf(&b, "goal_id: %s\n", a.GoalName)
	if len(a.Text) > 16000 {
		fmt.Fprintf(&b, "\nARTIFACT (truncated to 16000 bytes)\n%s\n", a.Text[:16000])
	} else {
		fmt.Fprintf(&b, "\nARTIFACT\n%s\n", a.Text)
	}
	b.WriteString("\nReturn one JSON object only:\n")
	b.WriteString(`{"scores":{"dimension":1-5},"overall":1-5,"rationale":"one sentence"}`)
	return b.String()
}

func qualitySeed(artifactID, judge string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(artifactID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(judge))
	return int64(h.Sum64() & (1<<63 - 1))
}

// callQualityJudge sends one artifact to one judge. It retries once without
// the JSON format hint because some local models return an empty body under
// format=json (the same behavior the pointwise judge handles).
func callQualityJudge(baseURL, model, prompt string, seed int64) (qualityScore, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		score, err := callQualityJudgeOnce(baseURL, model, prompt, seed, attempt == 0)
		if err == nil {
			return score, nil
		}
		lastErr = err
	}
	return qualityScore{}, lastErr
}

func callQualityJudgeOnce(baseURL, model, prompt string, seed int64, useFormat bool) (qualityScore, error) {
	req := ollamaChatRequest{
		Model:    model,
		Messages: []map[string]string{{"role": "user", "content": prompt}},
		Stream:   false,
		Think:    judgeThink,
		Options:  judgeOptions(1024, seed),
	}
	if useFormat {
		req.Format = "json"
	}
	body, err := json.Marshal(req)
	if err != nil {
		return qualityScore{}, err
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Post(baseURL+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		return qualityScore{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return qualityScore{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return qualityScore{}, fmt.Errorf("ollama returned %s: %s", resp.Status, truncate(string(raw), 200))
	}
	var cr ollamaChatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return qualityScore{}, err
	}
	score, err := parseQualityContent(cr.Message.Content)
	if err != nil {
		return qualityScore{}, err
	}
	score.Raw = truncate(cr.Message.Content, 4000)
	return score, nil
}

func parseQualityContent(content string) (qualityScore, error) {
	obj, ok := firstJSONObject(content)
	if !ok {
		return qualityScore{}, fmt.Errorf("no JSON object in quality response: %q", truncate(content, 120))
	}
	var decoded struct {
		Scores    map[string]any `json:"scores"`
		Overall   any            `json:"overall"`
		Rationale string         `json:"rationale"`
	}
	if err := json.Unmarshal([]byte(obj), &decoded); err != nil {
		return qualityScore{}, fmt.Errorf("decoding quality JSON: %w", err)
	}
	scores := map[string]float64{}
	sum, n := 0.0, 0
	for name, v := range decoded.Scores {
		f, ok := toFloat(v)
		if !ok {
			continue
		}
		if f < 1 {
			f = 1
		}
		if f > 5 {
			f = 5
		}
		scores[name] = f
		sum += f
		n++
	}
	if len(scores) == 0 {
		return qualityScore{}, fmt.Errorf("quality response has no numeric scores")
	}
	overall, ok := toFloat(decoded.Overall)
	if !ok || overall <= 0 {
		overall = sum / float64(n)
	}
	if overall < 1 {
		overall = 1
	}
	if overall > 5 {
		overall = 5
	}
	return qualityScore{Scores: scores, Overall: overall, Rationale: decoded.Rationale}, nil
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case string:
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSpace(x), "%f", &f); err == nil {
			return f, true
		}
	}
	return 0, false
}

// qualityAgreement is the Spearman correlation of judge overall scores over
// the artifacts both judges scored. 0 means undefined or no overlap.
func qualityAgreement(scores []qualityScore) float64 {
	byArtifact := map[string]map[string]float64{}
	for _, s := range scores {
		if s.Error != "" {
			continue
		}
		key := fmt.Sprintf("%s|%s|%d", s.GoalName, s.Arm, s.Run)
		if byArtifact[key] == nil {
			byArtifact[key] = map[string]float64{}
		}
		byArtifact[key][s.Judge] = s.Overall
	}
	judges := map[string]bool{}
	for _, s := range scores {
		judges[s.Judge] = true
	}
	if len(judges) < 2 {
		return 0
	}
	var names []string
	for j := range judges {
		names = append(names, j)
	}
	sort.Strings(names)
	var samples []judgeq.Sample
	for _, m := range byArtifact {
		a, okA := m[names[0]]
		b, okB := m[names[1]]
		if okA && okB {
			samples = append(samples, judgeq.Sample{AnchorRating: a, JudgeScore: b})
		}
	}
	if len(samples) < 3 {
		return 0
	}
	return judgeq.Spearman(samples)
}

func runQuality(args []string) {
	fs := flag.NewFlagSet("quality", flag.ExitOnError)
	inPath := fs.String("in", "", "artifacts.json from the probe")
	rubricPath := fs.String("rubric", "eval/bench/v3/quality-rubric.yaml", "quality rubric")
	judgesCSV := fs.String("judges", "granite4.2:8b,lfm3-omni:2b-mxfp8", "comma-separated judge models")
	ollamaURL := fs.String("ollama", "http://localhost:11434", "Ollama base URL")
	outPath := fs.String("out", "quality-scores.json", "scores output")
	minAgreement := fs.Float64("min-agreement", 0.6, "fail below this Spearman floor when both judges scored")
	maxArtifacts := fs.Int("max", 0, "score at most N artifacts (0 = all)")
	_ = fs.Parse(args)
	if *inPath == "" {
		fmt.Fprintln(os.Stderr, "quality: -in is required")
		os.Exit(2)
	}
	rubric, err := loadQualityRubric(*rubricPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "quality:", err)
		os.Exit(1)
	}
	var artifacts []collectedArtifact
	raw, err := os.ReadFile(*inPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "quality:", err)
		os.Exit(1)
	}
	if err := json.Unmarshal(raw, &artifacts); err != nil {
		fmt.Fprintln(os.Stderr, "quality:", err)
		os.Exit(1)
	}
	if *maxArtifacts > 0 && len(artifacts) > *maxArtifacts {
		artifacts = artifacts[:*maxArtifacts]
	}
	judges := splitCSV(*judgesCSV)
	base := strings.TrimRight(*ollamaURL, "/")
	report := qualityReport{Rubric: rubric.ID, Judges: judges, Artifacts: len(artifacts)}
	for _, judge := range judges {
		for _, a := range artifacts {
			prompt := qualityPrompt(rubric, a)
			score, err := callQualityJudge(base, judge, prompt, qualitySeed(a.ArtifactID, judge))
			score.GoalName, score.ModelLabel, score.Arm = a.GoalName, a.ModelLabel, a.Arm
			score.Run, score.ArtifactID, score.Judge = a.Run, a.ArtifactID, judge
			if err != nil {
				score.Error = err.Error()
			}
			report.Scores = append(report.Scores, score)
			status := fmt.Sprintf("%.2f", score.Overall)
			if score.Error != "" {
				status = "error: " + truncate(score.Error, 80)
			}
			fmt.Printf("  %-24s %-10s %-10s run %d %-24s %s\n",
				a.GoalName, a.Arm, a.ModelLabel, a.Run, judge, status)
		}
	}
	report.Agreement = qualityAgreement(report.Scores)
	if err := writeJSON(*outPath, report); err != nil {
		fmt.Fprintln(os.Stderr, "quality: write:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (agreement %.3f)\n", *outPath, report.Agreement)
	if report.Agreement > 0 && report.Agreement < *minAgreement {
		fmt.Fprintf(os.Stderr, "quality: judge agreement %.3f is below the %.2f floor; the quality claim is not supported\n",
			report.Agreement, *minAgreement)
		os.Exit(1)
	}
}
