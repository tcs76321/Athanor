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
	MaxContext    int
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
	profiles := map[string]composition.Profile{}
	for _, prof := range p.Profiles() {
		profiles[prof.Name] = prof
	}
	infos := make([]composition.ModelInfo, 0, len(models))
	for _, m := range models {
		if !isChatSetupModel(m) {
			continue
		}
		maxCtx := maxContextForModel(m, p.FitLimitBytes, profiles[m.Name].KVBytesPerToken)
		infos = append(infos, composition.ModelInfo{
			Name: m.Name, Family: m.Family, SizeBytes: m.SizeBytes, FileBytes: m.FileBytes,
			ResidentBytes: measured[m.Name], NativeContext: m.NativeContext,
			KVBytesPerToken: m.KVBytesPerToken, MaxContext: maxCtx,
			ContextTarget: poolContextTarget(u.deps.Config, m.Name, m.SizeBytes, maxCtx),
		})
	}
	// Save only when the catalog actually changed. This runs on every render
	// of the model pages (and inside the 2s qualification poll), so an
	// unconditional Save rewrote the same row continuously.
	if _, changed := p.SyncChanged(infos); changed {
		_ = p.Save(ctx)
	}
}

// poolContextTarget chooses the working window for a model. The capacity
// window (MaxContext) is computed separately from the KV budget; this value is
// the smaller working target, capped by capacity. A small model is "wide" and
// keeps a larger working window; a large model is "tall" and trades context
// for depth. An explicit persona assignment always wins.
func poolContextTarget(cfg *config.Config, name string, sizeBytes int64, maxContext int) int {
	target := 0
	if cfg != nil {
		for _, role := range []string{"wide", "tall", "main", "security", "alternative"} {
			if pc, ok := cfg.Personas.Role(role); ok && pc.Model == name && pc.ContextTarget > 0 {
				target = pc.ContextTarget
				break
			}
		}
	}
	if target == 0 {
		target = contextTargetForSize(sizeBytes, maxContext)
	} else if maxContext > 0 && target > maxContext {
		target = maxContext
	}
	return target
}

// contextTargetForSize is the deterministic wide/tall working ladder, capped
// by the model's capacity. It deliberately does not use the whole capacity:
// short work does not need a 250k reservation, and the capacity is shown
// separately on the pool step.
func contextTargetForSize(sizeBytes int64, maxContext int) int {
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
	if maxContext > 0 && target > maxContext {
		target = maxContext
	}
	if target < 8192 && (maxContext == 0 || maxContext >= 8192) {
		target = 8192
	}
	if target > 65536 {
		target = 65536
	}
	return target
}

// maxContextForModel is the capacity calculation. It uses measured KV data
// when the catalog has it, otherwise the profile's qualification measurement.
func maxContextForModel(m setupModel, fitLimitBytes, measuredKV int64) int {
	weights := m.FileBytes
	if weights <= 0 {
		weights = m.SizeBytes
	}
	kv := m.KVBytesPerToken
	if kv <= 0 {
		kv = measuredKV
	}
	return composition.MaxContextFor(weights, kv, m.NativeContext, fitLimitBytes)
}

// includeModel adds an installed model the user removed earlier.
func (u *UI) includeModel(ctx context.Context, name string) error {
	p := u.pool()
	if p == nil {
		return fmt.Errorf("model pool is not available")
	}
	models, err := u.modelCatalog(ctx)
	if err != nil {
		return err
	}
	for _, m := range models {
		if m.Name == name {
			maxCtx := maxContextForModel(m, p.FitLimitBytes, 0)
			p.Upsert(composition.ModelInfo{
				Name: m.Name, Family: m.Family, SizeBytes: m.SizeBytes, FileBytes: m.FileBytes,
				NativeContext: m.NativeContext, KVBytesPerToken: m.KVBytesPerToken,
				MaxContext: maxCtx, ContextTarget: poolContextTarget(u.deps.Config, m.Name, m.SizeBytes, maxCtx),
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
		maxCtx := maxContextForModel(m, p.FitLimitBytes, profiles[m.Name].KVBytesPerToken)
		row := poolRow{
			Name: m.Name, Family: m.Family, SizeBytes: m.SizeBytes,
			Installed: true, MaxContext: maxCtx,
			ContextTarget: poolContextTarget(u.deps.Config, m.Name, m.SizeBytes, maxCtx),
		}
		if prof, ok := profiles[m.Name]; ok {
			row.InPool = true
			row.ContextTarget = prof.ContextTarget
			row.MaxContext = prof.MaxContext
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
		row := poolRow{Name: name, Family: prof.Family, InPool: true, ContextTarget: prof.ContextTarget, MaxContext: prof.MaxContext, TooLarge: prof.TooLarge, ResidentBytes: prof.ResidentBytes}
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

// hostReserveBytes resolves the effective host reserve, including the hard
// floor derived from the detected Podman VM. A config below that floor is
// clamped here too, so auto-assignment and qualification obey the same budget
// as the Resources and Models pages.
func hostReserveBytes(cfg *config.Config, hw Hardware) int64 {
	res := currentResources(hw, cfg)
	return int64(res.EffectiveHostReserveMB) << 20
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
