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

	"github.com/tcs76321/athanor/internal/composition"
	"github.com/tcs76321/athanor/internal/onboard"
)

// startPoolQualification runs the balanced screen over every model in the
// user's pool. Mode "core" runs the minimum screen, "extra" adds ten minutes of
// blocks, and "full" runs every case in the deep ladder. The first model's core
// pass sizes the timed blocks. The legacy two-role ladder remains available on
// /ui/setup.
func (u *UI) startPoolQualification(ctx context.Context, fresh, deep bool, mode string, minutes int) (qualState, bool, error) {
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
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "extra", "full":
	default:
		mode = "core"
	}
	if mode == "full" {
		// Full qualification runs every case in the deep ladder.
		deep = true
	}
	if mode == "extra" {
		if minutes <= 0 {
			minutes = 10
		}
		if minutes > 120 {
			minutes = 120
		}
	} else {
		minutes = 0
	}
	state := u.loadQualState(ctx)
	if state.Running && !state.stale() {
		return state, false, nil
	}
	if state.stale() || fresh {
		state = newQualState()
	}
	// "Run minimal core again" is a deliberate refresh; core-only starts from
	// a clean comparison rather than silently reusing the old core.
	if mode == "core" && state.CoreComplete && !fresh {
		state = newQualState()
	}
	if !sameStrings(state.Models, models) {
		// A changed pool invalidates the core comparison: the new model has
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
	runCtx, cancel := context.WithCancel(context.Background())
	u.qualCancel = cancel
	u.qualMu.Unlock()

	ladder := onboard.DefaultLadder()
	if deep {
		ladder = onboard.DeepLadder()
	}
	state.Running = true
	state.StartedAt = time.Now()
	state.Error = ""
	state.Deep = deep
	state.Phase = mode
	state.MinimumMinutes = minutes
	if state.Results == nil {
		state.Results = map[string]bool{}
	}
	state.Done = len(state.Results)
	if mode == "core" {
		state.Total = poolCorePlan(models, ladder)
	} else {
		state.Total = poolQualificationPlan(models, ladder)
	}
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
		u.runPoolQualification(runCtx, models, ladder, mode, &runState)
	}(runState)
	return state, true, nil
}

// ensureQualificationStarted starts the minimum screen the first time the
// qualification step is opened. It resumes an interrupted run (the Core
// restarted while it was working), but never restarts a run the operator
// deliberately stopped or one that already has its core.
func (u *UI) ensureQualificationStarted(ctx context.Context) {
	if u.deps.Config == nil {
		return
	}
	st := u.loadQualState(ctx)
	if st.Running && !st.stale() {
		return
	}
	if st.CoreComplete || !st.FinishedAt.IsZero() {
		return
	}
	if p := u.pool(); p == nil || len(p.EligibleNames()) == 0 {
		return
	}
	_, _, _ = u.startPoolQualification(ctx, false, false, "core", 0)
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

func poolCorePlan(models []string, ladder onboard.Ladder) int {
	return len(models) * len(onboard.QualificationCore(ladder))
}

func (u *UI) runPoolQualification(ctx context.Context, models []string, ladder onboard.Ladder, mode string, state *qualState) {
	state.Running = true
	state.Error = ""
	if state.Results == nil {
		state.Results = map[string]bool{}
	}
	if state.Details == nil {
		state.Details = map[string]qualCaseRecord{}
	}
	core := onboard.QualificationCore(ladder)
	if len(core) == 0 {
		state.Running = false
		state.Error = "qualification ladder has no core cases"
		u.saveQualState(context.Background(), *state)
		return
	}
	runCases := func(model string, refs []onboard.CaseRef, measure bool) {
		defer ollamaUnload(context.Background(), u.deps.Config.Inference.OllamaURL, model)
		for _, ref := range refs {
			if ctx.Err() != nil {
				return
			}
			u.runQualCase(ctx, model, ref, ladder, state)
		}
		if measure {
			if resident, ok := ollamaResident(ctx, u.deps.Config.Inference.OllamaURL, model); ok {
				u.pool().SetMeasuredResident(model, resident, 8192)
			}
		}
	}

	// The core always runs for every model, regardless of the minimum run time.
	// On a slow machine with a large pool this can exceed the requested extra
	// time; that is correct, because the screen is not comparable until every
	// model has the same core.
	if !state.CoreComplete {
		coreStart := time.Now()
		first := state.FirstModelSeconds == 0
		for _, model := range models {
			if ctx.Err() != nil {
				break
			}
			modelStart := time.Now()
			state.CurrentModel = model
			u.saveQualState(context.Background(), *state)
			runCases(model, core, true)
			if first && modelCoreComplete(state, model, core) {
				// The first successfully screened model's core pass is the
				// cost probe. Estimate the full core and use the same per-case
				// cost to size extra blocks instead of waiting for every model.
				state.FirstModel = model
				state.FirstModelSeconds = time.Since(modelStart).Seconds()
				state.EstimatedCoreSeconds = state.FirstModelSeconds * float64(len(models))
				first = false
				u.saveQualState(context.Background(), *state)
			}
		}
		if coreComplete(state, models, core) {
			state.CoreComplete = true
			state.CoreSeconds = time.Since(coreStart).Seconds()
		}
		state.Done = len(state.Results)
		u.saveQualState(context.Background(), *state)
	}
	if mode == "core" || !state.CoreComplete || ctx.Err() != nil {
		u.finishPoolQualification(ctx, models, ladder, state)
		return
	}

	extra := onboard.QualificationExtra(ladder)
	if mode == "full" {
		// Full qualification is bounded only by the ladder: run every extra
		// case for every model in fair blocks, so the balanced score keeps a
		// common sample.
		for len(extra) > 0 && ctx.Err() == nil {
			block := extra
			if len(block) > 30 {
				block = block[:30]
			}
			for _, model := range models {
				if ctx.Err() != nil {
					break
				}
				state.CurrentModel = model
				u.saveQualState(context.Background(), *state)
				runCases(model, block, false)
			}
			extra = extra[len(block):]
			state.Done = len(state.Results)
			u.saveQualState(context.Background(), *state)
		}
		u.finishPoolQualification(ctx, models, ladder, state)
		return
	}

	// A ten-minute extension fills from where the core stopped: the first
	// successfully screened model's core pass sizes the blocks, and a final
	// block may wrap up a little past the limit rather than stop with time
	// unused. Every block runs for every model or not at all.
	if state.MinimumMinutes <= 0 {
		u.finishPoolQualification(ctx, models, ladder, state)
		return
	}
	deadline := state.StartedAt.Add(time.Duration(state.MinimumMinutes) * time.Minute)
	for len(extra) > 0 && ctx.Err() == nil && time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		perCase := estimateCaseSeconds(state, len(models), len(core))
		if perCase <= 0 {
			perCase = 5 * time.Second
		}
		blockCases := int(remaining / (perCase * time.Duration(len(models))))
		if blockCases < 1 {
			// Use the remaining time on one last small sweep; this is the
			// intentional wrap-up, not a hard stop at the minimum.
			blockCases = 1
		}
		if blockCases > 30 {
			blockCases = 30
		}
		if blockCases > len(extra) {
			blockCases = len(extra)
		}
		block := extra[:blockCases]
		for _, model := range models {
			if ctx.Err() != nil {
				break
			}
			state.CurrentModel = model
			u.saveQualState(context.Background(), *state)
			runCases(model, block, false)
		}
		extra = extra[blockCases:]
		state.Done = len(state.Results)
		u.saveQualState(context.Background(), *state)
	}
	u.finishPoolQualification(ctx, models, ladder, state)
}

func modelCoreComplete(state *qualState, model string, core []onboard.CaseRef) bool {
	for _, ref := range core {
		if _, ok := state.Results[ref.Kind+"|"+model+"|"+ref.ID]; !ok {
			return false
		}
	}
	return true
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

func coreComplete(state *qualState, models []string, core []onboard.CaseRef) bool {
	for _, model := range models {
		for _, ref := range core {
			if _, ok := state.Results[ref.Kind+"|"+model+"|"+ref.ID]; !ok {
				return false
			}
		}
	}
	return true
}

func estimateCaseSeconds(state *qualState, models, coreCases int) time.Duration {
	if state.FirstModelSeconds >= 1 && coreCases > 0 {
		return time.Duration(state.FirstModelSeconds*float64(time.Second)) / time.Duration(coreCases)
	}
	if state.CoreSeconds > 0 && models > 0 && coreCases > 0 {
		return time.Duration(state.CoreSeconds*float64(time.Second)) / time.Duration(models*coreCases)
	}
	var total time.Duration
	n := 0
	for _, rec := range state.Details {
		if rec.DurationMS > 0 && (rec.Kind == "generator" || rec.Kind == "judge") {
			total += time.Duration(rec.DurationMS) * time.Millisecond
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return total / time.Duration(n)
}

func (u *UI) finishPoolQualification(ctx context.Context, models []string, ladder onboard.Ladder, state *qualState) {
	state.Running = false
	state.CurrentModel = ""
	state.FinishedAt = time.Now()
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
			u.saveQualState(context.Background(), *state)
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
			u.saveQualState(context.Background(), *state)
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

// ollamaResident returns the loaded size reported by /api/ps for one model.
// The qualification screen just used the model, so a successful read is a
// measured weights+KV number at num_ctx=8192.
func ollamaResident(ctx context.Context, baseURL, model string) (int64, bool) {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet,
		strings.TrimRight(baseURL, "/")+"/api/ps", nil)
	if err != nil {
		return 0, false
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return 0, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}
	var body struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
			Size  int64  `json:"size"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return 0, false
	}
	for _, m := range body.Models {
		name := m.Name
		if name == "" {
			name = m.Model
		}
		if name == model && m.Size > 0 {
			return m.Size, true
		}
	}
	return 0, false
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
