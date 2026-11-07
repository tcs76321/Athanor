package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/onboard"
	"github.com/tcs76321/athanor/internal/store"
)

// qualKey is the `system_state` key for the UI's bounded qualification run.
// It survives a Core restart; a stale "running" flag is ignored after qualTTL.
const qualKey = "ui:qualification"

// qualTTL bounds how long a persisted "running" flag is trusted. A Core
// restart cannot resume the goroutine, so a stale run is restartable.
const qualTTL = 2 * time.Hour

// qualState is the resumable qualification state.
type qualState struct {
	Running    bool            `json:"running"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt time.Time       `json:"finished_at"`
	Total      int             `json:"total"`
	Done       int             `json:"done"`
	Results    map[string]bool `json:"results"`
	// Details is the per-case evidence behind Results. It is optional for
	// state written before this field existed; the UI degrades to the summary.
	Details map[string]qualCaseRecord `json:"details,omitempty"`
	Error   string                    `json:"error,omitempty"`
	Deep    bool                      `json:"deep,omitempty"`
}

// qualCaseRecord is one completed case: what was asked, what was expected,
// what the model answered, and whether it passed. It is the inspection
// surface behind the score.
type qualCaseRecord struct {
	Kind       string `json:"kind"`
	CaseID     string `json:"case_id"`
	Capability string `json:"capability,omitempty"`
	Prompt     string `json:"prompt,omitempty"`
	Want       string `json:"want,omitempty"`
	Answer     string `json:"answer,omitempty"`
	Correct    bool   `json:"correct"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

func newQualState() qualState {
	return qualState{Results: map[string]bool{}, Details: map[string]qualCaseRecord{}}
}

func (s qualState) stale() bool {
	return s.Running && time.Since(s.StartedAt) > qualTTL
}

// qualificationView is the wizard-facing progress snapshot. The wizard polls
// the page while a run is active; the percent and estimate are derived from
// the persisted case count, not a separate in-memory counter.
type qualificationView struct {
	Running         bool
	Done            int
	Total           int
	Percent         int
	Elapsed         string
	Remaining       string
	Error           string
	Stale           bool
	Complete        bool
	HasMeasurements bool
}

// qualificationViewFor builds the progress view. A run with no plan yet
// (Total == 0) renders as "starting"; a finished run with every case done is
// 100% even if the worker has not persisted Running=false yet.
func qualificationViewFor(s qualState, hasMeasurements bool) qualificationView {
	v := qualificationView{
		Running: s.Running, Done: s.Done, Total: s.Total,
		Error: s.Error, Stale: s.stale(), HasMeasurements: hasMeasurements,
	}
	if v.Total > 0 {
		v.Percent = v.Done * 100 / v.Total
		if v.Percent > 100 {
			v.Percent = 100
		}
	}
	if s.Running && v.Total > 0 && v.Done >= v.Total {
		v.Percent = 100
	}
	if !s.StartedAt.IsZero() {
		end := time.Now()
		if !s.Running && !s.FinishedAt.IsZero() {
			end = s.FinishedAt
		}
		elapsed := end.Sub(s.StartedAt)
		if elapsed > 0 {
			v.Elapsed = elapsed.Round(time.Second).String()
		}
		if s.Running && v.Done > 0 && v.Total > v.Done {
			perCase := elapsed / time.Duration(v.Done)
			remaining := perCase * time.Duration(v.Total-v.Done)
			if remaining > 0 {
				v.Remaining = remaining.Round(time.Second).String()
			}
		}
	}
	v.Complete = !s.Running && v.Total > 0 && v.Done >= v.Total
	return v
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
	// FileBytes is the on-disk weight size; SizeBytes may be the larger
	// measured resident size from an audit.
	FileBytes       int64
	Family          string
	NativeContext   int
	KVBytesPerToken int64
	// Measured is true when SizeBytes came from the audit's actual loaded
	// size (weights + KV) rather than the model file on disk.
	Measured bool
}

// handleSetupPersonas writes an operator-chosen routing table. The onboarding
// wizard could already apply an automatic assignment; after setup there was no
// way to change it without editing config.yaml. This is the same writer, with
// explicit per-role choices and installed-model validation.
func (u *UI) handleSetupPersonas(w http.ResponseWriter, r *http.Request) {
	if u.deps.Config == nil || u.deps.ConfigPath == "" {
		u.fail(w, fmt.Errorf("configuration is not available to the UI"))
		return
	}
	if err := r.ParseForm(); err != nil {
		u.fail(w, err)
		return
	}
	models, err := u.modelCatalog(r.Context())
	if err != nil {
		u.fail(w, fmt.Errorf("reading installed models: %w", err))
		return
	}
	updates := map[string]config.PersonaConfig{}
	for _, role := range []string{"wide", "tall", "main", "security", "alternative"} {
		model := strings.TrimSpace(r.FormValue("model_" + role))
		if model == "" {
			continue
		}
		if !modelInstalled(model, models) {
			u.fail(w, fmt.Errorf("model %q is not installed", model))
			return
		}
		pc, ok := u.deps.Config.Personas.Role(role)
		if !ok {
			pc = config.PersonaConfig{ContextTarget: 8192}
		}
		pc.Model = model
		pc.ContextTarget = parseFormInt(r, "context_"+role, pc.ContextTarget)
		if pc.ContextTarget < 2048 {
			u.fail(w, fmt.Errorf("%s context target must be at least 2048", role))
			return
		}
		for _, m := range models {
			if m.Name == model {
				if m.Family != "" {
					pc.Family = m.Family
				}
				break
			}
		}
		updates[role] = pc
	}
	if len(updates) == 0 {
		u.fail(w, fmt.Errorf("choose at least one model"))
		return
	}
	if err := config.UpdatePersonas(u.deps.ConfigPath, updates); err != nil {
		u.fail(w, err)
		return
	}
	if u.deps.Store != nil {
		data := map[string]any{"event": "personas_applied", "source": "setup"}
		for role, pc := range updates {
			data[role] = pc.Model
		}
		_, _ = u.deps.Store.AppendEvent(r.Context(), store.Event{Category: "inference", Data: data})
	}
	if u.deps.Restart != nil {
		u.writeRestartPage(w, "/ui/setup?applied=personas")
		go func() {
			time.Sleep(1500 * time.Millisecond)
			u.deps.Restart()
		}()
		return
	}
	u.markRestartRequired(r.Context(), "persona routing changed")
	http.Redirect(w, r, "/ui/setup?applied=personas", http.StatusSeeOther)
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
		models, err = u.modelCatalog(r.Context())
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
	data["RolePicks"] = u.rolePicks()
	data["Applied"] = r.URL.Query().Get("applied")
	data["Stale"] = stale
	hasMeasurements := false
	if cfg != nil {
		ms := qualMeasurementsFromState(state)
		hasMeasurements = len(ms) > 0
		data["Measurements"] = ms
		if p := u.pool(); p != nil {
			data["QualDetails"] = qualDetailsFromState(state, p.EligibleNames())
		}
		if len(ms) > 0 {
			recs := onboard.Route(setupOnboardRoles(roles), families, ms, setupRoutingOptions())
			data["Recommendations"] = recs
			data["Apply"] = applyYAML(recs)
		}
	}
	data["Qualification"] = qualificationViewFor(state, hasMeasurements)
	if u.deps.ModelAudit != "" {
		var audit onboard.Audit
		if err := json.Unmarshal([]byte(u.deps.ModelAudit), &audit); err == nil {
			data["ModelAudit"] = audit
			data["ModelAuditFailing"] = audit.FailingRoles()
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

func (u *UI) handleQualifyCancel(w http.ResponseWriter, r *http.Request) {
	u.cancelQualification()
	next := r.FormValue("next")
	if !strings.HasPrefix(next, "/ui/") {
		next = "/ui/setup"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (u *UI) handleQualifyStart(w http.ResponseWriter, r *http.Request) {
	cfg := u.deps.Config
	if cfg == nil {
		u.fail(w, fmt.Errorf("configuration is not available to the UI"))
		return
	}
	_ = r.ParseForm()
	if _, _, err := u.startQualification(r.Context(), cfg, r.FormValue("fresh") != ""); err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui/setup", http.StatusSeeOther)
}

// startQualification initializes a bounded run, persists the visible state
// before returning, then starts the worker. Persisting first removes the race
// where the redirect renders the "Start" button again before the goroutine has
// written Running=true.
func (u *UI) startQualification(ctx context.Context, cfg *config.Config, fresh bool) (qualState, bool, error) {
	if cfg == nil {
		return qualState{}, false, fmt.Errorf("configuration is not available to the UI")
	}
	state := u.loadQualState(ctx)
	if state.Running && !state.stale() {
		return state, false, nil
	}
	if state.stale() || fresh {
		state = newQualState()
	}
	u.qualMu.Lock()
	if u.qualRunning {
		u.qualMu.Unlock()
		return state, false, nil
	}
	u.qualRunning = true
	u.qualMu.Unlock()

	roles, models := setupRolesAndModels(cfg)
	state.Running = true
	state.StartedAt = time.Now()
	state.Error = ""
	if state.Results == nil {
		state.Results = map[string]bool{}
	}
	state.Done = len(state.Results)
	state.Total = qualificationPlan(roles, models)
	u.saveQualState(ctx, state)
	runState := state
	if state.Results != nil {
		runState.Results = make(map[string]bool, len(state.Results))
		for k, v := range state.Results {
			runState.Results[k] = v
		}
	}
	go func(runState qualState) {
		defer func() {
			u.qualMu.Lock()
			u.qualRunning = false
			u.qualMu.Unlock()
		}()
		u.runQualification(context.Background(), cfg, roles, models, &runState)
	}(state)
	return state, true, nil
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
	raw, ok := u.loadSystemState(ctx, qualKey)
	if !ok {
		return newQualState()
	}
	var s qualState
	if json.Unmarshal([]byte(raw), &s) != nil || s.Results == nil {
		return newQualState()
	}
	if s.Details == nil {
		s.Details = map[string]qualCaseRecord{}
	}
	return s
}

func (u *UI) saveQualState(ctx context.Context, s qualState) {
	raw, err := json.Marshal(s)
	if err != nil {
		return
	}
	_ = u.saveSystemState(ctx, qualKey, string(raw))
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

// catalogTTL bounds how long an installed-model snapshot is reused. Pulls and
// external `ollama pull` calls are rare relative to page renders, so a short
// TTL keeps the pages honest without making them hammer the inference server.
const catalogTTL = 15 * time.Second

// modelCatalog returns the installed models, reusing a recent snapshot. The
// first reader after the TTL performs the fetch; a failed fetch is cached for
// the same window so a down Ollama does not turn every page load into a
// timeout.
func (u *UI) modelCatalog(ctx context.Context) ([]setupModel, error) {
	base := ""
	if u.deps.Config != nil {
		base = u.deps.Config.Inference.OllamaURL
	}
	u.catalogMu.Lock()
	if u.catalogBase == base && !u.catalogAt.IsZero() && time.Since(u.catalogAt) < catalogTTL {
		models := append([]setupModel(nil), u.catalog...)
		errText := u.catalogErr
		u.catalogMu.Unlock()
		if errText != "" {
			return nil, errors.New(errText)
		}
		return models, nil
	}
	u.catalogMu.Unlock()

	models, err := fetchModels(ctx, base)
	u.catalogMu.Lock()
	u.catalogBase = base
	u.catalogAt = time.Now()
	if err != nil {
		u.catalogErr = err.Error()
		u.catalog = nil
		u.catalogMu.Unlock()
		return nil, err
	}
	u.catalog = append([]setupModel(nil), models...)
	u.catalogErr = ""
	u.catalogMu.Unlock()
	return models, nil
}

// invalidateCatalog drops the snapshot after an install or removal so the next
// render reflects the change immediately.
func (u *UI) invalidateCatalog() {
	u.catalogMu.Lock()
	u.catalogAt = time.Time{}
	u.catalogMu.Unlock()
}

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
		native, kvPerToken := fetchModelContextMetadata(ctx, baseURL, name)
		out = append(out, setupModel{
			Name: name, SizeBytes: m.Size, FileBytes: m.Size,
			Family: m.Details.Family, NativeContext: native, KVBytesPerToken: kvPerToken,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SizeBytes != out[j].SizeBytes {
			return out[i].SizeBytes > out[j].SizeBytes
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// fetchModelContextMetadata reads native context and estimates KV bytes per
// token from Ollama's model_info. Missing metadata is not an error: the pool
// falls back to qualification measurements or a size tier.
func fetchModelContextMetadata(ctx context.Context, baseURL, name string) (native int, kvPerToken int64) {
	cctx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	raw, _ := json.Marshal(map[string]string{"model": name})
	req, err := http.NewRequestWithContext(cctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/api/show", bytes.NewReader(raw))
	if err != nil {
		return 0, 0
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 750 * time.Millisecond}).Do(req)
	if err != nil {
		return 0, 0
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, 0
	}
	var body struct {
		ModelInfo map[string]any `json:"model_info"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return 0, 0
	}
	get := func(suffix string) float64 {
		for k, v := range body.ModelInfo {
			if strings.HasSuffix(k, suffix) {
				if f, ok := v.(float64); ok {
					return f
				}
			}
		}
		return 0
	}
	native = int(get(".context_length"))
	layers := get(".block_count")
	heads := get(".attention.head_count")
	kvHeads := get(".attention.head_count_kv")
	embedding := get(".embedding_length")
	if native <= 0 || layers <= 0 || heads <= 0 || kvHeads <= 0 || embedding <= 0 {
		return native, 0
	}
	headDim := embedding / heads
	// K and V, two bytes per element at Ollama's default fp16 KV cache.
	kvPerToken = int64(layers * kvHeads * headDim * 2 * 2)
	return native, kvPerToken
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
