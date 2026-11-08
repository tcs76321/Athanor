package ui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/doctor"
	"github.com/tcs76321/athanor/internal/onboard"
	"github.com/tcs76321/athanor/internal/store"
)

// Machine onboarding (once per computer). The state lives in system_state so a
// Core restart resumes where the operator left off, and a completed or skipped
// wizard never nags again.
const onboardingKey = "ui:machine_onboarding"

type onboardingState struct {
	Step      string    `json:"step"`
	Completed bool      `json:"completed"`
	Skipped   bool      `json:"skipped"`
	Version   string    `json:"version,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type onboardingStep struct {
	ID    string
	Label string
	Note  string
}

type personaGuide struct {
	Role    string
	Title   string
	Purpose string
}

var personaGuides = []personaGuide{
	{"wide", "Wide", "Ingestion, repository reading, and short summaries. It should be fast and cheap."},
	{"tall", "Tall", "Hard reasoning: planning, decomposition, and difficult design work. It should be the strongest model that fits."},
	{"main", "Main", "Normal execution and synthesis. The everyday worker."},
	{"security", "Security", "Evaluation, comparison, and compaction at temperature 0.0. It must not share a family with the generators."},
	{"alternative", "Alternative", "An orthogonal second opinion for candidate generation. A different family is preferred."},
}

type pullSuggestion struct {
	Model  string
	Reason string
}

type resourceView struct {
	TotalMemoryGiB    float64
	HostMemoryGiB     float64
	OutsideRuntimeGiB float64
	FreeDiskGiB       float64
	CPUs              int
	MemoryMB          int
	MemoryMaxMB       int
	CPUsAllocated     float64
	CPUsMax           float64
	TmpfsMB           int
	PidsLimit         int
	ReserveMB         int
	HostReserveMB     int
	// MinHostReserveMB is the hard floor Athanor will use: the Podman VM size
	// (or 2 GiB without a separate VM). A configured value below this is
	// clamped rather than accepted.
	MinHostReserveMB int
	// EffectiveHostReserveMB is what the budget actually uses: the configured
	// value, or the hard floor when the configured value is below it.
	EffectiveHostReserveMB int
	// ReserveBelowVM is true when a Podman VM exists and the configured host
	// reserve is below its hard floor. ReserveClamped also marks that Athanor
	// is enforcing the floor for the budget.
	ReserveBelowVM bool
	ReserveClamped bool
	// ReserveBelowRecommended is true when the configured value is allowed
	// but below the recommended VM + 1 GiB headroom.
	ReserveBelowRecommended bool
	// RecommendedReserveMB is the smallest sensible value: the VM size plus a
	// 1 GiB margin, rounded to a 256 MB step and capped below host RAM.
	RecommendedReserveMB int
}

type modelBudget struct {
	// Host inference budget: the configured persona models run on the host.
	HostGiB              float64
	RuntimeGiB           float64
	VmGiB                float64 // detected Podman VM allocation (inside the host reserve)
	ReserveGiB           float64 // effective OS/apps/Core/Podman reserve on the host
	ConfiguredReserveGiB float64 // the operator's stored value
	InferenceGiB         float64 // Host - Reserve
	FitLimitGiB          float64 // GPU budget when discrete, else InferenceGiB
	Model                string
	ModelGiB             float64
	ModelMeasured        bool
	ModelFitsHost        bool
	ModelFits            bool

	// Accelerator is the host GPU/NPU probe. A discrete GPU budgets VRAM; a
	// unified accelerator (Apple Silicon) uses the host budget above.
	AccelName     string
	AccelKind     string
	AccelBackend  string
	AccelUnified  bool
	AccelDevices  int
	VRAMGiB       float64
	GPUReserveGiB float64
	GPUBudgetGiB  float64
	GPUFits       bool
	SpillGiB      float64
	NPU           string

	// Runtime sandbox budget: Job Pods live inside the Podman runtime.
	PodGiB          float64
	RuntimeReserve  float64
	RuntimeMaxGiB   float64
	RuntimeFits     bool
	RuntimeHeadroom float64

	Overcommit              bool
	OverBy                  float64
	ReserveBelowVM          bool
	ReserveClamped          bool
	ReserveBelowRecommended bool
	RecommendedReserveMB    int
}

// hostReserveGiB is the fallback non-model reserve when the config does not
// set one. It covers the OS, apps, the Core, and the Podman VM.
const hostReserveGiB = 4.0

// budgetFor explains two independent memory budgets instead of adding Job Pod
// memory to host model memory. The host budget is the largest *configured
// persona* resident size against host RAM minus the operator's host reserve.
// The runtime budget is the Job Pod ceiling against the runtime allocation
// minus its own reserve. Installed-but-unused models do not enter either
// budget.
func budgetFor(roles []setupRole, models []setupModel, hw Hardware, res resourceView) modelBudget {
	hostBytes := hw.HostMemoryBytes
	runtimeBytes := hw.TotalMemoryBytes
	if hostBytes == 0 {
		hostBytes = runtimeBytes
	}
	if runtimeBytes == 0 {
		runtimeBytes = hostBytes
	}
	reserveMB := res.EffectiveHostReserveMB
	if reserveMB <= 0 {
		reserveMB = res.HostReserveMB
	}
	if reserveMB <= 0 {
		reserveMB = int(hostReserveGiB * 1024)
	}
	b := modelBudget{
		HostGiB:                 float64(hostBytes) / (1 << 30),
		RuntimeGiB:              float64(runtimeBytes) / (1 << 30),
		ReserveGiB:              float64(reserveMB) / 1024,
		ConfiguredReserveGiB:    float64(res.HostReserveMB) / 1024,
		PodGiB:                  float64(res.MemoryMB) / 1024,
		RuntimeReserve:          float64(res.ReserveMB) / 1024,
		ReserveClamped:          res.ReserveClamped,
		ReserveBelowRecommended: res.ReserveBelowRecommended,
		RecommendedReserveMB:    res.RecommendedReserveMB,
	}
	if b.HostGiB > b.RuntimeGiB {
		// A separate Podman VM is itself host memory. The reserve is meant to
		// cover it; surface a warning when it does not.
		b.VmGiB = b.RuntimeGiB
		b.ReserveBelowVM = res.ReserveBelowVM
		if b.RecommendedReserveMB == 0 {
			rec := int(b.VmGiB*1024) + 1024
			b.RecommendedReserveMB = ((rec + 255) / 256) * 256
		}
	}
	b.InferenceGiB = b.HostGiB - b.ReserveGiB
	if b.InferenceGiB < 0 {
		b.InferenceGiB = 0
	}
	b.FitLimitGiB = b.InferenceGiB
	if a := hw.Accelerator; a != nil {
		b.AccelName, b.AccelKind, b.AccelBackend = a.Name, a.Kind, a.Backend
		b.AccelUnified, b.AccelDevices, b.NPU = a.Unified, a.Devices, a.NPU
		b.VRAMGiB = float64(a.MemoryMiB) / 1024
		if !a.Unified && a.MemoryMiB > 0 {
			// Leave headroom for the display/compositor and other small GPU
			// users. The model budget is VRAM, not host RAM.
			reserve := b.VRAMGiB * 0.1
			if reserve < 0.5 {
				reserve = 0.5
			}
			if reserve > 2.0 {
				reserve = 2.0
			}
			b.GPUReserveGiB = reserve
			b.GPUBudgetGiB = b.VRAMGiB - reserve
			if b.GPUBudgetGiB < 0 {
				b.GPUBudgetGiB = 0
			}
			b.FitLimitGiB = b.GPUBudgetGiB
		}
	}
	b.RuntimeMaxGiB = b.RuntimeGiB - b.RuntimeReserve
	if b.RuntimeMaxGiB < 0 {
		b.RuntimeMaxGiB = 0
	}
	b.RuntimeFits = b.PodGiB <= b.RuntimeMaxGiB
	b.RuntimeHeadroom = b.RuntimeMaxGiB - b.PodGiB

	byName := make(map[string]setupModel, len(models))
	for _, m := range models {
		byName[m.Name] = m
	}
	for _, role := range roles {
		m, ok := byName[role.Model]
		if !ok {
			continue
		}
		if g := float64(m.SizeBytes) / (1 << 30); g > b.ModelGiB {
			b.ModelGiB = g
			b.Model = m.Name
			b.ModelMeasured = m.Measured
		}
	}
	b.ModelFitsHost = b.ModelGiB <= b.InferenceGiB
	b.ModelFits = b.ModelFitsHost
	if a := hw.Accelerator; a != nil && !a.Unified && b.GPUBudgetGiB > 0 {
		b.GPUFits = b.ModelGiB <= b.GPUBudgetGiB
		b.ModelFits = b.GPUFits
		if b.ModelGiB > b.GPUBudgetGiB {
			b.SpillGiB = b.ModelGiB - b.GPUBudgetGiB
		}
	}
	b.OverBy = b.ModelGiB + b.ReserveGiB - b.HostGiB
	b.Overcommit = b.HostGiB > 0 && b.OverBy > 0
	return b
}

var onboardingSteps = []onboardingStep{
	{"welcome", "Welcome", "What Athanor is and what it will do"},
	{"resources", "Resources", "RAM, CPU, and Job Pod limits"},
	{"models", "Model pool", "Choose the models Athanor may use"},
	{"qualify", "Qualification", "Measure and rank the pool on this computer"},
	{"preferences", "Preferences", "Theme and notifications"},
	{"environment", "Environment", "Final doctor checks before you start"},
	{"ready", "Ready", "Create the first project"},
}

func (u *UI) loadOnboarding(ctx context.Context) onboardingState {
	st := onboardingState{Step: "welcome"}
	if u.deps.Store == nil {
		return st
	}
	raw, ok := u.loadSystemState(ctx, onboardingKey)
	if !ok {
		return st
	}
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		slog.Error("ui: parsing onboarding state", "err", err)
		return onboardingState{Step: "welcome"}
	}
	if st.Step == "" {
		st.Step = "welcome"
	}
	if !validOnboardingStep(st.Step) {
		st.Step = "welcome"
	}
	return st
}

func (u *UI) saveOnboarding(ctx context.Context, st onboardingState) error {
	if u.deps.Store == nil {
		return nil
	}
	st.UpdatedAt = time.Now().UTC()
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := u.saveSystemState(ctx, onboardingKey, string(raw)); err != nil {
		return fmt.Errorf("saving onboarding: %w", err)
	}
	return nil
}

func validOnboardingStep(id string) bool {
	for _, s := range onboardingSteps {
		if s.ID == id {
			return true
		}
	}
	return false
}

func onboardingIndex(id string) int {
	for i, s := range onboardingSteps {
		if s.ID == id {
			return i
		}
	}
	return 0
}

// needsOnboarding reports whether a fresh install should be sent to the
// wizard: no completed/skipped marker and no projects yet.
func (u *UI) needsOnboarding(ctx context.Context) bool {
	st := u.loadOnboarding(ctx)
	if st.Completed || st.Skipped {
		return false
	}
	if u.deps.Store == nil {
		return false
	}
	var n int
	if err := u.deps.Store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&n); err != nil {
		return false
	}
	return n == 0
}

// pullProgress is one model pull's live state.
type pullProgress struct {
	Status    string
	StartedAt time.Time
	Completed int64
	Total     int64
	Error     string
	Done      bool
}

type pullView struct {
	Model     string
	Status    string
	Percent   int
	Completed int64
	Total     int64
	Error     string
	Done      bool
}

func (u *UI) handleWelcome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st := u.loadOnboarding(ctx)
	if st.Completed {
		http.Redirect(w, r, "/ui", http.StatusSeeOther)
		return
	}
	step := r.URL.Query().Get("step")
	if validOnboardingStep(step) {
		st.Step = step
	}
	data := u.welcomeData(ctx, st)
	data["Bare"] = true
	data["Applied"] = r.URL.Query().Get("applied")
	u.render(w, "Welcome", welcomeBody, data)
}

func (u *UI) welcomeData(ctx context.Context, st onboardingState) map[string]any {
	models, modelsErr := []setupModel{}, ""
	roles := []setupRole{}
	if u.deps.Config != nil {
		var err error
		models, err = u.modelCatalog(ctx)
		if err != nil {
			modelsErr = err.Error()
		}
		models = applyMeasuredSizes(models, u.deps.ModelAudit)
		roles = u.personaRoles(models)
	}
	health := []healthCheck{}
	if st.Step == "environment" {
		if u.deps.Doctor != nil {
			health = doctorChecks(u.deps.Doctor(ctx))
		} else {
			health = u.healthChecks(ctx, roles, models, modelsErr)
		}
		health = softenDefaultModelWarning(health, roles)
	}
	u.onboardMu.Lock()
	pulls := make([]pullView, 0, len(u.pulls))
	for model, p := range u.pulls {
		pct := 0
		if p.Total > 0 {
			pct = int(p.Completed * 100 / p.Total)
		}
		pulls = append(pulls, pullView{
			Model: model, Status: p.Status, Percent: pct,
			Completed: p.Completed, Total: p.Total, Error: p.Error, Done: p.Done,
		})
	}
	u.onboardMu.Unlock()
	hw := Hardware{}
	if u.deps.Hardware != nil {
		hw = u.deps.Hardware(ctx)
	}
	measurements := []onboard.Measurement{}
	qual := u.loadQualState(ctx)
	if u.deps.Store != nil {
		measurements = qualMeasurementsFromState(qual)
	}
	fitModels := models
	if names := measuredModelNames(u.deps.ModelAudit); len(names) > 0 {
		// Once an audit exists, only measured models are safe candidates: an
		// unmeasured 30B blob can look like it fits by file size while its KV
		// cache overruns the host.
		fitModels = make([]setupModel, 0, len(models))
		for _, m := range models {
			if names[m.Name] {
				fitModels = append(fitModels, m)
			}
		}
	}
	resources := currentResources(hw, u.deps.Config)
	budget := budgetFor(roles, models, hw, resources)
	u.syncPool(ctx, models, budget, hw)
	assignments := u.autoAssignments(roles, fitModels, hw)
	roster := u.rosterView(int64(budget.FitLimitGiB * (1 << 30)))
	pool := u.welcomePool(models)
	suggestions := u.poolSuggestions(models, hw)
	if pool.QualifiedCount > 0 {
		measurements = qualMeasurementsFromState(qual)
	}
	healthBlocked := false
	for _, c := range health {
		if c.Status == "fail" {
			healthBlocked = true
			break
		}
	}
	rankedNames := make([]string, 0, len(pool.Ranked))
	for _, row := range pool.Ranked {
		rankedNames = append(rankedNames, row.Name)
	}
	poolModels := rankedNames
	if p := u.pool(); p != nil {
		if names := p.EligibleNames(); len(names) > 0 {
			poolModels = names
		}
	}
	tier := normalizeQualTier(qual.Tier)
	if qual.Running {
		tier = u.currentQualTier()
	}
	qualView := qualificationViewForPool(qual, poolModels, tier, len(measurements) > 0 || pool.QualifiedCount > 0)
	return map[string]any{
		"State": st, "Step": st.Step, "Steps": onboardingSteps,
		"StepIndex": onboardingIndex(st.Step), "StepNumber": onboardingIndex(st.Step) + 1,
		"StepCount": len(onboardingSteps), "Roles": roles, "Models": models,
		"ModelsErr": modelsErr, "Health": health, "HealthBlocked": healthBlocked, "Pulls": pulls,
		"Themes": themeNames, "Theme": u.Theme(), "ThemeLabel": themeLabel(u.Theme()),
		"PersonaGuides": personaGuides, "Assignments": assignments,
		"Suggestions": suggestions, "Resources": resources, "Hardware": hw,
		"Budget": budget, "ConfigPath": u.deps.ConfigPath,
		"HasMeasurements":    len(measurements) > 0 || pool.QualifiedCount > 0,
		"Qualification":      qualView,
		"Pool":               pool,
		"PoolRows":           pool.Rows,
		"PoolRanked":         pool.Ranked,
		"QualDetails":        qualDetailsFromState(qual, rankedNames),
		"Roster":             roster,
		"PoolCount":          pool.Count,
		"PoolReady":          pool.Ready,
		"PoolWarning":        pool.Warning,
		"PoolRecommendation": pool.Recommendation,
		"PoolMin":            pool.Min,
		"OllamaURL": func() string {
			if u.deps.Config == nil {
				return ""
			}
			return u.deps.Config.Inference.OllamaURL
		}(),
	}
}

// applyMeasuredSizes overlays the measured resident size from the model audit
// onto the catalog. Model-fit decisions must use weights + KV, not the blob on
// disk; without an audit the catalog size remains the fallback.
func applyMeasuredSizes(models []setupModel, raw string) []setupModel {
	if raw == "" || len(models) == 0 {
		return models
	}
	var a onboard.Audit
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return models
	}
	sizes := map[string]int64{}
	for _, r := range a.Roles {
		if r.LoadedBytes > sizes[r.Model] {
			sizes[r.Model] = r.LoadedBytes
		}
	}
	if len(sizes) == 0 {
		return models
	}
	out := append([]setupModel(nil), models...)
	for i := range out {
		if b := sizes[out[i].Name]; b > 0 {
			out[i].SizeBytes = b
			out[i].Measured = true
		}
	}
	return out
}

// measuredModelNames returns the models covered by the audit, if any. When an
// audit is present it is the safe candidate set for fit decisions.
func measuredModelNames(raw string) map[string]bool {
	if raw == "" {
		return nil
	}
	var a onboard.Audit
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, r := range a.Roles {
		if r.Model != "" {
			out[r.Model] = true
		}
	}
	return out
}

// autoAssignments converts the measured pool into the functional roster and
// maps it onto the configured persona roles. The same policy and evidence feed
// the roster UI, so the displayed cast and the applied cast cannot drift.
func (u *UI) autoAssignments(roles []setupRole, models []setupModel, hw Hardware) []onboard.Assignment {
	if len(models) == 0 {
		return nil
	}
	fitBytes := inferenceFitBytes(hw, hostReserveBytes(u.deps.Config, hw))
	roster := u.rosterView(fitBytes)
	picks := map[string]onboard.RosterPick{}
	for _, pick := range roster.Picks {
		picks[pick.Role] = pick
	}
	if len(roles) == 0 {
		for _, name := range []string{"wide", "tall", "main", "security", "alternative"} {
			roles = append(roles, setupRole{Role: name})
		}
	}
	if len(picks) == 0 {
		// No measurements yet: fall back to the conservative size policy so
		// first use still has a working cast.
		oroles := make([]onboard.Role, 0, len(roles))
		for _, r := range roles {
			oroles = append(oroles, onboard.Role{Role: r.Role, Model: r.Model, ContextTarget: r.ContextTarget})
		}
		omodels := make([]onboard.Model, 0, len(models))
		for _, m := range models {
			omodels = append(omodels, onboard.Model{
				Name: m.Name, SizeBytes: m.SizeBytes, Family: onboard.InferFamily(m.Family, m.Name),
			})
		}
		return onboard.AutoAssign(oroles, omodels, fitBytes, 0)
	}
	out := make([]onboard.Assignment, 0, len(roles))
	for _, r := range roles {
		pick, ok := picks[r.Role]
		if !ok {
			continue
		}
		out = append(out, onboard.Assignment{
			Role: r.Role, Model: pick.Model, Family: pick.Family,
			ContextTarget: r.ContextTarget, Reason: pick.Reason,
		})
	}
	return out
}

// pullSuggestions lists configured persona models that are not installed,
// deduplicated and without role labels; the Models step explains the personas
// separately so the operator is not asked to map them by hand.
// recommendedResources chooses the adaptive Job Pod ceiling and the internal
// sandbox knobs from the runtime capacity. CPU, /tmp, and PID values are
// derived, not operator fields; per-job sizing scales the ceiling by task
// capability. The operator-facing advice is to leave as much host memory free
// as possible for Ollama, which runs outside this runtime.
func recommendedResources(hw Hardware) resourceView {
	totalMB := int(hw.TotalMemoryBytes / (1 << 20))
	hostBytes := hw.HostMemoryBytes
	if hostBytes == 0 {
		hostBytes = hw.TotalMemoryBytes
	}
	minReserve, recReserve := 2048, 4096
	vmMB := int(hw.TotalMemoryBytes / (1 << 20))
	hostMB := int(hostBytes / (1 << 20))
	if vmMB > 0 && hostMB > vmMB {
		// macOS/Podman machine: the VM is a separate host-memory consumer.
		// The hard floor covers the VM; the recommendation leaves 1 GiB for
		// macOS, apps, and the Core above it.
		minReserve = vmMB
		recReserve = vmMB + 1024
		recReserve = ((recReserve + 255) / 256) * 256
		if hostMB > 1024 && recReserve > hostMB-1024 {
			recReserve = hostMB - 1024
			if recReserve < minReserve {
				recReserve = minReserve
			}
		}
	}
	res := resourceView{
		TotalMemoryGiB:         float64(hw.TotalMemoryBytes) / (1 << 30),
		HostMemoryGiB:          float64(hostBytes) / (1 << 30),
		FreeDiskGiB:            float64(hw.FreeDiskBytes) / (1 << 30),
		CPUs:                   hw.CPUs,
		MemoryMB:               4096,
		CPUsAllocated:          4,
		TmpfsMB:                2048,
		PidsLimit:              128,
		ReserveMB:              2048,
		HostReserveMB:          recReserve,
		MinHostReserveMB:       minReserve,
		EffectiveHostReserveMB: recReserve,
		RecommendedReserveMB:   recReserve,
	}
	res.OutsideRuntimeGiB = res.HostMemoryGiB - res.TotalMemoryGiB
	if res.OutsideRuntimeGiB < 0 {
		res.OutsideRuntimeGiB = 0
	}
	res.CPUsMax = float64(hw.CPUs)
	if res.CPUsMax < 1 {
		res.CPUsMax = 8
	}
	if res.CPUsMax > 16 {
		res.CPUsMax = 16
	}
	if totalMB > 0 {
		// Half the runtime is a safe ceiling for one sandbox while leaving
		// room for the Core and Podman; cap it so giant VMs do not produce
		// an 8 GiB+ sandbox by default.
		half := totalMB / 2
		if half > 8192 {
			half = 8192
		}
		if half < 2048 {
			half = 2048
		}
		res.MemoryMB = (half / 512) * 512
		res.MemoryMaxMB = totalMB - res.ReserveMB
	} else {
		res.MemoryMaxMB = 65536
	}
	if res.MemoryMaxMB < 1024 {
		res.MemoryMaxMB = 1024
	}
	if res.MemoryMaxMB > 65536 {
		res.MemoryMaxMB = 65536
	}
	if res.MemoryMB > res.MemoryMaxMB {
		res.MemoryMB = res.MemoryMaxMB
	}
	res.CPUsAllocated = res.CPUsMax
	if res.CPUsAllocated > 4 {
		res.CPUsAllocated = 4
	}
	if res.CPUsAllocated < 1 {
		res.CPUsAllocated = 1
	}
	tmp := res.MemoryMB / 4
	if tmp < 512 {
		tmp = 512
	}
	if tmp > 4096 {
		tmp = 4096
	}
	res.TmpfsMB = (tmp / 256) * 256
	return res
}

// currentResources starts from the adaptive recommendation and overlays the
// operator's persisted Job Pod limits, so the form shows what is actually in
// effect instead of snapping back to defaults after a restart.
func currentResources(hw Hardware, cfg *config.Config) resourceView {
	res := recommendedResources(hw)
	if cfg == nil {
		return res
	}
	if cfg.JobPod.MemoryMB > 0 {
		res.MemoryMB = cfg.JobPod.MemoryMB
	}
	if cfg.JobPod.CPUs > 0 {
		res.CPUsAllocated = cfg.JobPod.CPUs
	}
	if cfg.JobPod.TmpfsMB > 0 {
		res.TmpfsMB = cfg.JobPod.TmpfsMB
	}
	if cfg.JobPod.PidsLimit > 0 {
		res.PidsLimit = cfg.JobPod.PidsLimit
	}
	if cfg.Inference.HostReserveMB > 0 {
		res.HostReserveMB = cfg.Inference.HostReserveMB
	}
	if res.MemoryMB > res.MemoryMaxMB {
		res.MemoryMB = res.MemoryMaxMB
	}
	if res.CPUsAllocated > res.CPUsMax {
		res.CPUsAllocated = res.CPUsMax
	}
	if res.PidsLimit < 32 {
		res.PidsLimit = 32
	}
	if res.PidsLimit > 8192 {
		res.PidsLimit = 8192
	}
	// On macOS the Podman machine is a separate VM carved out of host RAM.
	// The host reserve is supposed to cover the OS, apps, the Core, and that
	// VM. Clamp a configured value below the VM floor instead of letting a
	// model budget pretend the VM is free; keep the recommendation visible.
	if res.MinHostReserveMB <= 0 {
		res.MinHostReserveMB = 2048
	}
	if res.RecommendedReserveMB <= 0 {
		res.RecommendedReserveMB = 4096
	}
	switch {
	case res.HostReserveMB < res.MinHostReserveMB:
		res.ReserveBelowVM = true
		res.ReserveClamped = true
		res.EffectiveHostReserveMB = res.MinHostReserveMB
	case res.HostReserveMB < res.RecommendedReserveMB:
		res.ReserveBelowRecommended = true
		res.EffectiveHostReserveMB = res.HostReserveMB
	default:
		res.EffectiveHostReserveMB = res.HostReserveMB
	}
	return res
}

type healthCheck struct {
	Name   string
	Status string // ok | warn | fail
	Detail string
	Fix    string
}

// personaRoles maps the configured personas to the installed model catalog.
func (u *UI) personaRoles(models []setupModel) []setupRole {
	roles := make([]setupRole, 0, 5)
	if u.deps.Config == nil {
		return roles
	}
	for _, name := range []string{"wide", "tall", "main", "security", "alternative"} {
		pc, ok := u.deps.Config.Personas.Role(name)
		if !ok {
			continue
		}
		roles = append(roles, setupRole{
			Role: name, Model: pc.Model, Family: pc.Family,
			ContextTarget: pc.ContextTarget,
			Installed:     modelInstalled(pc.Model, models),
		})
	}
	return roles
}

// homeHealth is the front-page health summary: the few things an operator
// should see before reading any project. It is cheaper and more focused than
// the full first-run environment check.
func (u *UI) homeHealth(ctx context.Context, failed, alarms, pending int, frozen bool) []healthCheck {
	checks := []healthCheck{{Name: "Core", Status: "ok", Detail: u.deps.Runtime + " · " + u.deps.Version}}
	if u.deps.Config != nil {
		mctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		models, err := u.modelCatalog(mctx)
		cancel()
		if err != nil {
			checks = append(checks, healthCheck{Name: "Ollama", Status: "fail", Detail: err.Error()})
		} else {
			checks = append(checks, healthCheck{Name: "Ollama", Status: "ok", Detail: fmt.Sprintf("%d model(s) installed", len(models))})
			roles := u.personaRoles(models)
			missing := []string{}
			for _, r := range roles {
				if !r.Installed {
					missing = append(missing, r.Role)
				}
			}
			if len(missing) > 0 {
				checks = append(checks, healthCheck{Name: "Models", Status: "warn", Detail: "configured legacy model(s) not installed: " + strings.Join(missing, ", ") + " (the pool is authoritative)"})
			} else if len(roles) > 0 {
				checks = append(checks, healthCheck{Name: "Models", Status: "ok", Detail: "all configured persona models present"})
			}
		}
		if strings.TrimSpace(u.deps.Config.JobPod.Image) == "" {
			checks = append(checks, healthCheck{Name: "Job Pod", Status: "warn", Detail: "no image configured; code tools cannot run"})
		} else {
			checks = append(checks, healthCheck{Name: "Job Pod", Status: "ok", Detail: u.deps.Config.JobPod.Image})
		}
	}
	workStatus := "ok"
	workDetail := "no failures in the last 24h"
	if failed > 0 {
		workStatus = "warn"
		workDetail = fmt.Sprintf("%d failed run(s) in the last 24h", failed)
	}
	checks = append(checks, healthCheck{Name: "Work", Status: workStatus, Detail: workDetail})
	alarmStatus := "ok"
	alarmDetail := "no active alarms"
	if alarms > 0 {
		alarmStatus = "warn"
		alarmDetail = fmt.Sprintf("%d active alarm(s)", alarms)
	}
	checks = append(checks, healthCheck{Name: "Alarms", Status: alarmStatus, Detail: alarmDetail})
	if pending > 0 {
		checks = append(checks, healthCheck{Name: "Approvals", Status: "warn", Detail: fmt.Sprintf("%d waiting for a decision", pending)})
	}
	if frozen {
		checks = append(checks, healthCheck{Name: "Kill switch", Status: "warn", Detail: "frozen: no new work starts"})
	}
	return checks
}

// overallHealth returns the worst status in the set.
func overallHealth(checks []healthCheck) string {
	worst := "ok"
	for _, c := range checks {
		if c.Status == "fail" {
			return "fail"
		}
		if c.Status == "warn" {
			worst = "warn"
		}
	}
	return worst
}

// doctorChecks adapts the CLI doctor report to the wizard's check rows.
func doctorChecks(rep doctor.Report) []healthCheck {
	out := make([]healthCheck, 0, len(rep.Checks))
	for _, c := range rep.Checks {
		out = append(out, healthCheck{
			Name: c.Name, Status: string(c.Status), Detail: c.Detail, Fix: c.Remediation,
		})
	}
	return out
}

// softenDefaultModelWarning replaces the fresh-install "configured model(s)
// not installed" row with the real next step. The shipped personas are
// examples; the Models step assigns installed models before qualification.
// Without this, a first-run user sees a five-model, tens-of-GB pull list
// before reaching the screen that lets them choose what they already have.
func softenDefaultModelWarning(checks []healthCheck, roles []setupRole) []healthCheck {
	for _, r := range roles {
		if r.Installed {
			return checks
		}
	}
	for i := range checks {
		if checks[i].Name != "models" {
			continue
		}
		if strings.HasPrefix(checks[i].Detail, "configured model(s) not installed") {
			checks[i].Detail = "no default persona model is installed yet"
			checks[i].Fix = "Choose installed models in the Models step; the defaults are examples, not requirements."
		}
	}
	return checks
}

func (u *UI) healthChecks(ctx context.Context, roles []setupRole, models []setupModel, modelsErr string) []healthCheck {
	var out []healthCheck
	out = append(out, healthCheck{
		Name: "Core", Status: "ok",
		Detail: fmt.Sprintf("%s · %s", u.deps.Runtime, u.deps.Version),
	})
	if modelsErr != "" {
		out = append(out, healthCheck{
			Name: "Ollama", Status: "fail", Detail: modelsErr,
			Fix: "Start Ollama, then reload. The Core reaches it at the configured ollama_url.",
		})
	} else {
		out = append(out, healthCheck{
			Name: "Ollama", Status: "ok",
			Detail: fmt.Sprintf("%d model(s) installed", len(models)),
		})
	}
	missing := []string{}
	for _, r := range roles {
		if !r.Installed {
			missing = append(missing, r.Role+" → "+r.Model)
		}
	}
	if len(missing) > 0 {
		out = append(out, healthCheck{
			Name: "Persona models", Status: "fail",
			Detail: "missing: " + strings.Join(missing, ", "),
			Fix:    "Pull the missing models from the Models step.",
		})
	} else if len(roles) > 0 {
		out = append(out, healthCheck{Name: "Persona models", Status: "ok", Detail: "all configured models installed"})
	}
	if u.deps.Store != nil {
		var one int
		if err := u.deps.Store.DB().QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
			out = append(out, healthCheck{Name: "State", Status: "fail", Detail: err.Error(), Fix: "Check the state volume and file permissions."})
		} else {
			out = append(out, healthCheck{Name: "State", Status: "ok", Detail: "SQLite reachable"})
		}
	}
	if u.deps.Config != nil {
		if strings.TrimSpace(u.deps.Config.JobPod.Image) == "" {
			out = append(out, healthCheck{
				Name: "Job Pod image", Status: "warn",
				Detail: "no job_pod.image configured",
				Fix:    "Build the Job Pod image and set job_pod.image; code tasks cannot execute tools without it.",
			})
		} else {
			out = append(out, healthCheck{Name: "Job Pod image", Status: "ok", Detail: u.deps.Config.JobPod.Image})
		}
		out = append(out, healthCheck{
			Name: "Network", Status: "ok",
			Detail: fmt.Sprintf("default policy %s; %d allowlisted host(s)", u.deps.Config.Network.DefaultPolicy, len(u.deps.Config.Network.AllowList)),
		})
	}
	out = append(out, healthCheck{
		Name: "Host prerequisites", Status: "warn",
		Detail: "Podman socket, memory, and disk are checked by doctor.",
		Fix:    "Run `athanor doctor` in the Core for the full host report.",
	})
	return out
}

func (u *UI) applyPersonas(ctx context.Context) error {
	if u.deps.Config == nil {
		return fmt.Errorf("configuration is not available")
	}
	if u.deps.ConfigPath == "" {
		return fmt.Errorf("config path is not available; cannot apply the assignment")
	}
	models, err := u.modelCatalog(ctx)
	if err != nil {
		return fmt.Errorf("reading installed models: %w", err)
	}
	roles := u.personaRoles(models)
	hw := Hardware{}
	if u.deps.Hardware != nil {
		hw = u.deps.Hardware(ctx)
	}
	resources := currentResources(hw, u.deps.Config)
	budget := budgetFor(roles, models, hw, resources)
	u.syncPool(ctx, models, budget, hw)
	assignments := u.autoAssignments(roles, models, hw)
	if len(assignments) == 0 {
		return fmt.Errorf("no models are installed to assign")
	}
	updates := map[string]config.PersonaConfig{}
	for _, a := range assignments {
		pc, ok := u.deps.Config.Personas.Role(a.Role)
		if !ok {
			continue
		}
		pc.Model = a.Model
		if a.Family != "" {
			pc.Family = a.Family
		}
		updates[a.Role] = pc
	}
	if err := config.UpdatePersonas(u.deps.ConfigPath, updates); err != nil {
		return err
	}
	u.markRestartRequired(ctx, "persona assignment changed; run athanor models -audit before restarting")
	if u.deps.Store != nil {
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		data := map[string]any{"event": "personas_applied"}
		for _, a := range assignments {
			data[a.Role] = a.Model
		}
		_, _ = u.deps.Store.AppendEvent(cctx, store.Event{Category: "inference", Data: data})
	}
	return nil
}

func parseFormInt(r *http.Request, name string, fallback int) int {
	raw := strings.TrimSpace(r.FormValue(name))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

func parseFormFloat(r *http.Request, name string, fallback float64) float64 {
	raw := strings.TrimSpace(r.FormValue(name))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return v
}

// writeRestartPage tells the browser the Core is restarting and when to come
// back. It is written before the process exits so a self-restart looks like a
// short pause, not a broken tab.
func (u *UI) writeRestartPage(w http.ResponseWriter, next string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="refresh" content="4;url=%s"><title>Restarting Athanor</title></head><body><h1>Restarting Athanor</h1><p>The new Job Pod limits were saved. The Core is restarting itself; this page will reload when it is back.</p><p>If it does not return, check the Core logs.</p></body></html>`, next)
}

func (u *UI) handleWelcomePost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		u.fail(w, err)
		return
	}
	ctx := r.Context()
	st := u.loadOnboarding(ctx)
	action := r.FormValue("action")
	step := r.FormValue("step")
	if validOnboardingStep(step) {
		st.Step = step
	}
	switch action {
	case "skip":
		st.Skipped = true
		st.Step = "ready"
	case "complete":
		st.Completed = true
		st.Skipped = false
		st.Version = u.deps.Version
	case "back":
		idx := onboardingIndex(st.Step)
		if idx > 0 {
			st.Step = onboardingSteps[idx-1].ID
		}
	case "next":
		// A red environment check is a real prerequisite failure. Do not let a
		// plain Continue carry the operator into work that cannot run; the UI
		// offers an explicit "Continue anyway" that sends ack=1.
		if st.Step == "environment" && r.FormValue("ack") != "1" {
			if blocked, _ := u.welcomeData(ctx, st)["HealthBlocked"].(bool); blocked {
				http.Redirect(w, r, "/ui/welcome?step=environment&blocked=1", http.StatusSeeOther)
				return
			}
		}
		idx := onboardingIndex(st.Step)
		if idx < len(onboardingSteps)-1 {
			st.Step = onboardingSteps[idx+1].ID
		} else {
			st.Completed = true
			st.Skipped = false
			st.Version = u.deps.Version
		}
	case "pull":
		model := strings.TrimSpace(r.FormValue("model"))
		if model != "" {
			u.startPull(model)
		}
	case "cancel_pull":
		model := strings.TrimSpace(r.FormValue("model"))
		u.onboardMu.Lock()
		if cancel := u.pullCancels[model]; cancel != nil {
			cancel()
		}
		if p := u.pulls[model]; p != nil && !p.Done {
			p.Status = "cancelling"
		}
		u.onboardMu.Unlock()
	case "pool_add":
		model := strings.TrimSpace(r.FormValue("model"))
		if model != "" {
			if err := u.includeModel(ctx, model); err != nil {
				u.fail(w, err)
				return
			}
		}
	case "pool_remove":
		model := strings.TrimSpace(r.FormValue("model"))
		if model != "" {
			if err := u.excludeModel(ctx, model); err != nil {
				u.fail(w, err)
				return
			}
		}
	case "run_qualify":
		if u.deps.Config == nil {
			u.fail(w, fmt.Errorf("configuration is not available to the UI"))
			return
		}
		st.Step = "qualify"
		if err := u.setQualificationTier(ctx, qualTierMinimal); err != nil {
			u.fail(w, err)
			return
		}
	case "run_qualify_more":
		if u.deps.Config == nil {
			u.fail(w, fmt.Errorf("configuration is not available to the UI"))
			return
		}
		st.Step = "qualify"
		if err := u.setQualificationTier(ctx, qualTierBalanced); err != nil {
			u.fail(w, err)
			return
		}
	case "run_qualify_full":
		if u.deps.Config == nil {
			u.fail(w, fmt.Errorf("configuration is not available to the UI"))
			return
		}
		st.Step = "qualify"
		if err := u.setQualificationTier(ctx, qualTierComplete); err != nil {
			u.fail(w, err)
			return
		}
	case "set_qual_tier":
		if u.deps.Config == nil {
			u.fail(w, fmt.Errorf("configuration is not available to the UI"))
			return
		}
		st.Step = "qualify"
		if err := u.setQualificationTier(ctx, r.FormValue("tier")); err != nil {
			u.fail(w, err)
			return
		}
	case "apply_personas":
		if err := u.applyPersonas(ctx); err != nil {
			u.fail(w, err)
			return
		}
		if err := u.saveOnboarding(ctx, st); err != nil {
			u.fail(w, err)
			return
		}
		http.Redirect(w, r, "/ui/welcome?step=models&applied=personas", http.StatusSeeOther)
		return
	case "apply_resources":
		hw := Hardware{}
		if u.deps.Hardware != nil {
			hw = u.deps.Hardware(ctx)
		}
		res := currentResources(hw, u.deps.Config)
		reserveMB := parseFormInt(r, "host_reserve_mb", res.EffectiveHostReserveMB)
		if reserveMB < res.MinHostReserveMB {
			u.fail(w, fmt.Errorf("host reserve must be at least %d MB on this machine (the Podman VM needs that much host memory); %d MB is recommended",
				res.MinHostReserveMB, res.RecommendedReserveMB))
			return
		}
		limits := config.JobPodResources{
			MemoryMB:      parseFormInt(r, "memory_mb", res.MemoryMB),
			CPUs:          parseFormFloat(r, "cpus", res.CPUsAllocated),
			TmpfsMB:       parseFormInt(r, "tmpfs_mb", res.TmpfsMB),
			PidsLimit:     parseFormInt(r, "pids_limit", res.PidsLimit),
			HostReserveMB: reserveMB,
		}
		if err := config.UpdateJobPodResources(u.deps.ConfigPath, limits); err != nil {
			u.fail(w, err)
			return
		}
		if err := u.saveOnboarding(ctx, st); err != nil {
			u.fail(w, err)
			return
		}
		back := "/ui/welcome?step=resources&applied=resources"
		if st.Step != "" {
			back = "/ui/welcome?step=" + st.Step + "&applied=resources"
		}
		if u.deps.Restart != nil {
			// The supervisor restarts the Core in place; the browser waits on
			// this page and reloads when it comes back. Return to the step that
			// asked for the change (Resources or Models).
			u.writeRestartPage(w, back)
			go func() {
				time.Sleep(1500 * time.Millisecond)
				u.deps.Restart()
			}()
			return
		}
		u.markRestartRequired(ctx, "Job Pod resource limits changed")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	if err := u.saveOnboarding(ctx, st); err != nil {
		u.fail(w, err)
		return
	}
	if st.Skipped {
		http.Redirect(w, r, "/ui", http.StatusSeeOther)
		return
	}
	if st.Completed {
		http.Redirect(w, r, "/ui/projects/new", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/ui/welcome?step="+st.Step, http.StatusSeeOther)
}

// startPull runs one Ollama model pull in the background and tracks its
// progress for the wizard. The pull is explicit (a button press), never a
// silent multi-gigabyte download.
func (u *UI) startPull(model string) {
	u.onboardMu.Lock()
	if u.pulls == nil {
		u.pulls = map[string]*pullProgress{}
	}
	if p, ok := u.pulls[model]; ok && !p.Done {
		u.onboardMu.Unlock()
		return
	}
	pruneRuns(u.pulls, maxRunHistory)
	u.pulls[model] = &pullProgress{Status: "starting", StartedAt: time.Now()}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	u.pullCancels[model] = cancel
	u.onboardMu.Unlock()
	go u.runPull(ctx, model)
}

func (u *UI) runPull(ctx context.Context, model string) {
	defer u.invalidateCatalog()
	defer func() {
		u.onboardMu.Lock()
		delete(u.pullCancels, model)
		u.onboardMu.Unlock()
	}()
	base := ""
	if u.deps.Config != nil {
		base = u.deps.Config.Inference.OllamaURL
	}
	fail := func(msg string) {
		u.onboardMu.Lock()
		if p := u.pulls[model]; p != nil {
			p.Error = msg
			p.Done = true
			p.Status = "failed"
		}
		u.onboardMu.Unlock()
	}
	if base == "" {
		fail("inference.ollama_url is not configured")
		return
	}
	body, err := json.Marshal(map[string]any{"model": model, "stream": true})
	if err != nil {
		fail(err.Error())
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(base, "/")+"/api/pull", bytes.NewReader(body))
	if err != nil {
		fail(err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 0}).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			fail("cancelled by operator")
			return
		}
		fail(err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		fail(fmt.Sprintf("ollama returned %s", resp.Status))
		return
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var line struct {
			Status    string `json:"status"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
			Error     string `json:"error"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			continue
		}
		u.onboardMu.Lock()
		if p := u.pulls[model]; p != nil {
			p.Status = line.Status
			if line.Total > 0 {
				p.Total = line.Total
			}
			if line.Completed > 0 {
				p.Completed = line.Completed
			}
			if line.Error != "" {
				p.Error = line.Error
			}
		}
		u.onboardMu.Unlock()
	}
	if err := sc.Err(); err != nil {
		if ctx.Err() != nil {
			fail("cancelled by operator")
			return
		}
		fail(err.Error())
		return
	}
	u.onboardMu.Lock()
	if p := u.pulls[model]; p != nil {
		p.Done = true
		p.Status = "pulled"
		if p.Total > 0 {
			p.Completed = p.Total
		}
	}
	u.onboardMu.Unlock()
}
