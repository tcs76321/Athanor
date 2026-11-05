package ui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	TotalMemoryGiB float64
	FreeDiskGiB    float64
	CPUs           int
	MemoryMB       int
	MemoryMaxMB    int
	CPUsAllocated  float64
	CPUsMax        float64
	TmpfsMB        int
	PidsLimit      int
	ReserveMB      int
}

type modelBudget struct {
	TotalGiB   float64
	LargestGiB float64
	PodGiB     float64
	ReserveGiB float64
	SpareGiB   float64
	Overcommit bool
}

func budgetFor(models []setupModel, hw Hardware, res resourceView) modelBudget {
	b := modelBudget{
		TotalGiB:   float64(hw.TotalMemoryBytes) / (1 << 30),
		PodGiB:     float64(res.MemoryMB) / 1024,
		ReserveGiB: float64(res.ReserveMB) / 1024,
	}
	for _, m := range models {
		if g := float64(m.SizeBytes) / (1 << 30); g > b.LargestGiB {
			b.LargestGiB = g
		}
	}
	b.SpareGiB = b.TotalGiB - b.LargestGiB - b.PodGiB - b.ReserveGiB
	b.Overcommit = b.TotalGiB > 0 && b.SpareGiB < 0
	return b
}

var onboardingSteps = []onboardingStep{
	{"welcome", "Welcome", "What Athanor is and what it will do"},
	{"environment", "Environment", "Doctor checks run from the GUI"},
	{"resources", "Resources", "RAM, CPU, and Job Pod limits"},
	{"models", "Models", "Personas, installed models, and self-selection"},
	{"qualify", "Qualification", "Optional bounded routing screen"},
	{"preferences", "Preferences", "Theme and notifications"},
	{"ready", "Ready", "Create the first project"},
}

func (u *UI) loadOnboarding(ctx context.Context) onboardingState {
	st := onboardingState{Step: "welcome"}
	if u.deps.Store == nil {
		return st
	}
	var raw string
	if err := u.deps.Store.DB().QueryRowContext(ctx,
		`SELECT value FROM system_state WHERE key = ?`, onboardingKey).Scan(&raw); err != nil {
		return st
	}
	_ = json.Unmarshal([]byte(raw), &st)
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
	_, err = u.deps.Store.DB().ExecContext(ctx,
		`INSERT INTO system_state (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		onboardingKey, string(raw))
	if err != nil {
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
		models, err = fetchModels(ctx, u.deps.Config.Inference.OllamaURL)
		if err != nil {
			modelsErr = err.Error()
		}
		roles = u.personaRoles(models)
	}
	health := []healthCheck{}
	if st.Step == "environment" {
		if u.deps.Doctor != nil {
			health = doctorChecks(u.deps.Doctor(ctx))
		} else {
			health = u.healthChecks(ctx, roles, models, modelsErr)
		}
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
	assignments := u.autoAssignments(roles, models, hw)
	suggestions := u.pullSuggestions(roles)
	resources := recommendedResources(hw, u.deps.Config)
	budget := budgetFor(models, hw, resources)
	return map[string]any{
		"State": st, "Step": st.Step, "Steps": onboardingSteps,
		"StepIndex": onboardingIndex(st.Step), "StepNumber": onboardingIndex(st.Step) + 1,
		"StepCount": len(onboardingSteps), "Roles": roles, "Models": models,
		"ModelsErr": modelsErr, "Health": health, "Pulls": pulls,
		"Themes": themeNames, "Theme": u.Theme(), "ThemeLabel": themeLabel(u.Theme()),
		"PersonaGuides": personaGuides, "Assignments": assignments,
		"Suggestions": suggestions, "Resources": resources, "Hardware": hw,
		"Budget": budget, "ConfigPath": u.deps.ConfigPath,
		"OllamaURL": func() string {
			if u.deps.Config == nil {
				return ""
			}
			return u.deps.Config.Inference.OllamaURL
		}(),
	}
}

// autoAssignments converts the UI's catalog to the deterministic selector.
func (u *UI) autoAssignments(roles []setupRole, models []setupModel, hw Hardware) []onboard.Assignment {
	if len(models) == 0 {
		return nil
	}
	oroles := make([]onboard.Role, 0, len(roles))
	for _, r := range roles {
		oroles = append(oroles, onboard.Role{Role: r.Role, Model: r.Model, ContextTarget: r.ContextTarget})
	}
	if len(oroles) == 0 {
		for _, name := range []string{"wide", "tall", "main", "security", "alternative"} {
			oroles = append(oroles, onboard.Role{Role: name})
		}
	}
	omodels := make([]onboard.Model, 0, len(models))
	for _, m := range models {
		omodels = append(omodels, onboard.Model{Name: m.Name, SizeBytes: m.SizeBytes, Family: m.Family})
	}
	reserve := int64(2 << 30)
	return onboard.AutoAssign(oroles, omodels, int64(hw.TotalMemoryBytes), reserve)
}

// pullSuggestions lists configured persona models that are not installed,
// deduplicated and without role labels; the Models step explains the personas
// separately so the operator is not asked to map them by hand.
func (u *UI) pullSuggestions(roles []setupRole) []pullSuggestion {
	seen := map[string]bool{}
	out := []pullSuggestion{}
	for _, r := range roles {
		if r.Installed || r.Model == "" || seen[r.Model] {
			continue
		}
		seen[r.Model] = true
		out = append(out, pullSuggestion{Model: r.Model, Reason: "expected by this configuration"})
	}
	return out
}

// recommendedResources proposes Job Pod limits from host capacity, while
// preserving values already set in config.
func recommendedResources(hw Hardware, cfg *config.Config) resourceView {
	totalMB := int(hw.TotalMemoryBytes / (1 << 20))
	res := resourceView{
		TotalMemoryGiB: float64(hw.TotalMemoryBytes) / (1 << 30),
		FreeDiskGiB:    float64(hw.FreeDiskBytes) / (1 << 30),
		CPUs:           hw.CPUs,
		MemoryMB:       4096,
		TmpfsMB:        2048,
		PidsLimit:      256,
		CPUsAllocated:  2,
		CPUsMax:        float64(hw.CPUs),
	}
	if res.CPUsMax < 1 {
		res.CPUsMax = 8
	}
	if res.CPUsMax > 16 {
		res.CPUsMax = 16
	}
	if res.CPUs < 4 {
		res.CPUsAllocated = 1
	}
	if totalMB > 0 {
		proposed := totalMB / 4
		if proposed < 1024 {
			proposed = 1024
		}
		if proposed > 4096 {
			proposed = 4096
		}
		res.MemoryMB = (proposed / 512) * 512
		res.MemoryMaxMB = totalMB - 2048
	} else {
		res.MemoryMaxMB = 65536
	}
	if res.MemoryMaxMB < 1024 {
		res.MemoryMaxMB = 1024
	}
	if res.MemoryMaxMB > 65536 {
		res.MemoryMaxMB = 65536
	}
	res.ReserveMB = 2048
	if cfg != nil {
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
	}
	if res.MemoryMB > res.MemoryMaxMB {
		res.MemoryMB = res.MemoryMaxMB
	}
	if res.CPUsAllocated > res.CPUsMax {
		res.CPUsAllocated = res.CPUsMax
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
		models, err := fetchModels(mctx, u.deps.Config.Inference.OllamaURL)
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
				checks = append(checks, healthCheck{Name: "Models", Status: "fail", Detail: "missing: " + strings.Join(missing, ", ")})
			} else if len(roles) > 0 {
				checks = append(checks, healthCheck{Name: "Models", Status: "ok", Detail: "all persona models present"})
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
	models, err := fetchModels(ctx, u.deps.Config.Inference.OllamaURL)
	if err != nil {
		return fmt.Errorf("reading installed models: %w", err)
	}
	roles := u.personaRoles(models)
	hw := Hardware{}
	if u.deps.Hardware != nil {
		hw = u.deps.Hardware(ctx)
	}
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
	u.markRestartRequired(ctx, "persona assignment changed")
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
		mem, merr := strconv.Atoi(r.FormValue("memory_mb"))
		cpus, cerr := strconv.ParseFloat(r.FormValue("cpus"), 64)
		tmpfs, terr := strconv.Atoi(r.FormValue("tmpfs_mb"))
		pids, perr := strconv.Atoi(r.FormValue("pids_limit"))
		if merr != nil || cerr != nil || terr != nil || perr != nil {
			u.fail(w, fmt.Errorf("resource values must be numbers"))
			return
		}
		if err := config.UpdateJobPodResources(u.deps.ConfigPath, config.JobPodResources{
			MemoryMB: mem, CPUs: cpus, TmpfsMB: tmpfs, PidsLimit: pids,
		}); err != nil {
			u.fail(w, err)
			return
		}
		u.markRestartRequired(ctx, "Job Pod resource limits changed")
		if err := u.saveOnboarding(ctx, st); err != nil {
			u.fail(w, err)
			return
		}
		http.Redirect(w, r, "/ui/welcome?step=resources&applied=resources", http.StatusSeeOther)
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
	u.pulls[model] = &pullProgress{Status: "starting"}
	u.onboardMu.Unlock()
	go u.runPull(model)
}

func (u *UI) runPull(model string) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
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
