package ui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
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

var onboardingSteps = []onboardingStep{
	{"welcome", "Welcome", "What Athanor is and what it will do"},
	{"environment", "Environment", "Core, Ollama, database, and models"},
	{"models", "Models", "Persona models and one-click pulls"},
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
	health := u.healthChecks(ctx, roles, models, modelsErr)
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
	return map[string]any{
		"State": st, "Step": st.Step, "Steps": onboardingSteps,
		"StepIndex": onboardingIndex(st.Step), "Roles": roles, "Models": models,
		"ModelsErr": modelsErr, "Health": health, "Pulls": pulls,
		"Themes": themeNames, "Theme": u.Theme(), "ThemeLabel": themeLabel(u.Theme()),
		"OllamaURL": func() string {
			if u.deps.Config == nil {
				return ""
			}
			return u.deps.Config.Inference.OllamaURL
		}(),
	}
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
