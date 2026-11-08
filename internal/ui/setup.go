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

// qualTTL bounds how long a persisted "running" flag is displayed as live
// when no worker can own it. It is a display hint only: it never discards
// completed cases. Reconcile clears the flag at boot and the qualification
// step resumes the interrupted run.
const qualTTL = 2 * time.Hour

// qualCaseTimeout bounds one case. The first case for a model includes a cold
// load; a 30B model on a slow disk routinely exceeds the old 45s ceiling. A
// timeout is an infrastructure error, not a wrong answer, so the run stops
// and leaves the case pending instead of recording a failure.
const qualCaseTimeout = 5 * time.Minute

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
	// MinimumMinutes is the operator's minimum total run time; 0 means
	// core-only. The core may exceed it. Phase is "core" or "extra".
	MinimumMinutes int    `json:"minimum_minutes,omitempty"`
	Phase          string `json:"phase,omitempty"`
	// Tier is the operator's chosen depth: minimal (two cases per capability),
	// balanced (the standard ladder), or complete (every deep case). It is
	// saved so a Core restart resumes the same depth.
	Tier         string  `json:"tier,omitempty"`
	CoreComplete bool    `json:"core_complete,omitempty"` // the Minimal plan is complete
	CoreSeconds  float64 `json:"core_seconds,omitempty"`
	// FirstModel is the first screened model and its core time; the extra
	// budget is estimated from this pass rather than waiting for every model.
	FirstModel           string   `json:"first_model,omitempty"`
	FirstModelSeconds    float64  `json:"first_model_seconds,omitempty"`
	EstimatedCoreSeconds float64  `json:"estimated_core_seconds,omitempty"`
	Models               []string `json:"models,omitempty"`
	// CurrentModel is the model being measured right now. The worker persists
	// it before the first case so the UI can show activity while a large model
	// loads, instead of a silent zero.
	CurrentModel string `json:"current_model,omitempty"`
	// Waiting is set while a running job holds inference; the screen yields
	// rather than evicting the job's model.
	Waiting bool `json:"waiting,omitempty"`
	// StartDone is len(Results) when the current attempt began. The running
	// clock estimate divides by cases measured in this attempt, not by the
	// whole resumable history.
	StartDone int `json:"start_done,omitempty"`
}

// qualCaseRecord is one completed case: what was asked, what was expected,
// what the model answered, and whether it passed. It is the inspection
// surface behind the score.
type qualCaseRecord struct {
	Kind       string `json:"kind"`
	CaseID     string `json:"case_id"`
	Model      string `json:"model,omitempty"`
	Capability string `json:"capability,omitempty"`
	Prompt     string `json:"prompt,omitempty"`
	Want       string `json:"want,omitempty"`
	Answer     string `json:"answer,omitempty"`
	Correct    bool   `json:"correct"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	// OutputTokens, EvalDurationMS, and TokensPerSecond come from Ollama's
	// eval counters. They are optional for records written before speed was
	// measured.
	OutputTokens    int     `json:"output_tokens,omitempty"`
	EvalDurationMS  int64   `json:"eval_duration_ms,omitempty"`
	TokensPerSecond float64 `json:"tokens_per_second,omitempty"`
	Error           string  `json:"error,omitempty"`
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
	Running          bool
	Done             int
	Total            int
	Recorded         int
	Percent          int
	Elapsed          string
	Remaining        string
	Error            string
	Stale            bool
	Interrupted      bool
	Complete         bool
	CoreComplete     bool
	CanExtend        bool
	MinimumMinutes   int
	Phase            string
	Estimate         string
	MinimumMet       bool
	MinimumRemaining string
	AllCases         bool
	HasMeasurements  bool
	// Per-tier estimates shown beside the depth choices. "done" means no case
	// remains for that depth; "estimating…" means the first model has not
	// completed its core pass yet.
	MinimalEstimate  string
	BalancedEstimate string
	CompleteEstimate string
	CompleteCases    int
	// CoreRemaining is the first-model estimate projected against the clock,
	// shown while the core is still running.
	CoreRemaining string
	CurrentModel  string
	Tier          string
	Waiting       bool
}

// qualificationViewFor builds the progress view. A run with no plan yet
// (Total == 0) renders as "starting"; a finished run with every case done is
// 100% even if the worker has not persisted Running=false yet.
func qualificationViewFor(s qualState, hasMeasurements bool) qualificationView {
	v := qualificationView{
		Running: s.Running, Done: s.Done, Total: s.Total, Recorded: len(s.Results),
		Error: s.Error, Stale: s.stale(), HasMeasurements: hasMeasurements,
		Interrupted:  !s.Running && !s.StartedAt.IsZero() && s.FinishedAt.IsZero(),
		CoreComplete: s.CoreComplete, MinimumMinutes: s.MinimumMinutes, Phase: s.Phase,
		CurrentModel: s.CurrentModel, Tier: normalizeQualTier(s.Tier), Waiting: s.Waiting,
	}
	if s.CoreSeconds > 0 {
		v.Estimate = time.Duration(s.CoreSeconds * float64(time.Second)).Round(time.Second).String()
	} else if s.EstimatedCoreSeconds > 0 {
		v.Estimate = time.Duration(s.EstimatedCoreSeconds * float64(time.Second)).Round(time.Second).String()
	}
	v.AllCases = v.Total > 0 && v.Done >= v.Total
	if s.MinimumMinutes > 0 && !s.StartedAt.IsZero() {
		minimum := time.Duration(s.MinimumMinutes) * time.Minute
		minimumEnd := s.StartedAt.Add(minimum)
		now := time.Now()
		if !s.Running && !s.FinishedAt.IsZero() {
			now = s.FinishedAt
		}
		v.MinimumMet = !now.Before(minimumEnd)
		if s.Running && now.Before(minimumEnd) {
			v.MinimumRemaining = minimumEnd.Sub(now).Round(time.Second).String()
		}
	} else {
		v.MinimumMet = s.MinimumMinutes == 0
	}
	v.CanExtend = s.CoreComplete && !s.Running
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
			// A resumed run starts with completed cases and zero elapsed
			// time; a sub-second projection is noise, not an estimate.
			if remaining >= time.Second {
				v.Remaining = remaining.Round(time.Second).String()
			}
		}
	}
	if s.Running && s.EstimatedCoreSeconds > 0 && !s.StartedAt.IsZero() {
		left := time.Duration(s.EstimatedCoreSeconds*float64(time.Second)) - time.Since(s.StartedAt)
		if left > 0 {
			v.CoreRemaining = humanETA(left)
		}
	}
	v.Complete = !s.Running && s.CoreComplete && (v.MinimumMet || v.AllCases)
	return v
}

// qualificationViewForPool projects progress and estimates onto the selected
// depth's plan. It computes the denominator from the plan, not from the last
// persisted state.Total, so switching depth changes the percentage
// immediately and never reports more than 100%.
func qualificationViewForPool(s qualState, models []string, tier string, hasMeasurements bool) qualificationView {
	v := qualificationViewFor(s, hasMeasurements)
	v.Tier = normalizeQualTier(tier)
	v.Remaining = ""
	if len(models) == 0 {
		return v
	}
	defaultLadder := onboard.DefaultLadder()
	minimalPlan := onboard.QualificationMinimal(defaultLadder)
	balancedPlan := onboard.AllCaseRefs(defaultLadder)
	completePlan := onboard.AllCaseRefs(onboard.DeepLadder())
	plan := minimalPlan
	switch v.Tier {
	case qualTierBalanced:
		plan = balancedPlan
	case qualTierComplete:
		plan = completePlan
	}
	v.Total = len(models) * len(plan)
	v.Done = donePairs(plan, s.Results, models)
	v.Recorded = len(s.Results)
	v.CoreComplete = coreCoverage(minimalPlan, models, s.Results)
	v.AllCases = v.Total > 0 && v.Done >= v.Total
	v.Complete = !s.Running && v.Total > 0 && v.Done >= v.Total
	v.Percent = 0
	if v.Total > 0 {
		v.Percent = v.Done * 100 / v.Total
		if v.Percent > 100 {
			v.Percent = 100
		}
	}
	perCase := observedPerCase(&s)
	if perCase <= 0 {
		v.MinimalEstimate, v.BalancedEstimate, v.CompleteEstimate = "estimating…", "estimating…", "estimating…"
		v.CompleteCases = missingPairs(completePlan, s.Results, models)
		return v
	}
	missing := missingPairs(plan, s.Results, models)
	if s.Running && missing > 0 {
		v.Remaining = humanETA(perCase * time.Duration(missing))
	}
	v.MinimalEstimate = tierETA(perCase, missingPairs(minimalPlan, s.Results, models))
	v.BalancedEstimate = tierETA(perCase, missingPairs(balancedPlan, s.Results, models))
	v.CompleteEstimate = tierETA(perCase, missingPairs(completePlan, s.Results, models))
	v.CompleteCases = missingPairs(completePlan, s.Results, models)
	return v
}

// observedPerCase averages completed case durations and prefers the running
// clock once there are enough samples, so a fast first case does not make the
// whole screen look cheap.
func observedPerCase(s *qualState) time.Duration {
	var total time.Duration
	n := 0
	for _, rec := range s.Details {
		if rec.DurationMS > 0 {
			total += time.Duration(rec.DurationMS) * time.Millisecond
			n++
		}
	}
	var per time.Duration
	if n > 0 {
		per = total / time.Duration(n)
	}
	if s.Running && !s.StartedAt.IsZero() {
		// Count only cases measured in this attempt. Dividing the current
		// session's clock by the whole resumable history made a resumed run
		// look almost instantaneous.
		newCases := len(s.Results) - s.StartDone
		if newCases >= 5 {
			observed := time.Since(s.StartedAt) / time.Duration(newCases)
			if observed > per {
				per = observed
			}
		}
	}
	return per
}

func tierETA(perCase time.Duration, pending int) string {
	if pending == 0 {
		return "done"
	}
	if perCase <= 0 {
		return "estimating…"
	}
	return "about " + humanETA(perCase*time.Duration(pending))
}

func humanETA(d time.Duration) string {
	if d < time.Minute {
		return d.Round(time.Second).String()
	}
	minutes := int(d.Round(time.Minute) / time.Minute)
	if minutes < 60 {
		return fmt.Sprintf("%d min", minutes)
	}
	return fmt.Sprintf("%dh%02dm", minutes/60, minutes%60)
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
		}
		data["OllamaURL"] = cfg.Inference.OllamaURL
	}
	state := u.loadQualState(r.Context())
	data["Models"] = models
	data["Roles"] = roles
	data["Applied"] = r.URL.Query().Get("applied")
	data["Stale"] = state.stale()
	hasMeasurements := len(state.Results) > 0
	data["HasMeasurements"] = hasMeasurements
	tier := normalizeQualTier(state.Tier)
	if u.qualificationRunning() {
		tier = u.currentQualTier()
	}
	poolModels := []string{}
	if p := u.pool(); p != nil {
		poolModels = p.EligibleNames()
		data["QualDetails"] = qualDetailsFromState(state, poolModels)
		pool := u.welcomePool(models)
		data["PoolRanked"] = pool.Ranked
		data["PoolCount"] = pool.Count
		data["QualifiedCount"] = pool.QualifiedCount
	}
	data["Tier"] = tier
	data["Qualification"] = qualificationViewForPool(state, poolModels, tier, hasMeasurements)
	if u.deps.ModelAudit != "" {
		var audit onboard.Audit
		if err := json.Unmarshal([]byte(u.deps.ModelAudit), &audit); err == nil {
			data["ModelAudit"] = audit
			data["ModelAuditFailing"] = audit.FailingRoles()
		}
	}
	u.render(w, "Setup", setupBody, data)
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
	if u.deps.Config == nil {
		u.fail(w, fmt.Errorf("configuration is not available to the UI"))
		return
	}
	_ = r.ParseForm()
	tier := normalizeQualTier(r.FormValue("tier"))
	if strings.TrimSpace(r.FormValue("tier")) == "" {
		tier = u.loadQualState(r.Context()).Tier
	}
	u.setQualTier(tier)
	if _, _, err := u.startPoolQualification(r.Context(), r.FormValue("fresh") != "", tier); err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui/setup", http.StatusSeeOther)
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
	modelSet := map[string]bool{}
	for key := range s.Results {
		if parts := strings.SplitN(key, "|", 3); len(parts) == 3 {
			modelSet[parts[1]] = true
		}
	}
	models := make([]string, 0, len(modelSet))
	for m := range modelSet {
		models = append(models, m)
	}
	balanced := onboard.BalancedCaseKeys(s.Results, models)
	for key, correct := range s.Results {
		parts := strings.SplitN(key, "|", 3)
		if len(parts) != 3 {
			continue
		}
		if !balanced[parts[0]+"|"+parts[2]] {
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

func baseModelName(s string) string {
	if i := strings.IndexByte(s, ':'); i >= 0 {
		return s[:i]
	}
	return s
}

// chatResult is the content plus Ollama's decode counters.
type chatResult struct {
	Content         string
	EvalCount       int
	EvalDurationNS  int64
	PromptEvalCount int
}

// ollamaChat is one bounded, non-thinking, temperature-0 call for the screen.
func ollamaChatResult(ctx context.Context, baseURL, model, prompt string) (chatResult, error) {
	body, err := json.Marshal(map[string]any{
		"model": model, "stream": false, "think": false,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
		"options":  map[string]any{"temperature": 0, "num_predict": 256, "num_ctx": 8192},
	})
	if err != nil {
		return chatResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, qualCaseTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return chatResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: qualCaseTimeout}).Do(req)
	if err != nil {
		return chatResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return chatResult{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return chatResult{}, fmt.Errorf("ollama returned %s", resp.Status)
	}
	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		EvalCount       int    `json:"eval_count"`
		EvalDuration    int64  `json:"eval_duration"`
		PromptEvalCount int    `json:"prompt_eval_count"`
		Error           string `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return chatResult{}, err
	}
	if out.Error != "" {
		return chatResult{}, fmt.Errorf("%s", out.Error)
	}
	return chatResult{
		Content:         out.Message.Content,
		EvalCount:       out.EvalCount,
		EvalDurationNS:  out.EvalDuration,
		PromptEvalCount: out.PromptEvalCount,
	}, nil
}
