package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/onboard"
)

// ladderOptions bounds one qualification run.
type ladderOptions struct {
	StatePath   string
	Roles       []string
	MaxModels   int
	CaseTimeout time.Duration
	Wall        time.Duration
}

// runLadder is the F2 bounded qualification ladder: it screens the installed
// models that fit on this machine with a small grounded instrument, persists
// each case so a rerun resumes, and returns the per-role measurements the
// existing routing proposal consumes.
//
// This is a screen, not the frozen benchmark: the generator cases test
// deterministic reasoning and output discipline, and the judge cases test
// preference against fixed labels. The probe/frozen corpus remains the full
// qualification (docs/qualification-smoke-m9.md).
func runLadder(ctx context.Context, cfg *config.Config, models []onboard.Model,
	roles []onboard.Role, opts ladderOptions) ([]onboard.Measurement, error) {
	ladder := onboard.DefaultLadder()
	state, err := loadLadderState(opts.StatePath)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, opts.Wall)
	defer cancel()

	byRole := map[string]onboard.Role{}
	for _, r := range roles {
		byRole[r.Role] = r
	}
	var all []onboard.Measurement
	for _, roleName := range opts.Roles {
		role, ok := byRole[roleName]
		if !ok {
			fmt.Printf("ladder: unknown role %q (have %v)\n", roleName, roleNames(roles))
			continue
		}
		cands := ladderCandidates(role, models, opts.MaxModels)
		if len(cands) == 0 {
			fmt.Printf("ladder: %s has no installed model that fits\n", roleName)
			continue
		}
		fmt.Printf("ladder: %s — %d candidate(s): %s\n", roleName, len(cands), strings.Join(cands, ", "))
		for _, model := range cands {
			if err := runLadderModel(ctx, cfg, ladder, &state, opts.StatePath, roleName, model, opts.CaseTimeout); err != nil {
				if ctx.Err() != nil {
					fmt.Printf("ladder: wall budget reached at %s/%s\n", roleName, model)
					break
				}
				fmt.Printf("ladder: %s/%s stopped: %v\n", roleName, model, err)
			}
		}
	}

	if err := saveLadderState(opts.StatePath, state); err != nil {
		return nil, err
	}
	all = state.Measurements(opts.Roles, ladderModelNames(models, roles))
	if err := writeMeasurements(opts.StatePath, all); err != nil {
		return nil, err
	}
	printLadderSummary(state, opts.Roles, ladderModelNames(models, roles))
	return all, nil
}

// runLadderModel runs the cases for one (role, model). A completed case is
// skipped, so an interrupted run resumes where it stopped.
func runLadderModel(ctx context.Context, cfg *config.Config, l onboard.Ladder,
	state *onboard.LadderState, statePath, role, model string, timeout time.Duration) error {
	if role == "security" {
		for i, c := range l.Judges {
			if state.Done(role, model, c.ID) {
				continue
			}
			prompt, want := onboard.JudgePrompt(c, i%2 == 0)
			answer, err := ladderChat(ctx, cfg.Inference.OllamaURL, model, prompt, timeout)
			correct := err == nil && onboard.ScoreJudge(answer, want)
			state.Record(onboard.CaseResult{Role: role, Model: model, CaseID: c.ID, Correct: correct})
			if err := saveLadderState(statePath, *state); err != nil {
				return err
			}
			fmt.Printf("  %-12s %-24s %-14s %s\n", role, model, c.ID, ladderMark(correct, answer, err))
		}
		return nil
	}
	for _, c := range l.Generators {
		if state.Done(role, model, c.ID) {
			continue
		}
		answer, err := ladderChat(ctx, cfg.Inference.OllamaURL, model, onboard.GenPrompt(c), timeout)
		correct := err == nil && onboard.ScoreGen(c, answer)
		state.Record(onboard.CaseResult{Role: role, Model: model, CaseID: c.ID, Correct: correct})
		if err := saveLadderState(statePath, *state); err != nil {
			return err
		}
		fmt.Printf("  %-12s %-24s %-14s %s\n", role, model, c.ID, ladderMark(correct, answer, err))
	}
	return nil
}

func ladderMark(ok bool, answer string, err error) string {
	if err != nil {
		return "error: " + shortErr(err)
	}
	mark := "FAIL"
	if ok {
		mark = "ok"
	}
	return fmt.Sprintf("%-4s %q", mark, truncate(strings.TrimSpace(answer), 48))
}

// ladderCandidates returns the configured model plus the largest installed
// models, deduplicated and bounded. The caller passes only models that fit.
func ladderCandidates(role onboard.Role, models []onboard.Model, max int) []string {
	if max < 1 {
		max = 1
	}
	fitting := append([]onboard.Model(nil), models...)
	sort.SliceStable(fitting, func(i, j int) bool {
		if fitting[i].SizeBytes != fitting[j].SizeBytes {
			return fitting[i].SizeBytes > fitting[j].SizeBytes
		}
		return fitting[i].Name < fitting[j].Name
	})
	seen := map[string]bool{}
	out := make([]string, 0, max)
	add := func(name string) {
		if name == "" || seen[name] || len(out) >= max {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	add(role.Model)
	for _, m := range fitting {
		add(m.Name)
	}
	return out
}

// ladderChat is one temperature-0, non-thinking, bounded call. It is the only
// model-facing part of the ladder; scoring is local and deterministic.
func ladderChat(ctx context.Context, baseURL, model, prompt string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	body, err := json.Marshal(map[string]any{
		"model": model, "stream": false, "think": false,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
		"options":  map[string]any{"temperature": 0, "num_predict": 512, "num_ctx": 8192},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", resp.Status, truncate(string(raw), 160))
	}
	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if out.Error != "" {
		return "", fmt.Errorf("%s", out.Error)
	}
	return out.Message.Content, nil
}

func loadLadderState(path string) (onboard.LadderState, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return onboard.NewLadderState(), nil
	}
	if err != nil {
		return onboard.LadderState{}, err
	}
	var s onboard.LadderState
	if err := json.Unmarshal(raw, &s); err != nil {
		return onboard.LadderState{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if s.Version != onboard.LadderVersion || s.Results == nil {
		fmt.Printf("ladder: %s uses an older instrument; starting fresh\n", path)
		return onboard.NewLadderState(), nil
	}
	return s, nil
}

func saveLadderState(path string, s onboard.LadderState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func writeMeasurements(statePath string, ms []onboard.Measurement) error {
	raw, err := json.MarshalIndent(ms, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(filepath.Dir(statePath), "measurements.json"), raw, 0o644)
}

func printLadderSummary(s onboard.LadderState, roles, models []string) {
	fmt.Println()
	fmt.Println("ladder measurements (grounded screen, not the frozen corpus):")
	for _, role := range roles {
		for _, model := range models {
			if m, ok := s.Measurement(role, model); ok {
				fmt.Printf("  %-12s %-24s %.2f (%d case(s))\n", role, model, m.Score, m.Samples)
			}
		}
	}
}

func roleNames(roles []onboard.Role) []string {
	out := make([]string, 0, len(roles))
	for _, r := range roles {
		out = append(out, r.Role)
	}
	return out
}

func ladderModelNames(models []onboard.Model, roles []onboard.Role) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range models {
		if !seen[m.Name] {
			seen[m.Name] = true
			out = append(out, m.Name)
		}
	}
	for _, r := range roles {
		if r.Model != "" && !seen[r.Model] {
			seen[r.Model] = true
			out = append(out, r.Model)
		}
	}
	return out
}

func shortErr(err error) string {
	return truncate(err.Error(), 80)
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
