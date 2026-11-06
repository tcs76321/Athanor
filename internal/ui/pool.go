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
	ResidentBytes int64
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
	MachineScore   int
	MachineHostGiB float64
	MachineCPUs    int
	FitLimitGiB    float64
}

func (u *UI) pool() *composition.Pool { return u.deps.Pool }

// syncPool reconciles the persisted pool with the installed catalog. It is a
// no-op without a pool or a successful catalog read.
func (u *UI) syncPool(ctx context.Context, models []setupModel, budget modelBudget, hw Hardware) {
	p := u.pool()
	if p == nil || len(models) == 0 {
		return
	}
	p.SetMachine(composition.Machine{
		HostBytes: int64(hw.HostMemoryBytes), RuntimeBytes: int64(hw.TotalMemoryBytes),
		FreeDiskBytes: int64(hw.FreeDiskBytes), CPUs: hw.CPUs,
	})
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
			ContextTarget: poolContextTarget(u.deps.Config, m.Name, m.SizeBytes, p.FitLimitBytes),
		})
	}
	if added := p.Sync(infos); added > 0 {
		_ = p.Save(ctx)
		return
	}
	// Metadata may have changed (family, size); persist it too.
	_ = p.Save(ctx)
}

// poolContextTarget chooses a per-model context window. A small model is a
// "wide" model: it carries less weight and can hold a large context. A large
// model is a "tall" model: its weights take the memory, so it gets a smaller
// window. An explicit persona assignment always wins.
func poolContextTarget(cfg *config.Config, name string, sizeBytes, fitLimitBytes int64) int {
	if cfg != nil {
		for _, role := range []string{"wide", "tall", "main", "security", "alternative"} {
			if pc, ok := cfg.Personas.Role(role); ok && pc.Model == name && pc.ContextTarget > 0 {
				return pc.ContextTarget
			}
		}
	}
	return contextTargetForSize(sizeBytes, fitLimitBytes)
}

// contextTargetForSize is the deterministic wide/tall ladder. The memory cap
// reserves 1 GiB for runtime overhead and estimates KV at 16 KiB per GiB of
// weights per token. The estimate is deliberately conservative; it keeps a
// large model from asking for a window that cannot coexist with its weights.
func contextTargetForSize(sizeBytes, fitLimitBytes int64) int {
	sizeGiB := float64(sizeBytes) / (1 << 30)
	var target int
	switch {
	case sizeGiB <= 3:
		target = 65536
	case sizeGiB <= 6:
		target = 49152
	case sizeGiB <= 9:
		target = 32768
	case sizeGiB <= 12:
		target = 24576
	case sizeGiB <= 16:
		target = 16384
	case sizeGiB <= 20:
		target = 12288
	default:
		target = 8192
	}
	if fitLimitBytes > 0 && sizeBytes > 0 {
		available := fitLimitBytes - sizeBytes - (1 << 30)
		kvPerToken := int64(sizeGiB * 16 * 1024)
		if available > 0 && kvPerToken > 0 {
			maxCtx := int(available / kvPerToken)
			maxCtx = (maxCtx / 2048) * 2048
			if maxCtx < target {
				target = maxCtx
			}
		}
	}
	if target < 8192 {
		target = 8192
	}
	if target > 65536 {
		target = 65536
	}
	return target
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
				ContextTarget: poolContextTarget(u.deps.Config, m.Name, m.SizeBytes, p.FitLimitBytes),
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
	view.FitLimitGiB = float64(p.FitLimitBytes) / (1 << 30)
	machine := p.MachineProfile()
	view.MachineScore = machine.Score
	view.MachineHostGiB = float64(machine.HostBytes) / (1 << 30)
	view.MachineCPUs = machine.CPUs
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
			Installed: true, ContextTarget: poolContextTarget(u.deps.Config, m.Name, m.SizeBytes, p.FitLimitBytes),
		}
		if prof, ok := profiles[m.Name]; ok {
			row.InPool = true
			row.ContextTarget = prof.ContextTarget
			row.Family = firstNonEmpty(prof.Family, m.Family)
			row.ResidentBytes = prof.ResidentBytes
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
		row := poolRow{Name: name, Family: prof.Family, InPool: true, ContextTarget: prof.ContextTarget, TooLarge: prof.TooLarge, ResidentBytes: prof.ResidentBytes}
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
// large for this machine: host RAM minus the operator's host reserve. The
// reserve covers the OS, apps, the Core, and the Podman VM.
func inferenceFitBytes(hw Hardware, reserveBytes int64) int64 {
	host := hw.HostMemoryBytes
	runtime := hw.TotalMemoryBytes
	if host == 0 {
		host = runtime
	}
	if host == 0 {
		return 0
	}
	if reserveBytes <= 0 {
		reserveBytes = int64(hostReserveGiB * (1 << 30))
	}
	limit := int64(host) - reserveBytes
	if limit < 1<<30 {
		limit = 1 << 30
	}
	return limit
}

// hostReserveBytes resolves the operator's host reserve, falling back to the
// 4 GiB default when the config omits it.
func hostReserveBytes(cfg *config.Config) int64 {
	if cfg != nil && cfg.Inference.HostReserveMB > 0 {
		return int64(cfg.Inference.HostReserveMB) << 20
	}
	return int64(hostReserveGiB * (1 << 30))
}

// poolSuggestions recommends a small, size-appropriate core set when the
// machine is missing one. It never requires a model: pulls are explicit and
// the pool warning is advisory. At most four are suggested.
func (u *UI) poolSuggestions(models []setupModel, hw Hardware) []pullSuggestion {
	installed := map[string]bool{}
	for _, m := range models {
		installed[m.Name] = true
	}
	hostGiB := float64(hw.HostMemoryBytes) / (1 << 30)
	if hostGiB == 0 {
		hostGiB = float64(hw.TotalMemoryBytes) / (1 << 30)
	}
	var candidates []pullSuggestion
	switch {
	case hostGiB > 0 && hostGiB <= 16:
		candidates = []pullSuggestion{
			{Model: "granite4.2:3b", Reason: "small and tool-friendly"},
			{Model: "gemma4:e4b-mlx", Reason: "different family, long context"},
			{Model: "ornith-1.5:9b", Reason: "independent second opinion"},
			{Model: "bonsai-2:9b", Reason: "small alternative perspective"},
		}
	case hostGiB > 40:
		candidates = []pullSuggestion{
			{Model: "gemma4:12b-mlx", Reason: "strong general writer"},
			{Model: "qwen3.8:27b-mlx", Reason: "deep work on a large host"},
			{Model: "granite4.2:8b", Reason: "different family, tool-friendly"},
			{Model: "ornith-1.5:9b", Reason: "independent second opinion"},
		}
	default:
		candidates = []pullSuggestion{
			{Model: "granite4.2:8b", Reason: "balanced, tool-friendly"},
			{Model: "gemma4:12b-mlx", Reason: "strong general writer"},
			{Model: "ornith-1.5:9b", Reason: "independent second opinion"},
			{Model: "bonsai-2:9b", Reason: "small alternative perspective"},
		}
	}
	out := make([]pullSuggestion, 0, 4)
	for _, c := range candidates {
		if installed[c.Model] {
			continue
		}
		out = append(out, c)
		if len(out) == 4 {
			break
		}
	}
	return out
}
