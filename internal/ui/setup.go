package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/onboard"
)

// qualKey is the `system_state` key for the UI's bounded qualification run.
// It survives a Core restart; a stale "running" flag is ignored after qualTTL.
const qualKey = "ui:qualification"

// qualTTL bounds how long a persisted "running" flag is trusted. A Core
// restart cannot resume the goroutine, so a stale run is restartable.
const qualTTL = 10 * time.Minute

// qualState is the resumable qualification state.
type qualState struct {
	Running    bool            `json:"running"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt time.Time       `json:"finished_at"`
	Total      int             `json:"total"`
	Done       int             `json:"done"`
	Results    map[string]bool `json:"results"`
	Error      string          `json:"error,omitempty"`
}

func newQualState() qualState {
	return qualState{Results: map[string]bool{}}
}

func (s qualState) stale() bool {
	return s.Running && time.Since(s.StartedAt) > qualTTL
}

func qualCaseKey(role, model, caseID string) string {
	return role + "|" + model + "|" + caseID
}

// setupRole is one persona row on the setup page.
type setupRole struct {
	Role          string
	Model         string
	Family        string
	ContextTarget int
	Installed     bool
}

// setupModel is one installed Ollama model.
type setupModel struct {
	Name      string
	SizeBytes int64
	Family    string
}

func (u *UI) handleSetup(w http.ResponseWriter, r *http.Request) {
	cfg := u.deps.Config
	data := map[string]any{
		"ConfigPath": configPath(cfg),
		"Runtime":    u.deps.Runtime,
		"Version":    u.deps.Version,
	}
	var roles []setupRole
	var families = map[string]string{}
	var models []setupModel
	if cfg != nil {
		var err error
		models, err = fetchModels(r.Context(), cfg.Inference.OllamaURL)
		if err != nil {
			data["ModelsErr"] = err.Error()
		}
		for _, name := range []string{"wide", "tall", "main", "security", "alternative"} {
			pc, ok := cfg.Personas.Role(name)
			if !ok {
				continue
			}
			roles = append(roles, setupRole{
				Role: name, Model: pc.Model, Family: pc.Family,
				ContextTarget: pc.ContextTarget,
				Installed:     modelInstalled(pc.Model, models),
			})
			if pc.Family != "" {
				families[pc.Model] = pc.Family
			}
		}
		for _, m := range models {
			if m.Family != "" {
				if _, ok := families[m.Name]; !ok {
					families[m.Name] = m.Family
				}
			}
		}
		data["OllamaURL"] = cfg.Inference.OllamaURL
	}
	state := u.loadQualState(r.Context())
	stale := state.stale()
	if stale {
		state.Running = false
	}
	data["Models"] = models
	data["Roles"] = roles
	data["State"] = state
	data["Stale"] = stale
	if cfg != nil {
		ms := qualMeasurementsFromState(state)
		data["Measurements"] = ms
		if len(ms) > 0 {
			recs := onboard.Route(setupOnboardRoles(roles), families, ms, setupRoutingOptions())
			data["Recommendations"] = recs
			data["Apply"] = applyYAML(recs)
		}
	}
	u.render(w, "Setup", setupBody, data)
}

// setupOnboardRoles converts the view rows back to the onboarding roles.
func setupOnboardRoles(rows []setupRole) []onboard.Role {
	out := make([]onboard.Role, 0, len(rows))
	for _, r := range rows {
		out = append(out, onboard.Role{Role: r.Role, Model: r.Model, ContextTarget: r.ContextTarget})
	}
	return out
}

func setupRoutingOptions() onboard.RoutingOptions {
	return onboard.RoutingOptions{
		JudgeRole:      "security",
		GeneratorRoles: []string{"main", "tall", "alternative", "wide"},
		MinImprovement: 0.05,
		MinSamples:     2,
	}
}

// applyYAML renders the model lines an operator would paste into config.yaml.
func applyYAML(recs []onboard.Recommendation) string {
	var b strings.Builder
	b.WriteString("personas:\n")
	for _, r := range recs {
		if !r.Replace {
			continue
		}
		fmt.Fprintf(&b, "  %s:\n    model: %s\n", r.Role, r.Recommended)
	}
	if b.String() == "personas:\n" {
		return ""
	}
	return b.String()
}

func (u *UI) handleQualifyStart(w http.ResponseWriter, r *http.Request) {
	cfg := u.deps.Config
	if cfg == nil {
		u.fail(w, fmt.Errorf("configuration is not available to the UI"))
		return
	}
	_ = r.ParseForm()
	state := u.loadQualState(r.Context())
	if state.Running && !state.stale() {
		http.Redirect(w, r, "/ui/setup", http.StatusSeeOther)
		return
	}
	if state.stale() || r.FormValue("fresh") != "" {
		state = newQualState()
	}
	u.qualMu.Lock()
	if u.qualRunning {
		u.qualMu.Unlock()
		http.Redirect(w, r, "/ui/setup", http.StatusSeeOther)
		return
	}
	u.qualRunning = true
	u.qualMu.Unlock()

	roles, models := setupRolesAndModels(cfg)
	go func() {
		defer func() {
			u.qualMu.Lock()
			u.qualRunning = false
			u.qualMu.Unlock()
		}()
		u.runQualification(context.Background(), cfg, roles, models, &state)
	}()
	http.Redirect(w, r, "/ui/setup", http.StatusSeeOther)
}

// setupRolesAndModels picks the two roles and two models per role the UI
// screens: the judge and the main generator, each against the configured model
// plus the largest installed alternative.
func setupRolesAndModels(cfg *config.Config) ([]onboard.Role, []onboard.Model) {
	var roles []onboard.Role
	for _, name := range []string{"security", "main"} {
		if pc, ok := cfg.Personas.Role(name); ok {
			roles = append(roles, onboard.Role{Role: name, Model: pc.Model, ContextTarget: pc.ContextTarget})
		}
	}
	models, err := fetchModels(context.Background(), cfg.Inference.OllamaURL)
	if err != nil {
		return roles, nil
	}
	out := make([]onboard.Model, 0, len(models))
	for _, m := range models {
		out = append(out, onboard.Model{Name: m.Name, SizeBytes: m.SizeBytes, Family: m.Family})
	}
	return roles, out
}

// runQualification is the bounded worker. It skips completed cases, saves
// after each one, and stops at the total wall bound.
func (u *UI) runQualification(ctx context.Context, cfg *config.Config, roles []onboard.Role, models []onboard.Model, state *qualState) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	if state.Results == nil {
		state.Results = map[string]bool{}
	}
	state.Running = true
	state.StartedAt = time.Now()
	state.Error = ""
	state.Total = qualificationPlan(roles, models)
	state.Done = len(state.Results)
	u.saveQualState(ctx, *state)
	if state.Total == 0 {
		state.Running = false
		state.Error = "no installed models to qualify; pull a model first"
		u.saveQualState(context.Background(), *state)
		return
	}

	ladder := onboard.DefaultLadder()
	judges := ladder.Judges
	if len(judges) > 4 {
		judges = judges[:4]
	}
	gens := ladder.Generators
	if len(gens) > 3 {
		gens = gens[:3]
	}
	for _, role := range roles {
		if ctx.Err() != nil {
			break
		}
		for _, model := range setupCandidates(role, models) {
			if ctx.Err() != nil {
				break
			}
			if role.Role == "security" {
				for i, c := range judges {
					if ctx.Err() != nil {
						break
					}
					key := qualCaseKey(role.Role, model, c.ID)
					if _, done := state.Results[key]; done {
						continue
					}
					prompt, want := onboard.JudgePrompt(c, i%2 == 0)
					answer, err := ollamaChat(ctx, cfg.Inference.OllamaURL, model, prompt)
					correct := err == nil && onboard.ScoreJudge(answer, want)
					state.Results[key] = correct
					state.Done = len(state.Results)
					u.saveQualState(ctx, *state)
					if ctx.Err() != nil {
						break
					}
				}
			} else {
				for _, c := range gens {
					if ctx.Err() != nil {
						break
					}
					key := qualCaseKey(role.Role, model, c.ID)
					if _, done := state.Results[key]; done {
						continue
					}
					answer, err := ollamaChat(ctx, cfg.Inference.OllamaURL, model, onboard.GenPrompt(c))
					correct := err == nil && onboard.ScoreGen(c, answer)
					state.Results[key] = correct
					state.Done = len(state.Results)
					u.saveQualState(ctx, *state)
					if ctx.Err() != nil {
						break
					}
				}
			}
		}
	}
	state.Running = false
	state.FinishedAt = time.Now()
	if ctx.Err() != nil {
		state.Error = "qualification stopped at the wall bound; start again to resume"
	}
	u.saveQualState(context.Background(), *state)
}

// qualificationPlan counts the cases the current roles/models imply.
func qualificationPlan(roles []onboard.Role, models []onboard.Model) int {
	total := 0
	for _, role := range roles {
		n := 3
		if role.Role == "security" {
			n = 4
		}
		total += n * len(setupCandidates(role, models))
	}
	return total
}

// setupCandidates returns the configured model plus the largest installed
// alternative, skipping names that are not installed.
func setupCandidates(role onboard.Role, models []onboard.Model) []string {
	installed := append([]onboard.Model(nil), models...)
	sort.SliceStable(installed, func(i, j int) bool {
		if installed[i].SizeBytes != installed[j].SizeBytes {
			return installed[i].SizeBytes > installed[j].SizeBytes
		}
		return installed[i].Name < installed[j].Name
	})
	var out []string
	seen := map[string]bool{}
	add := func(n string) {
		if n == "" || seen[n] || !onboardModelInstalled(n, models) {
			return
		}
		seen[n] = true
		out = append(out, n)
	}
	add(role.Model)
	for _, m := range installed {
		if len(out) == 2 {
			break
		}
		add(m.Name)
	}
	return out
}

func (u *UI) loadQualState(ctx context.Context) qualState {
	var raw string
	err := u.deps.Store.DB().QueryRowContext(ctx,
		`SELECT value FROM system_state WHERE key = ?`, qualKey).Scan(&raw)
	if err != nil {
		return newQualState()
	}
	var s qualState
	if json.Unmarshal([]byte(raw), &s) != nil || s.Results == nil {
		return newQualState()
	}
	return s
}

func (u *UI) saveQualState(ctx context.Context, s qualState) {
	raw, err := json.Marshal(s)
	if err != nil {
		return
	}
	_, _ = u.deps.Store.DB().ExecContext(ctx,
		`INSERT INTO system_state (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, qualKey, string(raw))
}

// qualMeasurementsFromState aggregates the stored case results.
func qualMeasurementsFromState(s qualState) []onboard.Measurement {
	type acc struct{ total, correct int }
	by := map[string]*acc{}
	for key, correct := range s.Results {
		parts := strings.SplitN(key, "|", 3)
		if len(parts) != 3 {
			continue
		}
		k := parts[0] + "|" + parts[1]
		if by[k] == nil {
			by[k] = &acc{}
		}
		by[k].total++
		if correct {
			by[k].correct++
		}
	}
	var out []onboard.Measurement
	for k, a := range by {
		parts := strings.SplitN(k, "|", 2)
		out = append(out, onboard.Measurement{
			Role: parts[0], Model: parts[1],
			Score: float64(a.correct) / float64(a.total), Samples: a.total,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Role != out[j].Role {
			return out[i].Role < out[j].Role
		}
		return out[i].Model < out[j].Model
	})
	return out
}

// --- Ollama read-only helpers ---

func fetchModels(ctx context.Context, baseURL string) ([]setupModel, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(baseURL, "/")+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned %s", resp.Status)
	}
	var body struct {
		Models []struct {
			Name    string `json:"name"`
			Model   string `json:"model"`
			Size    int64  `json:"size"`
			Details struct {
				Family string `json:"family"`
			} `json:"details"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]setupModel, 0, len(body.Models))
	for _, m := range body.Models {
		name := m.Name
		if name == "" {
			name = m.Model
		}
		if name == "" {
			continue
		}
		out = append(out, setupModel{Name: name, SizeBytes: m.Size, Family: m.Details.Family})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SizeBytes != out[j].SizeBytes {
			return out[i].SizeBytes > out[j].SizeBytes
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func modelInstalled(name string, models []setupModel) bool {
	for _, m := range models {
		if m.Name == name || baseModelName(m.Name) == baseModelName(name) {
			return true
		}
	}
	return false
}

func onboardModelInstalled(name string, models []onboard.Model) bool {
	for _, m := range models {
		if m.Name == name || baseModelName(m.Name) == baseModelName(name) {
			return true
		}
	}
	return false
}

func baseModelName(s string) string {
	if i := strings.IndexByte(s, ':'); i >= 0 {
		return s[:i]
	}
	return s
}

// ollamaChat is one bounded, non-thinking, temperature-0 call for the screen.
func ollamaChat(ctx context.Context, baseURL, model, prompt string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"model": model, "stream": false, "think": false,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
		"options":  map[string]any{"temperature": 0, "num_predict": 256, "num_ctx": 8192},
	})
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 45 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama returned %s", resp.Status)
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
