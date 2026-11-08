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

	// A stopped worker is still unwinding. Wait for it to release the shared
	// state before deciding whether this call owns a new run; otherwise a
	// Stop followed immediately by a depth choice silently does nothing.
	if err := u.waitForQualificationStop(ctx); err != nil {
		return u.loadQualState(ctx), false, err
	}

	u.qualMu.Lock()
	if u.qualRunning {
		u.qualMu.Unlock()
		return u.loadQualState(ctx), false, nil
	}
	state := u.loadQualState(ctx)
	if fresh {
		// An explicit "run again" is the only path that discards completed
		// evidence. A long or interrupted run must never be mistaken for a
		// stale one: the 2h clock used to wipe a Complete screen mid-flight.
		state = newQualState()
	}
	u.qualRunning = true
	u.qualStopping = false
	u.qualTier = tier
	u.qualLastSave = time.Time{}
	runCtx, cancel := context.WithCancel(context.Background())
	u.qualCancel = cancel
	done := make(chan struct{})
	u.qualDone = done
	u.qualMu.Unlock()

	if !sameStrings(state.Models, models) {
		// A changed pool invalidates the comparison baseline: the new model has
		// not run the same cases as the old set. Keep results for models that
		// are still pooled; drop evidence for removed ones so it cannot inflate
		// progress or the case count.
		state = retainQualModels(state, models)
		state.CoreComplete = false
		state.FirstModel = ""
		state.FirstModelSeconds = 0
		state.EstimatedCoreSeconds = 0
		state.CoreSeconds = 0
		state.Models = append([]string(nil), models...)
	}
	state.Running = true
	state.Waiting = false
	// A resumed screen starts a fresh clock. The case durations in Details are
	// what the estimate uses; carrying the downtime between restarts made the
	// remaining time look implausible.
	state.StartedAt = time.Now()
	state.FinishedAt = time.Time{}
	state.Error = ""
	state.Deep = tier == qualTierComplete
	state.Phase = tier
	state.Tier = tier
	state.MinimumMinutes = 0
	if state.Results == nil {
		state.Results = map[string]bool{}
	}
	_, plan := tierPlan(tier)
	state.Total = len(models) * len(plan)
	state.Done = donePairs(plan, state.Results, models)
	state.StartDone = len(state.Results)
	minimalPlan := onboard.QualificationMinimal(onboard.DefaultLadder())
	state.CoreComplete = coreCoverage(minimalPlan, models, state.Results)
	state.CurrentModel = models[0]
	saveCtx, saveCancel := context.WithTimeout(context.Background(), 5*time.Second)
	u.saveQualState(saveCtx, state)
	saveCancel()
	runState := copyQualState(state)
	go func(runState qualState, done chan struct{}) {
		defer func() {
			if r := recover(); r != nil {
				runState.Running = false
				runState.Waiting = false
				runState.FinishedAt = time.Now()
				runState.Error = fmt.Sprintf("qualification panicked: %v", r)
				if runState.CurrentModel != "" && u.deps.Config != nil {
					ollamaUnload(context.Background(), u.deps.Config.Inference.OllamaURL, runState.CurrentModel)
				}
				u.saveQualState(context.Background(), runState)
			}
			u.qualMu.Lock()
			u.qualRunning = false
			u.qualStopping = false
			u.qualCancel = nil
			u.qualDone = nil
			u.qualMu.Unlock()
			close(done)
			cancel()
		}()
		u.runPoolQualification(runCtx, models, &runState)
	}(runState, done)
	return state, true, nil
}

// waitForQualificationStop waits only when a cancelled worker is still
// unwinding. A live worker is not waited for: the caller either returns (it
// already owns the run) or applies a live depth change.
func (u *UI) waitForQualificationStop(ctx context.Context) error {
	u.qualMu.Lock()
	stopping := u.qualStopping
	done := u.qualDone
	u.qualMu.Unlock()
	if !stopping || done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// copyQualState is a deep copy of the mutable maps and model slice. The
// worker owns its copy; the state returned to an HTTP handler must not share
// maps the goroutine keeps writing.
func copyQualState(s qualState) qualState {
	out := s
	out.Models = append([]string(nil), s.Models...)
	out.Results = make(map[string]bool, len(s.Results))
	for k, v := range s.Results {
		out.Results[k] = v
	}
	out.Details = make(map[string]qualCaseRecord, len(s.Details))
	for k, v := range s.Details {
		out.Details[k] = v
	}
	return out
}

// retainQualModels drops results and case evidence for models no longer in
// the pool. Case keys are kind|model|id.
func retainQualModels(s qualState, models []string) qualState {
	keep := make(map[string]bool, len(models))
	for _, m := range models {
		keep[m] = true
	}
	for key := range s.Results {
		if !keep[qualStateModel(key)] {
			delete(s.Results, key)
		}
	}
	for key := range s.Details {
		if !keep[qualStateModel(key)] {
			delete(s.Details, key)
		}
	}
	return s
}

func qualStateModel(key string) string {
	parts := strings.SplitN(key, "|", 3)
	if len(parts) != 3 {
		return ""
	}
	return parts[1]
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
	u.qualMu.Lock()
	running, stopping := u.qualRunning, u.qualStopping
	u.qualMu.Unlock()
	if running && !stopping {
		// The worker reads qualTier between cases; a live switch needs no
		// second worker.
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
	}
	_, _, err := u.startPoolQualification(ctx, false, tier)
	return err
}

// reconcileInterruptedQualification clears a persisted running flag that no
// goroutine can own after a Core restart. Completed cases are kept, FinishedAt
// stays zero so the run is distinguishable from an operator stop, and
// maybeResumeQualification resumes it when the qualification step is opened.
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
	st.Waiting = false
	st.CurrentModel = ""
	u.saveQualState(ctx, st)
}

// maybeResumeQualification resumes a run that a Core restart interrupted.
// It never starts a new run: a fresh install (zero StartedAt) still waits for
// the operator to choose a depth. A deliberately stopped or finished run has
// FinishedAt set and is never resumed. A run that ended on an error is left
// for an explicit retry so a broken backend cannot cause a resume loop.
func (u *UI) maybeResumeQualification(ctx context.Context) {
	if u.deps.Config == nil || u.deps.Store == nil {
		return
	}
	if u.qualificationRunning() {
		return
	}
	state := u.loadQualState(ctx)
	if state.Running || state.StartedAt.IsZero() || !state.FinishedAt.IsZero() || state.Error != "" {
		return
	}
	tier := normalizeQualTier(state.Tier)
	_, plan := tierPlan(tier)
	p := u.pool()
	if p == nil {
		return
	}
	models := p.EligibleNames()
	if len(models) == 0 || missingPairs(plan, state.Results, models) == 0 {
		return
	}
	u.setQualTier(tier)
	if _, _, err := u.startPoolQualification(ctx, false, tier); err != nil {
		state.Error = err.Error()
		saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		u.saveQualState(saveCtx, state)
	}
}

// cancelQualification stops the in-flight screen. The run is resumable: the
// completed case keys stay in state, so starting again continues from there.
// qualStopping lets an immediate depth choice wait out the old worker instead
// of being refused as "already running".
func (u *UI) cancelQualification() {
	u.qualMu.Lock()
	cancel := u.qualCancel
	if cancel != nil {
		u.qualStopping = true
	}
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
	runCases := func(model string, refs []onboard.CaseRef, expectedTier string, l onboard.Ladder, plan []onboard.CaseRef) (bool, error) {
		for _, ref := range refs {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			// A live depth change stops the current pass between cases, so a
			// switch to Balanced or Complete does not wait for the current pass.
			if expectedTier != "" && u.currentQualTier() != expectedTier {
				return false, nil
			}
			// Never evict a model a job is using. Wait for the current job to
			// finish rather than fighting it for the single resident model.
			if !u.waitForInference(ctx, state) {
				return false, ctx.Err()
			}
			if err := u.runQualCase(ctx, model, ref, l, state); err != nil {
				return false, err
			}
			state.Done = donePairs(plan, state.Results, models)
			state.CoreComplete = coreCoverage(minimalPlan, models, state.Results)
			u.saveQualStateThrottled(context.Background(), *state)
		}
		return true, nil
	}

	// Qualification owns model residency while it runs. Evict anything left
	// from an earlier run before the first load, then keep exactly one model
	// resident by unloading each model before moving to the next.
	if !u.waitForInference(ctx, state) {
		ladder, _ := planFor(u.currentQualTier())
		u.finishPoolQualification(ctx, models, ladder, state)
		return
	}
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
		state.Done = donePairs(plan, state.Results, models)
		state.CoreComplete = coreCoverage(minimalPlan, models, state.Results)
		u.saveQualState(context.Background(), *state)

		ran := false
		var runErr error
		for _, model := range models {
			if ctx.Err() != nil {
				break
			}
			pending := pendingForModel(plan, state.Results, model)
			if len(pending) == 0 {
				continue
			}
			if !u.waitForInference(ctx, state) {
				break
			}
			ran = true
			state.CurrentModel = model
			u.saveQualState(context.Background(), *state)
			complete, err := runCases(model, pending, tier, ladder, plan)
			if !u.waitForInference(ctx, state) {
				// A stop can arrive during a job wait. Release the model we
				// still hold rather than leaving it resident after the run.
				ollamaUnload(context.Background(), u.deps.Config.Inference.OllamaURL, model)
				break
			}
			ollamaUnload(context.Background(), u.deps.Config.Inference.OllamaURL, model)
			if err != nil {
				runErr = err
				break
			}
			if !complete {
				break
			}
			state.Done = donePairs(plan, state.Results, models)
			state.CoreComplete = coreCoverage(minimalPlan, models, state.Results)
			u.saveQualState(context.Background(), *state)
		}
		if runErr != nil {
			break
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

// inferenceBusy reports whether a job may be using Ollama right now. Paused
// and awaiting jobs are excluded: they hold no residency and must not block
// the screen indefinitely. Queued and active phases are included because a
// job can start the moment qualification evicts its model.
func (u *UI) inferenceBusy(ctx context.Context) bool {
	if u.deps.Store == nil || u.deps.Store.DB() == nil {
		return false
	}
	var n int
	err := u.deps.Store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE state IN
		 ('queued','context_building','planning','diverging','evaluating',
		  'reflecting','synthesizing','comparing')`).Scan(&n)
	return err == nil && n > 0
}

// waitForInference blocks until no job can be calling Ollama, recording a
// visible waiting state so a long job does not look like a hung screen.
func (u *UI) waitForInference(ctx context.Context, state *qualState) bool {
	waited := false
	for {
		if ctx.Err() != nil {
			return false
		}
		if !u.inferenceBusy(ctx) {
			if waited {
				state.Waiting = false
				u.saveQualState(context.Background(), *state)
			}
			return true
		}
		if !waited {
			waited = true
			state.Waiting = true
			state.CurrentModel = ""
			u.saveQualState(context.Background(), *state)
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(2 * time.Second):
		}
	}
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
	state.Waiting = false
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
func (u *UI) runQualCase(ctx context.Context, model string, ref onboard.CaseRef, ladder onboard.Ladder, state *qualState) error {
	key := ref.Kind + "|" + model + "|" + ref.ID
	if _, done := state.Results[key]; done {
		return nil
	}
	start := time.Now()
	record := func(rec qualCaseRecord, correct bool) {
		state.Results[key] = correct
		state.Details[key] = rec
		state.Tier = u.currentQualTier()
		u.saveQualStateThrottled(context.Background(), *state)
	}
	switch ref.Kind {
	case "generator":
		for _, c := range ladder.Generators {
			if c.ID != ref.ID {
				continue
			}
			res, err := ollamaChatResult(ctx, u.deps.Config.Inference.OllamaURL, model, onboard.GenPrompt(c))
			if err != nil {
				return u.qualCaseError(ctx, state, model, ref, err)
			}
			rec := qualCaseRecord{
				Kind: "generator", CaseID: c.ID, Model: model, Capability: c.Capability,
				Prompt: c.Prompt, Want: c.Want, Answer: res.Content,
				Correct: onboard.ScoreGen(c, res.Content), DurationMS: time.Since(start).Milliseconds(),
				OutputTokens: res.EvalCount, EvalDurationMS: res.EvalDurationNS / int64(time.Millisecond),
			}
			if res.EvalCount > 0 && res.EvalDurationNS > 0 {
				rec.TokensPerSecond = float64(res.EvalCount) / (float64(res.EvalDurationNS) / float64(time.Second))
			}
			record(rec, rec.Correct)
			return nil
		}
	case "judge":
		for i, c := range ladder.Judges {
			if c.ID != ref.ID {
				continue
			}
			prompt, want := onboard.JudgePrompt(c, i%2 == 0)
			res, err := ollamaChatResult(ctx, u.deps.Config.Inference.OllamaURL, model, prompt)
			if err != nil {
				return u.qualCaseError(ctx, state, model, ref, err)
			}
			rec := qualCaseRecord{
				Kind: "judge", CaseID: c.ID, Model: model, Capability: c.Capability,
				Prompt: prompt, Want: want, Answer: res.Content,
				Correct: onboard.ScoreJudge(res.Content, want), DurationMS: time.Since(start).Milliseconds(),
				OutputTokens: res.EvalCount, EvalDurationMS: res.EvalDurationNS / int64(time.Millisecond),
			}
			if res.EvalCount > 0 && res.EvalDurationNS > 0 {
				rec.TokensPerSecond = float64(res.EvalCount) / (float64(res.EvalDurationNS) / float64(time.Second))
			}
			record(rec, rec.Correct)
			return nil
		}
	}
	return nil
}

// qualCaseError records an infrastructure failure without marking the case
// done. A timeout, a refused connection, or a missing model is not a wrong
// answer; recording it as a failed case permanently poisons the score and
// makes a retry impossible. The run stops so the operator can fix the backend
// and resume from the same case.
func (u *UI) qualCaseError(ctx context.Context, state *qualState, model string, ref onboard.CaseRef, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	state.Error = fmt.Sprintf("%s case %s on %s failed: %v", ref.Kind, ref.ID, model, err)
	state.Waiting = false
	u.saveQualState(context.Background(), *state)
	return err
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
		speedSum := 0.0
		speedN := 0
		for _, rec := range state.Details {
			if rec.Model == model && rec.TokensPerSecond > 0 {
				speedSum += rec.TokensPerSecond
				speedN++
			}
		}
		if speedN > 0 {
			p.SetSpeed(model, speedSum/float64(speedN), speedN)
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
