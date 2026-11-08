package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/composition"
	"github.com/tcs76321/athanor/internal/onboard"
)

const (
	qualTierMinimal  = "minimal"
	qualTierBalanced = "balanced"
	qualTierComplete = "complete"
)

func normalizeQualTier(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "minimal", "short", "core":
		return qualTierMinimal
	case "complete", "full":
		return qualTierComplete
	default:
		// Balanced is the recommended depth and the default for a fresh Core.
		return qualTierBalanced
	}
}

// startPoolQualification runs the chosen depth over every model in the user's
// pool. Minimal runs two cases per capability, Balanced runs the standard
// ladder, and Complete runs every case in the deep ladder. Completed cases
// calibrate the estimates. The legacy two-role ladder remains available on
// /ui/setup.
func (u *UI) startPoolQualification(ctx context.Context, fresh bool, tier string) (qualState, bool, error) {
	p := u.pool()
	if p == nil {
		return qualState{}, false, fmt.Errorf("model pool is not available")
	}
	if u.deps.Config == nil {
		return qualState{}, false, fmt.Errorf("configuration is not available")
	}
	models := p.EligibleNames()
	if len(models) == 0 {
		// A direct "run qualification" before the pool step has synced: take
		// the installed catalog as the initial pool rather than failing.
		installed, err := u.modelCatalog(ctx)
		if err != nil {
			return qualState{}, false, fmt.Errorf("reading installed models: %w", err)
		}
		if u.deps.Hardware != nil {
			hw := u.deps.Hardware(ctx)
			p.FitLimitBytes = inferenceFitBytes(hw, hostReserveBytes(u.deps.Config, hw))
		}
		infos := make([]composition.ModelInfo, 0, len(installed))
		for _, m := range installed {
			if !isChatSetupModel(m) {
				continue
			}
			maxCtx := maxContextForModel(m, p.FitLimitBytes, 0)
			infos = append(infos, composition.ModelInfo{
				Name: m.Name, Family: m.Family, SizeBytes: m.SizeBytes, FileBytes: m.FileBytes,
				NativeContext: m.NativeContext, KVBytesPerToken: m.KVBytesPerToken,
				MaxContext: maxCtx, ContextTarget: poolContextTarget(u.deps.Config, m.Name, m.SizeBytes, maxCtx),
			})
		}
		p.Sync(infos)
		models = p.EligibleNames()
	}
	if len(models) == 0 {
		return qualState{}, false, fmt.Errorf("add at least one model to the pool before qualifying")
	}
	tier = normalizeQualTier(tier)
	deep := tier == qualTierComplete
	state := u.loadQualState(ctx)
	if state.Running && !state.stale() {
		return state, false, nil
	}
	if state.stale() || fresh {
		state = newQualState()
	}
	if !sameStrings(state.Models, models) {
		// A changed pool invalidates the comparison baseline: the new model has
		// not run the same cases as the old set.
		state.CoreComplete = false
		state.FirstModel = ""
		state.FirstModelSeconds = 0
		state.EstimatedCoreSeconds = 0
		state.CoreSeconds = 0
		state.Models = append([]string(nil), models...)
	}
	u.qualMu.Lock()
	if u.qualRunning {
		u.qualMu.Unlock()
		return state, false, nil
	}
	u.qualRunning = true
	u.qualTier = tier
	u.qualLastSave = time.Time{}
	runCtx, cancel := context.WithCancel(context.Background())
	u.qualCancel = cancel
	u.qualMu.Unlock()

	state.Running = true
	state.StartedAt = time.Now()
	state.FinishedAt = time.Time{}
	state.Error = ""
	state.Deep = deep
	state.Phase = tier
	state.Tier = tier
	state.MinimumMinutes = 0
	if state.Results == nil {
		state.Results = map[string]bool{}
	}
	_, plan := tierPlan(tier)
	state.Total = len(models) * len(plan)
	state.Done = donePairs(plan, state.Results, models)
	minimalPlan := onboard.QualificationMinimal(onboard.DefaultLadder())
	state.CoreComplete = coreCoverage(minimalPlan, models, state.Results)
	if len(models) > 0 {
		state.CurrentModel = models[0]
	}
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
			u.qualCancel = nil
			u.qualMu.Unlock()
			cancel()
		}()
		u.runPoolQualification(runCtx, models, &runState)
	}(runState)
	return state, true, nil
}

// setQualTier records the operator's live depth choice. The worker reads it
// between cases, so a change applies without cancelling the current run.
func (u *UI) setQualTier(tier string) {
	tier = normalizeQualTier(tier)
	u.qualMu.Lock()
	u.qualTier = tier
	u.qualMu.Unlock()
}

func (u *UI) currentQualTier() string {
	u.qualMu.Lock()
	defer u.qualMu.Unlock()
	if u.qualTier == "" {
		return qualTierBalanced
	}
	return u.qualTier
}

func (u *UI) qualificationRunning() bool {
	u.qualMu.Lock()
	defer u.qualMu.Unlock()
	return u.qualRunning
}

// saveQualStateThrottled persists the run at most once per second. Case
// evidence is large, and a long screen would otherwise spend much of its time
// rewriting the same growing JSON document.
func (u *UI) saveQualStateThrottled(ctx context.Context, s qualState) {
	u.qualMu.Lock()
	if !u.qualLastSave.IsZero() && time.Since(u.qualLastSave) < time.Second {
		u.qualMu.Unlock()
		return
	}
	u.qualLastSave = time.Now()
	u.qualMu.Unlock()
	u.saveQualState(ctx, s)
}

// setQualificationTier applies the operator's depth choice. If a run is
// active, the worker picks it up between cases. Otherwise it starts (or
// continues) at that depth, keeping completed cases.
func (u *UI) setQualificationTier(ctx context.Context, tier string) error {
	tier = normalizeQualTier(tier)
	u.setQualTier(tier)
	if u.qualificationRunning() {
		return nil
	}
	state := u.loadQualState(ctx)
	state.Tier = tier
	u.saveQualState(ctx, state)
	switch tier {
	case qualTierMinimal:
		if state.CoreComplete {
			return nil
		}
		_, _, err := u.startPoolQualification(ctx, false, qualTierMinimal)
		return err
	default:
		_, _, err := u.startPoolQualification(ctx, false, tier)
		return err
	}
}

// reconcileInterruptedQualification clears a persisted running flag that no
// goroutine can own after a Core restart. Completed cases are kept, and the
// qualification step resumes from them when it is opened. FinishedAt stays
// zero so this is distinguishable from an operator stop.
func (u *UI) reconcileInterruptedQualification() {
	if u.deps.Store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st := u.loadQualState(ctx)
	if !st.Running {
		return
	}
	st.Running = false
	u.saveQualState(ctx, st)
}

// cancelQualification stops the in-flight screen. The run is resumable: the
// completed case keys stay in state, so starting again continues from there.
func (u *UI) cancelQualification() {
	u.qualMu.Lock()
	cancel := u.qualCancel
	u.qualMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func poolQualificationPlan(models []string, ladder onboard.Ladder) int {
	return len(models) * (len(ladder.Generators) + len(ladder.Judges))
}

// tierPlan returns the ladder and case set for a depth. Balanced is the whole
// standard ladder; Complete is the whole deep ladder.
func tierPlan(tier string) (onboard.Ladder, []onboard.CaseRef) {
	defaultLadder := onboard.DefaultLadder()
	switch normalizeQualTier(tier) {
	case qualTierComplete:
		deep := onboard.DeepLadder()
		return deep, onboard.AllCaseRefs(deep)
	case qualTierBalanced:
		return defaultLadder, onboard.AllCaseRefs(defaultLadder)
	default:
		return defaultLadder, onboard.QualificationMinimal(defaultLadder)
	}
}

func (u *UI) runPoolQualification(ctx context.Context, models []string, state *qualState) {
	state.Running = true
	state.Error = ""
	if state.Results == nil {
		state.Results = map[string]bool{}
	}
	if state.Details == nil {
		state.Details = map[string]qualCaseRecord{}
	}
	defaultLadder := onboard.DefaultLadder()
	deepLadder := onboard.DeepLadder()
	minimalPlan := onboard.QualificationMinimal(defaultLadder)
	balancedPlan := onboard.AllCaseRefs(defaultLadder)
	completePlan := onboard.AllCaseRefs(deepLadder)
	planFor := func(tier string) (onboard.Ladder, []onboard.CaseRef) {
		switch normalizeQualTier(tier) {
		case qualTierComplete:
			return deepLadder, completePlan
		case qualTierBalanced:
			return defaultLadder, balancedPlan
		default:
			return defaultLadder, minimalPlan
		}
	}
	runCases := func(model string, refs []onboard.CaseRef, expectedTier string, l onboard.Ladder) bool {
		for _, ref := range refs {
			if ctx.Err() != nil {
				return false
			}
			// A live depth change stops the current pass between cases, so a
			// switch to Balanced or Complete does not wait for the current pass.
			if expectedTier != "" && u.currentQualTier() != expectedTier {
				return false
			}
			u.runQualCase(ctx, model, ref, l, state)
		}
		return true
	}

	// Qualification owns model residency. Evict anything left from an earlier
	// run before the first load, then keep exactly one model resident by
	// unloading each model before moving to the next.
	for _, model := range models {
		ollamaUnload(context.Background(), u.deps.Config.Inference.OllamaURL, model)
	}

	// The selected plan is the unit of work. Every depth is a superset of the
	// one below it, so a live switch only adds (or drops) missing cases and
	// never discards completed work. A sweep is model-major: one model is
	// loaded, all of its pending cases run, and it is evicted before the next.
	for ctx.Err() == nil {
		tier := u.currentQualTier()
		ladder, plan := planFor(tier)
		state.Tier = tier
		state.Deep = tier == qualTierComplete
		state.Total = len(models) * len(plan)
		state.CoreComplete = coreCoverage(minimalPlan, models, state.Results)
		u.saveQualState(context.Background(), *state)

		ran := false
		for _, model := range models {
			if ctx.Err() != nil {
				break
			}
			pending := pendingForModel(plan, state.Results, model)
			if len(pending) == 0 {
				continue
			}
			ran = true
			state.CurrentModel = model
			u.saveQualState(context.Background(), *state)
			complete := runCases(model, pending, tier, ladder)
			ollamaUnload(context.Background(), u.deps.Config.Inference.OllamaURL, model)
			if !complete {
				break
			}
			state.Done = donePairs(plan, state.Results, models)
			state.CoreComplete = coreCoverage(minimalPlan, models, state.Results)
			u.saveQualState(context.Background(), *state)
		}
		if !ran {
			ladder, _ := planFor(u.currentQualTier())
			u.finishPoolQualification(ctx, models, ladder, state)
			return
		}
	}
	ladder, _ := planFor(u.currentQualTier())
	u.finishPoolQualification(ctx, models, ladder, state)
}

// pendingForModel returns the plan cases one model has not done.
func pendingForModel(plan []onboard.CaseRef, results map[string]bool, model string) []onboard.CaseRef {
	out := make([]onboard.CaseRef, 0, len(plan))
	for _, ref := range plan {
		if _, ok := results[ref.Kind+"|"+model+"|"+ref.ID]; !ok {
			out = append(out, ref)
		}
	}
	return out
}

// missingPairs counts the (case, model) runs still required for a plan.
func missingPairs(plan []onboard.CaseRef, results map[string]bool, models []string) int {
	n := 0
	for _, ref := range plan {
		for _, model := range models {
			if _, ok := results[ref.Kind+"|"+model+"|"+ref.ID]; !ok {
				n++
			}
		}
	}
	return n
}

func donePairs(plan []onboard.CaseRef, results map[string]bool, models []string) int {
	total := len(plan) * len(models)
	done := total - missingPairs(plan, results, models)
	if done < 0 {
		return 0
	}
	return done
}

// coreCoverage reports whether the Minimal plan is complete for every model.
func coreCoverage(minimal []onboard.CaseRef, models []string, results map[string]bool) bool {
	if len(models) == 0 || len(minimal) == 0 {
		return false
	}
	return missingPairs(minimal, results, models) == 0
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func (u *UI) finishPoolQualification(ctx context.Context, models []string, ladder onboard.Ladder, state *qualState) {
	state.Running = false
	state.CurrentModel = ""
	state.FinishedAt = time.Now()
	state.CoreComplete = coreCoverage(onboard.QualificationMinimal(onboard.DefaultLadder()), models, state.Results)
	if ctx.Err() == context.Canceled {
		state.Error = "qualification cancelled; start again to resume"
	}
	if len(onboard.BalancedCaseKeys(state.Results, models)) == 0 && state.Error == "" {
		state.Error = "no case was completed by every model; no balanced score"
	}
	u.saveQualState(context.Background(), *state)
	u.recordPoolQualification(context.Background(), models, ladder, *state)
}

// runQualCase runs and records one ladder case. The state key is the same
// kind|model|id key the resumable file uses.
func (u *UI) runQualCase(ctx context.Context, model string, ref onboard.CaseRef, ladder onboard.Ladder, state *qualState) {
	key := ref.Kind + "|" + model + "|" + ref.ID
	if _, done := state.Results[key]; done {
		return
	}
	start := time.Now()
	switch ref.Kind {
	case "generator":
		for _, c := range ladder.Generators {
			if c.ID != ref.ID {
				continue
			}
			answer, err := ollamaChat(ctx, u.deps.Config.Inference.OllamaURL, model, onboard.GenPrompt(c))
			if err != nil && ctx.Err() != nil {
				return
			}
			correct := err == nil && onboard.ScoreGen(c, answer)
			rec := qualCaseRecord{
				Kind: "generator", CaseID: c.ID, Capability: c.Capability,
				Prompt: c.Prompt, Want: c.Want, Answer: answer,
				Correct: correct, DurationMS: time.Since(start).Milliseconds(),
			}
			if err != nil {
				rec.Error = err.Error()
			}
			state.Results[key] = correct
			state.Details[key] = rec
			state.Done = len(state.Results)
			state.Tier = u.currentQualTier()
			u.saveQualStateThrottled(context.Background(), *state)
			return
		}
	case "judge":
		for i, c := range ladder.Judges {
			if c.ID != ref.ID {
				continue
			}
			prompt, want := onboard.JudgePrompt(c, i%2 == 0)
			answer, err := ollamaChat(ctx, u.deps.Config.Inference.OllamaURL, model, prompt)
			if err != nil && ctx.Err() != nil {
				return
			}
			correct := err == nil && onboard.ScoreJudge(answer, want)
			rec := qualCaseRecord{
				Kind: "judge", CaseID: c.ID, Capability: c.Capability,
				Prompt: prompt, Want: want, Answer: answer,
				Correct: correct, DurationMS: time.Since(start).Milliseconds(),
			}
			if err != nil {
				rec.Error = err.Error()
			}
			state.Results[key] = correct
			state.Details[key] = rec
			state.Done = len(state.Results)
			state.Tier = u.currentQualTier()
			u.saveQualStateThrottled(context.Background(), *state)
			return
		}
	}
}

// qualModelDetail is the per-model case evidence shown under the ranking.
type qualModelDetail struct {
	Model  string
	Passed int
	Total  int
	Cases  []qualCaseView
}

type qualCaseView struct {
	Kind       string
	CaseID     string
	Capability string
	Passed     bool
	Prompt     string
	Want       string
	Answer     string
	Error      string
	DurationMS int64
}

// qualDetailsFromState groups stored case evidence by model. It is deliberately
// derived from the same Results/Details state as the score, so the table and
// the expandable evidence cannot disagree.
func qualDetailsFromState(s qualState, models []string) []qualModelDetail {
	out := make([]qualModelDetail, 0, len(models))
	for _, model := range models {
		d := qualModelDetail{Model: model}
		for _, kind := range []string{"generator", "judge"} {
			prefix := kind + "|" + model + "|"
			for key, rec := range s.Details {
				if !strings.HasPrefix(key, prefix) {
					continue
				}
				if rec.Kind == "" {
					rec.Kind = kind
				}
				d.Total++
				if rec.Correct {
					d.Passed++
				}
				d.Cases = append(d.Cases, qualCaseView{
					Kind: rec.Kind, CaseID: rec.CaseID, Capability: rec.Capability,
					Passed: rec.Correct, Prompt: rec.Prompt, Want: rec.Want,
					Answer: rec.Answer, Error: rec.Error, DurationMS: rec.DurationMS,
				})
			}
		}
		sort.Slice(d.Cases, func(i, j int) bool {
			if d.Cases[i].Kind != d.Cases[j].Kind {
				return d.Cases[i].Kind < d.Cases[j].Kind
			}
			return d.Cases[i].CaseID < d.Cases[j].CaseID
		})
		if d.Total > 0 {
			out = append(out, d)
		}
	}
	return out
}

// recordPoolQualification writes the run into the measured pool. It replaces
// counts rather than accumulating them, so a resumable/rerun screen is
// idempotent.
func (u *UI) recordPoolQualification(ctx context.Context, models []string, ladder onboard.Ladder, state qualState) {
	p := u.pool()
	if p == nil {
		return
	}
	type tally struct{ successes, trials int }
	// Scores are computed only on cases every model completed. A canceled or
	// failed extra block therefore cannot advantage the models that ran first.
	balanced := onboard.BalancedCaseKeys(state.Results, models)
	if len(balanced) == 0 {
		return
	}
	for _, model := range models {
		byCapability := map[string]*tally{}
		add := func(capability string, correct bool) {
			if capability == "" {
				capability = string(composition.CapabilityGeneral)
			}
			t := byCapability[capability]
			if t == nil {
				t = &tally{}
				byCapability[capability] = t
			}
			t.trials++
			if correct {
				t.successes++
			}
		}
		gen := &tally{}
		for _, c := range ladder.Generators {
			if !balanced["generator|"+c.ID] {
				continue
			}
			v, ok := state.Results["generator|"+model+"|"+c.ID]
			if !ok {
				continue
			}
			gen.trials++
			if v {
				gen.successes++
			}
			add(c.Capability, v)
		}
		judge := &tally{}
		for _, c := range ladder.Judges {
			if !balanced["judge|"+c.ID] {
				continue
			}
			v, ok := state.Results["judge|"+model+"|"+c.ID]
			if !ok {
				continue
			}
			judge.trials++
			if v {
				judge.successes++
			}
			// Judges strengthen both overall judgment and the specific
			// capability the pair exercises.
			add(string(composition.CapabilityJudgment), v)
			add(c.Capability, v)
		}
		for capability, t := range byCapability {
			p.SetMeasured(model, capability, t.successes, t.trials)
		}
		if gen.trials > 0 {
			p.SetMeasured(model, "stance:generate", gen.successes, gen.trials)
		}
		if judge.trials > 0 {
			p.SetMeasured(model, "stance:verify", judge.successes, judge.trials)
		}
	}
	_ = p.Save(ctx)
}

// ollamaUnload asks Ollama to evict a model immediately. It is best-effort: a
// qualification result is still valid if the unload fails.
func ollamaUnload(ctx context.Context, baseURL, model string) {
	body, err := json.Marshal(map[string]any{
		"model": model, "stream": false, "keep_alive": 0,
	})
	if err != nil {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}
