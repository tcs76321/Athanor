package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/tcs76321/athanor/internal/composition"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/onboard"
)

// poolMin is the repertoire floor for the full deliberation experience.
// Fewer models still work, but the UI says what is lost.
const poolMin = 4

// poolRow is one model on the pool step: installed, included, and measured.
type poolRow struct {
	Name          string
	Family        string
	SizeBytes     int64
	InPool        bool
	Installed     bool
	Excluded      bool
	ContextTarget int
	Score         float64
	Samples       int
	Qualified     bool
	TooLarge      bool
	Code          float64
	Data          float64
	Document      float64
	Text          float64
	Judgment      float64
}

// poolView is the first-use pool and ranking snapshot.
type poolView struct {
	Rows           []poolRow
	Count          int
	QualifiedCount int
	Min            int
	Ready          bool
	Warning        string
	Ranked         []poolRow
}

func (u *UI) pool() *composition.Pool { return u.deps.Pool }

// syncPool reconciles the persisted pool with the installed catalog. It is a
// no-op without a pool or a successful catalog read.
func (u *UI) syncPool(ctx context.Context, models []setupModel, budget modelBudget) {
	p := u.pool()
	if p == nil || len(models) == 0 {
		return
	}
	p.FitLimitBytes = int64(budget.InferenceGiB * (1 << 30))
	measured := measuredResidents(u.deps.ModelAudit)
	infos := make([]composition.ModelInfo, 0, len(models))
	for _, m := range models {
		if !isChatSetupModel(m) {
			continue
		}
		infos = append(infos, composition.ModelInfo{
			Name: m.Name, Family: m.Family, SizeBytes: m.SizeBytes,
			ResidentBytes: measured[m.Name],
			ContextTarget: poolContextTarget(u.deps.Config, m.Name),
		})
	}
	if added := p.Sync(infos); added > 0 {
		_ = p.Save(ctx)
		return
	}
	// Metadata may have changed (family, size); persist it too.
	_ = p.Save(ctx)
}

func poolContextTarget(cfg *config.Config, name string) int {
	if cfg != nil {
		for _, role := range []string{"wide", "tall", "main", "security", "alternative"} {
			if pc, ok := cfg.Personas.Role(role); ok && pc.Model == name && pc.ContextTarget > 0 {
				return pc.ContextTarget
			}
		}
		floor := cfg.ContextEngine.SimpleFloor
		if cfg.ContextEngine.CodingFloor > floor {
			floor = cfg.ContextEngine.CodingFloor
		}
		if cfg.ContextEngine.DocumentFloor > floor {
			floor = cfg.ContextEngine.DocumentFloor
		}
		if floor > 0 {
			return floor
		}
	}
	return 16384
}

// includeModel adds an installed model the user removed earlier.
func (u *UI) includeModel(ctx context.Context, name string) error {
	p := u.pool()
	if p == nil {
		return fmt.Errorf("model pool is not available")
	}
	models, err := fetchModels(ctx, u.deps.Config.Inference.OllamaURL)
	if err != nil {
		return err
	}
	for _, m := range models {
		if m.Name == name {
			p.Upsert(composition.ModelInfo{
				Name: m.Name, Family: m.Family, SizeBytes: m.SizeBytes,
				ContextTarget: poolContextTarget(u.deps.Config, m.Name),
			})
			return p.Save(ctx)
		}
	}
	return fmt.Errorf("model %q is not installed", name)
}

// excludeModel removes a model from the pool and remembers the choice.
func (u *UI) excludeModel(ctx context.Context, name string) error {
	p := u.pool()
	if p == nil {
		return fmt.Errorf("model pool is not available")
	}
	p.Exclude(name)
	return p.Save(ctx)
}

// welcomePool builds the pool rows from the installed catalog and the pool's
// measured profiles.
func (u *UI) welcomePool(models []setupModel) poolView {
	view := poolView{Min: poolMin}
	p := u.pool()
	if p == nil {
		return view
	}
	profiles := map[string]composition.Profile{}
	for _, prof := range p.Profiles() {
		profiles[prof.Name] = prof
	}
	seen := map[string]bool{}
	for _, m := range models {
		if !isChatSetupModel(m) {
			continue
		}
		seen[m.Name] = true
		row := poolRow{
			Name: m.Name, Family: m.Family, SizeBytes: m.SizeBytes,
			Installed: true, ContextTarget: poolContextTarget(u.deps.Config, m.Name),
		}
		if prof, ok := profiles[m.Name]; ok {
			row.InPool = true
			row.ContextTarget = prof.ContextTarget
			row.Family = firstNonEmpty(prof.Family, m.Family)
			row.TooLarge = prof.TooLarge
			row.Score, row.Samples, row.Qualified = overallScore(prof)
			row.Code = scoreValue(prof, string(composition.CapabilityCode))
			row.Data = scoreValue(prof, string(composition.CapabilityData))
			row.Document = scoreValue(prof, string(composition.CapabilityDocument))
			row.Text = scoreValue(prof, string(composition.CapabilityText))
			row.Judgment = scoreValue(prof, string(composition.CapabilityJudgment))
		} else {
			row.Excluded = true
		}
		view.Rows = append(view.Rows, row)
	}
	// A pool entry whose model vanished from the catalog is not shown; Sync
	// removes it on the next catalog read.
	for name, prof := range profiles {
		if seen[name] {
			continue
		}
		row := poolRow{Name: name, Family: prof.Family, InPool: true, ContextTarget: prof.ContextTarget, TooLarge: prof.TooLarge}
		row.Score, row.Samples, row.Qualified = overallScore(prof)
		row.Code = scoreValue(prof, string(composition.CapabilityCode))
		row.Data = scoreValue(prof, string(composition.CapabilityData))
		row.Document = scoreValue(prof, string(composition.CapabilityDocument))
		row.Text = scoreValue(prof, string(composition.CapabilityText))
		row.Judgment = scoreValue(prof, string(composition.CapabilityJudgment))
		view.Rows = append(view.Rows, row)
	}
	sort.SliceStable(view.Rows, func(i, j int) bool {
		a, b := view.Rows[i], view.Rows[j]
		if a.InPool != b.InPool {
			return a.InPool
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		return a.Name < b.Name
	})
	for _, row := range view.Rows {
		if row.InPool && !row.TooLarge {
			view.Count++
			if row.Qualified {
				view.QualifiedCount++
			}
			view.Ranked = append(view.Ranked, row)
		}
	}
	view.Ready = view.Count >= poolMin
	if view.Count == 0 {
		view.Warning = "No models are in the pool. Pull at least four, or continue and Athanor will run on the legacy persona defaults."
	} else if view.Count < poolMin {
		view.Warning = fmt.Sprintf("%d model(s) in the pool. Four or more enable the full deliberation repertoire; fewer runs in a degraded single-model mode.", view.Count)
	}
	return view
}

func overallScore(prof composition.Profile) (score float64, samples int, qualified bool) {
	total := 0.0
	n := 0
	for key, s := range prof.Scores {
		if len(key) >= len("stance:") && key[:len("stance:")] == "stance:" {
			continue
		}
		if s.Trials == 0 {
			continue
		}
		total += s.Value()
		n++
		samples += s.Trials
	}
	if n == 0 {
		return 0.5, 0, false
	}
	return total / float64(n), samples, true
}

func scoreValue(prof composition.Profile, key string) float64 {
	if s, ok := prof.Scores[key]; ok && s.Trials > 0 {
		return s.Value()
	}
	return 0
}

func isChatSetupModel(m setupModel) bool {
	return onboard.IsChatModel(onboard.Model{Name: m.Name, Family: m.Family})
}

// measuredResidents reads the audit's loaded (weights + KV) sizes keyed by
// model. It is best-effort: a missing or malformed audit leaves file sizes as
// the fallback.
func measuredResidents(raw string) map[string]int64 {
	out := map[string]int64{}
	if raw == "" {
		return out
	}
	var a struct {
		Roles []struct {
			Model       string `json:"model"`
			LoadedBytes int64  `json:"loaded_bytes"`
		} `json:"roles"`
	}
	if json.Unmarshal([]byte(raw), &a) != nil {
		return out
	}
	for _, r := range a.Roles {
		if r.Model != "" && r.LoadedBytes > 0 {
			out[r.Model] = r.LoadedBytes
		}
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// inferenceFitBytes is the host inference budget used to mark a model too
// large for this machine: host RAM minus the OS/apps reserve and, on a
// separate-VM runtime, the VM allocation.
func inferenceFitBytes(hw Hardware) int64 {
	host := hw.HostMemoryBytes
	runtime := hw.TotalMemoryBytes
	if host == 0 {
		host = runtime
	}
	if host == 0 {
		return 0
	}
	limit := int64(host) - int64(hostReserveGiB*(1<<30))
	if runtime > 0 && host > runtime {
		limit -= int64(runtime)
	}
	if limit < 1<<30 {
		limit = 1 << 30
	}
	return limit
}

// poolSuggestions recommends a small, diverse starter set when the pool is
// short. It never recommends a model that is already installed: the user
// controls pulls.
func (u *UI) poolSuggestions(models []setupModel, pool poolView) []pullSuggestion {
	if pool.Count >= pool.Min {
		return nil
	}
	installed := map[string]bool{}
	for _, m := range models {
		installed[m.Name] = true
	}
	candidates := []pullSuggestion{
		{Model: "granite4.2:3b", Reason: "small, tool-friendly, and long-context"},
		{Model: "gemma4:12b-mlx", Reason: "strong general writer; different family"},
		{Model: "ornith-1.5:9b", Reason: "independent second opinion"},
		{Model: "qwen2.5-coder:7b", Reason: "code and repository work"},
	}
	out := make([]pullSuggestion, 0, pool.Min-pool.Count)
	for _, c := range candidates {
		if len(out) >= pool.Min-pool.Count {
			break
		}
		if installed[c.Model] {
			continue
		}
		out = append(out, c)
	}
	return out
}
