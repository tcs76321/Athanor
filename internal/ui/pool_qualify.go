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

// startPoolQualification runs the bounded screen over every model in the
// user's pool. The legacy two-role ladder remains available on /ui/setup;
// first use uses the pool so the ranking is per model and per capability.
func (u *UI) startPoolQualification(ctx context.Context, fresh, deep bool) (qualState, bool, error) {
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
	if state.Results == nil {
		state.Results = map[string]bool{}
	}
	state.Done = len(state.Results)
	state.Total = poolQualificationPlan(models, ladder)
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
		u.runPoolQualification(runCtx, models, ladder, &runState)
	}(runState)
	return state, true, nil
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

func (u *UI) runPoolQualification(ctx context.Context, models []string, ladder onboard.Ladder, state *qualState) {
	state.Running = true
	state.Error = ""
	if state.Results == nil {
		state.Results = map[string]bool{}
	}
	if state.Details == nil {
		state.Details = map[string]qualCaseRecord{}
	}
	// The core is one case per capability; every model runs it before any
	// score is compared. Extra cases run in blocks for every model, so a slow
	// model cannot starve the rest and an interrupted block is excluded from
	// the balanced score by recordPoolQualification.
	core := onboard.QualificationCore(ladder)
	blocks := onboard.QualificationBlocks(ladder, 30)
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
	for _, model := range models {
		if ctx.Err() != nil {
			break
		}
		runCases(model, core, true)
	}
	for _, block := range blocks {
		if ctx.Err() != nil {
			break
		}
		for _, model := range models {
			if ctx.Err() != nil {
				break
			}
			runCases(model, block, false)
		}
	}
	state.Running = false
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
