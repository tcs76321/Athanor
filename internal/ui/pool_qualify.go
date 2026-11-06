package ui

import (
	"context"
	"fmt"
	"time"

	"github.com/tcs76321/athanor/internal/composition"
	"github.com/tcs76321/athanor/internal/onboard"
)

// startPoolQualification runs the bounded screen over every model in the
// user's pool. The legacy two-role ladder remains available on /ui/setup;
// first use uses the pool so the ranking is per model and per capability.
func (u *UI) startPoolQualification(ctx context.Context, fresh bool) (qualState, bool, error) {
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
		installed, err := fetchModels(ctx, u.deps.Config.Inference.OllamaURL)
		if err != nil {
			return qualState{}, false, fmt.Errorf("reading installed models: %w", err)
		}
		if u.deps.Hardware != nil {
			p.FitLimitBytes = inferenceFitBytes(u.deps.Hardware(ctx))
		}
		infos := make([]composition.ModelInfo, 0, len(installed))
		for _, m := range installed {
			if !isChatSetupModel(m) {
				continue
			}
			infos = append(infos, composition.ModelInfo{
				Name: m.Name, Family: m.Family, SizeBytes: m.SizeBytes,
				ContextTarget: poolContextTarget(u.deps.Config, m.Name),
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
	u.qualMu.Unlock()

	ladder := onboard.DefaultLadder()
	state.Running = true
	state.StartedAt = time.Now()
	state.Error = ""
	if state.Results == nil {
		state.Results = map[string]bool{}
	}
	state.Done = len(state.Results)
	state.Total = poolQualificationPlan(models)
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
		u.runPoolQualification(context.Background(), models, ladder, &runState)
	}(runState)
	return state, true, nil
}

func poolQualificationPlan(models []string) int {
	ladder := onboard.DefaultLadder()
	return len(models) * (len(ladder.Generators) + len(ladder.Judges))
}

func (u *UI) runPoolQualification(ctx context.Context, models []string, ladder onboard.Ladder, state *qualState) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	state.Running = true
	state.Error = ""
	if state.Results == nil {
		state.Results = map[string]bool{}
	}
	for _, model := range models {
		if ctx.Err() != nil {
			break
		}
		for _, c := range ladder.Generators {
			if ctx.Err() != nil {
				break
			}
			key := "generator|" + model + "|" + c.ID
			if _, done := state.Results[key]; done {
				continue
			}
			answer, err := ollamaChat(ctx, u.deps.Config.Inference.OllamaURL, model, onboard.GenPrompt(c))
			state.Results[key] = err == nil && onboard.ScoreGen(c, answer)
			state.Done = len(state.Results)
			u.saveQualState(ctx, *state)
		}
		for i, c := range ladder.Judges {
			if ctx.Err() != nil {
				break
			}
			key := "judge|" + model + "|" + c.ID
			if _, done := state.Results[key]; done {
				continue
			}
			prompt, want := onboard.JudgePrompt(c, i%2 == 0)
			answer, err := ollamaChat(ctx, u.deps.Config.Inference.OllamaURL, model, prompt)
			state.Results[key] = err == nil && onboard.ScoreJudge(answer, want)
			state.Done = len(state.Results)
			u.saveQualState(ctx, *state)
		}
	}
	state.Running = false
	state.FinishedAt = time.Now()
	if ctx.Err() != nil {
		state.Error = "qualification stopped at the wall bound; start again to resume"
	}
	u.saveQualState(context.Background(), *state)
	u.recordPoolQualification(context.Background(), models, ladder, *state)
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
	for _, model := range models {
		byCapability := map[string]*tally{}
		gen := &tally{}
		for _, c := range ladder.Generators {
			v, ok := state.Results["generator|"+model+"|"+c.ID]
			if !ok {
				continue
			}
			gen.trials++
			if v {
				gen.successes++
			}
			capability := c.Capability
			if capability == "" {
				capability = string(composition.CapabilityGeneral)
			}
			t := byCapability[capability]
			if t == nil {
				t = &tally{}
				byCapability[capability] = t
			}
			t.trials++
			if v {
				t.successes++
			}
		}
		judge := &tally{}
		for _, c := range ladder.Judges {
			v, ok := state.Results["judge|"+model+"|"+c.ID]
			if !ok {
				continue
			}
			judge.trials++
			if v {
				judge.successes++
			}
		}
		for capability, t := range byCapability {
			p.SetMeasured(model, capability, t.successes, t.trials)
		}
		if gen.trials > 0 {
			p.SetMeasured(model, "stance:generate", gen.successes, gen.trials)
		}
		if judge.trials > 0 {
			p.SetMeasured(model, string(composition.CapabilityJudgment), judge.successes, judge.trials)
			p.SetMeasured(model, "stance:verify", judge.successes, judge.trials)
		}
	}
	_ = p.Save(ctx)
}
