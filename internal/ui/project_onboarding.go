package ui

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/discovery"
	"github.com/tcs76321/athanor/internal/hitl"
	"github.com/tcs76321/athanor/internal/planning"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
)

// Discovery is the option-portfolio surface the project onboarding drives.
// Satisfied by *discovery.Service; nil disables the discovery step.
type Discovery interface {
	Generate(ctx context.Context, projectID string, req discovery.Request) (discovery.Session, error)
	Get(ctx context.Context, projectID string) (discovery.Session, bool, error)
	Select(ctx context.Context, projectID, optionID string) error
}

// Planning is the AI-authored plan surface. Satisfied by *planning.Service;
// nil keeps the deterministic plan fallback.
type Planning interface {
	Generate(ctx context.Context, projectID, optionID string, req planning.Request) (planning.Session, error)
	Get(ctx context.Context, projectID string) (planning.Session, bool, error)
	Select(ctx context.Context, projectID string, index int) error
}

// Scheduler starts the ready leaves of a decomposed goal. Satisfied by
// *scheduler.Scheduler; nil falls back to starting the first task only.
type Scheduler interface {
	Start(ctx context.Context, goalID string) ([]string, error)
}

type projectBrief struct {
	ProjectID   string
	Status      string
	Goal        string
	Constraints []string
	Limitations string
	Sources     []string
	Success     string
}

type discoveryRun struct {
	Status        string
	Stage         string
	Detail        string
	Model         string
	Error         string
	StartedAt     time.Time
	UpdatedAt     time.Time
	PromptBytes   int
	ResponseBytes int
	Options       int
	Tail          string
	Steps         []discoveryStep
	Session       discovery.Session
}

type discoveryStep struct {
	Stage  string
	Detail string
	At     time.Time
}

type planRun struct {
	OptionID  string
	Status    string
	Error     string
	StartedAt time.Time
	Session   planning.Session
}

// sourceStatus is the allowlist state of one human-supplied research URL.
type sourceStatus struct {
	URL     string
	Host    string
	Allowed bool
}

func sourceStatuses(sources []string, cfg *config.Config) []sourceStatus {
	out := make([]sourceStatus, 0, len(sources))
	var allow []string
	if cfg != nil {
		allow = cfg.Network.AllowList
	}
	for _, raw := range sources {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		host := ""
		if u, err := url.Parse(raw); err == nil {
			host = strings.ToLower(u.Hostname())
		}
		out = append(out, sourceStatus{URL: raw, Host: host, Allowed: host != "" && hostAllowed(host, allow)})
	}
	return out
}

// hostAllowed applies the gateway's suffix-match rule with an explicit dot
// boundary, so "wikipedia.org" does not allow "evilwikipedia.org".
func hostAllowed(host string, allow []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, entry := range allow {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if host == entry || strings.HasSuffix(host, "."+entry) {
			return true
		}
	}
	return false
}

func (u *UI) loadBrief(ctx context.Context, p project.Project) (projectBrief, error) {
	b := projectBrief{ProjectID: p.ID, Status: "pending", Goal: p.Goal}
	if u.deps.Store == nil {
		return b, nil
	}
	var constraints, sources string
	err := u.deps.Store.DB().QueryRowContext(ctx, `
		SELECT status, goal, constraints_json, limitations, sources_json, success
		  FROM project_briefs WHERE project_id = ?`, p.ID).
		Scan(&b.Status, &b.Goal, &constraints, &b.Limitations, &sources, &b.Success)
	if err == sql.ErrNoRows {
		return b, nil
	}
	if err != nil {
		return b, err
	}
	_ = json.Unmarshal([]byte(constraints), &b.Constraints)
	_ = json.Unmarshal([]byte(sources), &b.Sources)
	return b, nil
}

func (u *UI) saveBrief(ctx context.Context, b projectBrief) error {
	if u.deps.Store == nil {
		return nil
	}
	constraints, _ := json.Marshal(b.Constraints)
	sources, _ := json.Marshal(b.Sources)
	_, err := u.deps.Store.DB().ExecContext(ctx, `
		INSERT INTO project_briefs (project_id, status, goal, constraints_json, limitations, sources_json, success)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id) DO UPDATE SET
		  status = excluded.status, goal = excluded.goal,
		  constraints_json = excluded.constraints_json,
		  limitations = excluded.limitations,
		  sources_json = excluded.sources_json,
		  success = excluded.success`,
		b.ProjectID, b.Status, b.Goal, string(constraints), b.Limitations, string(sources), b.Success)
	if err != nil {
		return fmt.Errorf("saving project brief: %w", err)
	}
	return nil
}

func (u *UI) handleProjectOnboarding(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := u.deps.Projects.Get(ctx, r.PathValue("id"))
	if err != nil {
		u.fail(w, err)
		return
	}
	brief, err := u.loadBrief(ctx, p)
	if err != nil {
		u.fail(w, err)
		return
	}
	var sess discovery.Session
	hasSession := false
	if u.deps.Discovery != nil {
		sess, hasSession, err = u.deps.Discovery.Get(ctx, p.ID)
		if err != nil {
			u.fail(w, err)
			return
		}
	}
	var planSess planning.Session
	hasPlan := false
	if u.deps.Planning != nil {
		planSess, hasPlan, err = u.deps.Planning.Get(ctx, p.ID)
		if err != nil {
			u.fail(w, err)
			return
		}
	}
	// A plan is scoped to the direction it was generated for. After a reset or
	// a different pick, an old ready plan must not be shown or approved for the
	// new direction.
	if hasPlan && hasSession && sess.SelectedID != "" && planSess.OptionID != "" &&
		planSess.OptionID != sess.SelectedID {
		hasPlan = false
		planSess = planning.Session{}
	}
	pRun := u.planRunFor(p.ID)
	if pRun != nil && pRun.OptionID != sess.SelectedID {
		pRun = nil
	}
	run := u.discoveryRunFor(p.ID)
	stage := "brief"
	discoveryError := ""
	planError := ""
	switch {
	case brief.Status == "approved":
		stage = "approved"
	case hasSession && sess.SelectedID != "" && brief.Status == "planned":
		switch {
		case hasPlan && planSess.Status == "generating":
			if time.Since(planSess.UpdatedAt) > 20*time.Minute {
				stage = "plan"
				planError = "The previous plan generation was interrupted. Generate again when ready."
			} else {
				stage = "generating_plan"
			}
		case hasPlan && planSess.Status == "failed":
			stage = "plan"
			planError = planSess.Error
		case hasPlan && len(planSess.Candidates) > 0:
			stage = "plan"
		case pRun != nil && pRun.Status == "running":
			stage = "generating_plan"
		case pRun != nil && pRun.Status == "failed":
			stage = "plan"
			planError = pRun.Error
		default:
			stage = "plan"
		}
	case hasSession && len(sess.Options) > 0:
		stage = "choose"
	case hasSession && sess.Status == "generating":
		if time.Since(sess.UpdatedAt) > 20*time.Minute {
			stage = "brief"
			discoveryError = "The previous discovery run was interrupted. Start it again when you are ready."
		} else {
			stage = "generating"
		}
	case hasSession && sess.Status == "failed":
		stage = "brief"
		discoveryError = sess.Error
	case run != nil && run.Status == "running":
		stage = "generating"
	case run != nil && run.Status == "failed":
		stage = "brief"
		discoveryError = run.Error
	}
	var selected *discovery.Option
	if hasSession && sess.SelectedID != "" {
		for i := range sess.Options {
			if sess.Options[i].ID == sess.SelectedID {
				selected = &sess.Options[i]
				break
			}
		}
	}
	discPercent, discElapsed := discoveryProgress(run)
	if run == nil && hasSession && sess.Status == "generating" && !sess.UpdatedAt.IsZero() {
		discPercent = 30
		discElapsed = time.Since(sess.UpdatedAt).Round(time.Second).String()
	}
	var selectedPlan *planning.Candidate
	if hasPlan && planSess.SelectedIndex >= 0 && planSess.SelectedIndex < len(planSess.Candidates) {
		selectedPlan = &planSess.Candidates[planSess.SelectedIndex]
	}
	plan := ""
	if selected != nil {
		plan, _ = buildProjectPlan(p, brief, *selected)
	}
	u.render(w, "Onboarding "+p.Name, projectOnboardingBody, map[string]any{
		"Project": p, "Brief": brief, "Session": sess, "HasSession": hasSession,
		"Run": run, "Stage": stage, "Selected": selected, "Plan": plan,
		"Status": brief.Status, "Sources": sourceStatuses(brief.Sources, u.deps.Config),
		"Requested": r.URL.Query().Get("requested") != "", "DiscoveryError": discoveryError,
		"DiscoveryPercent": discPercent, "DiscoveryElapsed": discElapsed,
		"PlanningAvailable": u.deps.Planning != nil,
		"PlanCandidates":    planSess.Candidates,
		"SelectedPlan":      selectedPlan, "PlanError": planError,
	})
}

func (u *UI) handleProjectOnboardingPost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := u.deps.Projects.Get(ctx, r.PathValue("id"))
	if err != nil {
		u.fail(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		u.fail(w, err)
		return
	}
	brief, err := u.loadBrief(ctx, p)
	if err != nil {
		u.fail(w, err)
		return
	}
	action := r.FormValue("action")
	switch action {
	case "save", "discover":
		brief.Goal = strings.TrimSpace(r.FormValue("goal"))
		if brief.Goal == "" {
			brief.Goal = p.Goal
		}
		brief.Constraints = splitCriteria(r.FormValue("constraints"))
		brief.Limitations = strings.TrimSpace(r.FormValue("limitations"))
		brief.Sources = splitCriteria(r.FormValue("sources"))
		brief.Success = strings.TrimSpace(r.FormValue("success"))
		brief.Status = "briefed"
		if err := u.saveBrief(ctx, brief); err != nil {
			u.fail(w, err)
			return
		}
		if action == "discover" {
			u.startDiscovery(p, brief)
		}
	case "select":
		if u.deps.Discovery == nil {
			u.fail(w, fmt.Errorf("discovery is not configured"))
			return
		}
		id := r.FormValue("option")
		if err := u.deps.Discovery.Select(ctx, p.ID, id); err != nil {
			u.fail(w, err)
			return
		}
		brief.Status = "planned"
		if err := u.saveBrief(ctx, brief); err != nil {
			u.fail(w, err)
			return
		}
	case "generate_plans":
		if u.deps.Planning == nil {
			u.fail(w, fmt.Errorf("AI planning is not configured"))
			return
		}
		if u.deps.Discovery == nil {
			u.fail(w, fmt.Errorf("discovery is not configured"))
			return
		}
		sess, ok, derr := u.deps.Discovery.Get(ctx, p.ID)
		if derr != nil {
			u.fail(w, derr)
			return
		}
		if !ok || sess.SelectedID == "" {
			u.fail(w, fmt.Errorf("choose a direction first"))
			return
		}
		var option *discovery.Option
		for i := range sess.Options {
			if sess.Options[i].ID == sess.SelectedID {
				option = &sess.Options[i]
				break
			}
		}
		if option == nil {
			u.fail(w, fmt.Errorf("the selected direction is missing"))
			return
		}
		u.startPlanning(p, brief, *option)
	case "select_plan":
		if u.deps.Planning == nil {
			u.fail(w, fmt.Errorf("AI planning is not configured"))
			return
		}
		if u.deps.Discovery == nil {
			u.fail(w, fmt.Errorf("discovery is not configured"))
			return
		}
		dsess, ok, derr := u.deps.Discovery.Get(ctx, p.ID)
		if derr != nil {
			u.fail(w, derr)
			return
		}
		if !ok || dsess.SelectedID == "" {
			u.fail(w, fmt.Errorf("choose a direction first"))
			return
		}
		psess, has, perr := u.deps.Planning.Get(ctx, p.ID)
		if perr != nil {
			u.fail(w, perr)
			return
		}
		if !has || psess.OptionID != dsess.SelectedID {
			u.fail(w, fmt.Errorf("the plans were generated for a different direction; generate them again"))
			return
		}
		idx, perr := strconv.Atoi(r.FormValue("plan"))
		if perr != nil {
			u.fail(w, fmt.Errorf("invalid plan selection"))
			return
		}
		if err := u.deps.Planning.Select(ctx, p.ID, idx); err != nil {
			u.fail(w, err)
			return
		}
		u.auditProject(p.ID, "plan_selected", map[string]any{
			"option": dsess.SelectedID, "index": idx, "title": psess.Candidates[idx].Title,
		})
		brief.Status = "planned"
		if err := u.saveBrief(ctx, brief); err != nil {
			u.fail(w, err)
			return
		}
	case "request_access":
		source := strings.TrimSpace(r.FormValue("source"))
		host := ""
		if u, perr := url.Parse(source); perr == nil {
			host = strings.ToLower(u.Hostname())
		}
		if source == "" || host == "" {
			u.fail(w, fmt.Errorf("a valid source URL is required"))
			return
		}
		if u.deps.HITLRepo == nil {
			u.fail(w, fmt.Errorf("the approval queue is not configured"))
			return
		}
		payload, _ := json.Marshal(map[string]any{
			"host": host, "url": source, "project": p.ID,
			"instruction": "Add this host to network.allow_list in config.yaml, then restart or reload the Core. The UI does not rewrite network policy.",
		})
		expires := time.Now().Add(24 * time.Hour)
		if _, err := u.deps.HITLRepo.Create(ctx, hitl.Request{
			ProjectID: p.ID, Type: hitl.TypeNetworkAccess,
			Severity: hitl.SeverityHigh, PayloadJSON: string(payload),
			ExpiresAt: &expires,
		}); err != nil {
			u.fail(w, err)
			return
		}
		u.auditProject(p.ID, "network_access_requested", map[string]any{"host": host, "url": source})
		http.Redirect(w, r, "/ui/projects/"+p.ID+"/onboarding?requested=1", http.StatusSeeOther)
		return
	case "approve":
		if err := u.approveProjectPlan(ctx, p, brief); err != nil {
			u.fail(w, err)
			return
		}
	case "reset":
		brief.Status = "briefed"
		if err := u.saveBrief(ctx, brief); err != nil {
			u.fail(w, err)
			return
		}
	}
	http.Redirect(w, r, "/ui/projects/"+p.ID+"/onboarding", http.StatusSeeOther)
}

func (u *UI) discoveryRunFor(projectID string) *discoveryRun {
	u.discMu.Lock()
	defer u.discMu.Unlock()
	if run := u.discRuns[projectID]; run != nil {
		cp := *run
		cp.Steps = append([]discoveryStep(nil), run.Steps...)
		return &cp
	}
	return nil
}

// reportDiscovery applies one generator progress event to the visible run.
func (u *UI) reportDiscovery(projectID string, ev discovery.ProgressEvent) {
	u.discMu.Lock()
	defer u.discMu.Unlock()
	run := u.discRuns[projectID]
	if run == nil {
		return
	}
	now := time.Now()
	run.Stage = ev.Stage
	if ev.Detail != "" {
		run.Detail = ev.Detail
	}
	run.UpdatedAt = now
	if ev.Stage == "prompt" {
		run.PromptBytes = ev.Bytes
	} else if ev.Bytes > 0 {
		run.ResponseBytes = ev.Bytes
	}
	if ev.Options > 0 {
		run.Options = ev.Options
	}
	if ev.Tail != "" {
		run.Tail = ev.Tail
	}
	if n := len(run.Steps); n == 0 || run.Steps[n-1].Stage != ev.Stage || run.Steps[n-1].Detail != ev.Detail {
		run.Steps = append(run.Steps, discoveryStep{Stage: ev.Stage, Detail: ev.Detail, At: now})
		if len(run.Steps) > 14 {
			run.Steps = run.Steps[len(run.Steps)-14:]
		}
	}
}

// discoveryProgress maps the current discovery stage to a coarse, honest
// percent. The model call is a single response, so the streaming phase grows
// with received bytes rather than pretending to know exact completion.
func discoveryProgress(run *discoveryRun) (int, string) {
	if run == nil {
		return 0, ""
	}
	stage := run.Stage
	percent := 0
	switch stage {
	case "starting":
		percent = 5
	case "session":
		percent = 10
	case "prompt":
		percent = 15
	case "model":
		percent = 20
	case "streaming":
		percent = 25 + run.ResponseBytes/400
		if percent > 70 {
			percent = 70
		}
	case "response":
		percent = 78
	case "parse":
		percent = 82
	case "dedupe":
		percent = 84
	case "validate":
		percent = 86
	case "score":
		percent = 90
	case "save":
		percent = 95
	case "done":
		percent = 100
	case "failed":
		percent = 100
	}
	if percent > 100 {
		percent = 100
	}
	elapsed := ""
	if !run.StartedAt.IsZero() {
		elapsed = time.Since(run.StartedAt).Round(time.Second).String()
	}
	return percent, elapsed
}

func (u *UI) startDiscovery(p project.Project, brief projectBrief) {
	if u.deps.Discovery == nil {
		return
	}
	u.discMu.Lock()
	if run := u.discRuns[p.ID]; run != nil && run.Status == "running" {
		u.discMu.Unlock()
		return
	}
	model := ""
	if u.deps.Config != nil {
		if pc, ok := u.deps.Config.Personas.Role("tall"); ok {
			model = pc.Model
		} else if pc, ok := u.deps.Config.Personas.Role("main"); ok {
			model = pc.Model
		}
	}
	run := &discoveryRun{Status: "running", Stage: "starting", Detail: "preparing discovery", Model: model,
		StartedAt: time.Now(), UpdatedAt: time.Now()}
	u.discRuns[p.ID] = run
	u.discMu.Unlock()

	u.auditProject(p.ID, "discovery_started", map[string]any{
		"goal": brief.Goal, "constraints": len(brief.Constraints), "sources": len(brief.Sources),
		"model": model,
	})

	req := discovery.Request{
		Goal: brief.Goal, Brief: brief.Limitations, Constraints: brief.Constraints,
		Sources: brief.Sources, Count: 10,
		Progress: func(ev discovery.ProgressEvent) { u.reportDiscovery(p.ID, ev) },
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		sess, err := u.deps.Discovery.Generate(ctx, p.ID, req)
		u.discMu.Lock()
		defer u.discMu.Unlock()
		if err != nil {
			run.Status = "failed"
			run.Stage = "failed"
			run.Detail = "discovery failed"
			run.Error = err.Error()
			run.UpdatedAt = time.Now()
			u.auditProject(p.ID, "discovery_failed", map[string]any{"error": err.Error()})
			return
		}
		run.Status = "done"
		run.Stage = "done"
		run.Detail = "portfolio ready"
		run.UpdatedAt = time.Now()
		run.Options = len(sess.Options)
		run.Session = sess
		u.auditProject(p.ID, "discovery_completed", map[string]any{
			"session": sess.ID, "options": len(sess.Options),
		})
	}()
}

func (u *UI) planRunFor(projectID string) *planRun {
	u.planMu.Lock()
	defer u.planMu.Unlock()
	if run := u.planRuns[projectID]; run != nil {
		cp := *run
		return &cp
	}
	return nil
}

func (u *UI) startPlanning(p project.Project, brief projectBrief, option discovery.Option) {
	if u.deps.Planning == nil {
		return
	}
	u.planMu.Lock()
	if run := u.planRuns[p.ID]; run != nil && run.Status == "running" {
		u.planMu.Unlock()
		return
	}
	run := &planRun{OptionID: option.ID, Status: "running", StartedAt: time.Now()}
	u.planRuns[p.ID] = run
	u.planMu.Unlock()

	u.auditProject(p.ID, "plan_started", map[string]any{"option": option.ID, "title": option.Title})
	req := planning.Request{
		Goal: brief.Goal, Brief: brief.Limitations, Constraints: brief.Constraints,
		Sources: brief.Sources, OptionTitle: option.Title, OneLiner: option.OneLiner,
		Customer: option.Customer, Pain: option.Pain, Wedge: option.Wedge,
		Offer: option.FirstOffer, Validation: option.ValidationExperiment,
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		sess, err := u.deps.Planning.Generate(ctx, p.ID, option.ID, req)
		u.planMu.Lock()
		defer u.planMu.Unlock()
		if err != nil {
			run.Status = "failed"
			run.Error = err.Error()
			u.auditProject(p.ID, "plan_failed", map[string]any{"error": err.Error()})
			return
		}
		run.Status = "done"
		run.Session = sess
		u.auditProject(p.ID, "plan_completed", map[string]any{
			"session": sess.ID, "candidates": len(sess.Candidates),
		})
	}()
}

// auditProject appends a project-scoped event on a short detached context so a
// cancelled request cannot drop the audit row.
func (u *UI) auditProject(projectID, event string, data map[string]any) {
	if u.deps.Store == nil {
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	data["event"] = event
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := u.deps.Store.AppendEvent(ctx, store.Event{
		Category: "projects", ProjectID: projectID, Data: data,
	}); err != nil {
		slog.Error("ui: auditing project event", "project", projectID, "event", event, "err", err)
	}
}

// buildProjectPlan turns the chosen option and the brief into a reviewable
// plan and a dependency-ordered task graph. It is deterministic: the same
// inputs produce the same plan, and no model call is needed at approval time.
func buildProjectPlan(p project.Project, b projectBrief, o discovery.Option) (string, []project.TaskSpec) {
	var out strings.Builder
	fmt.Fprintf(&out, "# %s\n\n", o.Title)
	fmt.Fprintf(&out, "**Direction:** %s\n\n", o.OneLiner)
	fmt.Fprintf(&out, "**Project:** %s\n\n", p.Name)
	fmt.Fprintf(&out, "## Why this direction\n\n")
	fmt.Fprintf(&out, "- **Customer:** %s\n", o.Customer)
	fmt.Fprintf(&out, "- **Pain:** %s\n", o.Pain)
	fmt.Fprintf(&out, "- **Wedge:** %s\n", o.Wedge)
	fmt.Fprintf(&out, "- **Business model:** %s\n", o.BusinessModel)
	fmt.Fprintf(&out, "- **Pricing:** %s\n", o.Pricing)
	fmt.Fprintf(&out, "- **First offer:** %s\n", o.FirstOffer)
	fmt.Fprintf(&out, "- **Time to first revenue:** %d days\n", o.TimeToFirstRevenueDays)
	fmt.Fprintf(&out, "- **Startup cost:** $%d\n", o.StartupCostUSD)
	if o.Competition != "" {
		fmt.Fprintf(&out, "- **Competition:** %s\n", o.Competition)
	}
	out.WriteString("\n## Assumptions to test\n\n")
	if len(o.Assumptions) == 0 {
		out.WriteString("- (none recorded)\n")
	} else {
		for _, a := range o.Assumptions {
			fmt.Fprintf(&out, "- %s\n", a)
		}
	}
	out.WriteString("\n## Risks\n\n")
	if len(o.Risks) == 0 {
		out.WriteString("- (none recorded)\n")
	} else {
		for _, r := range o.Risks {
			fmt.Fprintf(&out, "- %s\n", r)
		}
	}
	out.WriteString("\n## First 30 days\n\n")
	fmt.Fprintf(&out, "1. **Validate demand.** %s\n", o.ValidationExperiment)
	fmt.Fprintf(&out, "2. **Build the first offer.** %s\n", o.FirstOffer)
	out.WriteString("3. **Find 25 prospects** and send a first, personal outreach message.\n")
	out.WriteString("4. **Decide.** Review the evidence and choose go, adjust, or stop.\n")
	out.WriteString("\n## Limits\n\n")
	if len(b.Constraints) == 0 {
		out.WriteString("- (none recorded)\n")
	} else {
		for _, c := range b.Constraints {
			fmt.Fprintf(&out, "- %s\n", c)
		}
	}
	if b.Limitations != "" {
		fmt.Fprintf(&out, "\n%s\n", b.Limitations)
	}
	if len(b.Sources) > 0 {
		out.WriteString("\n## Sources\n\n")
		for _, s := range b.Sources {
			fmt.Fprintf(&out, "- %s\n", s)
		}
	}

	criteria := []string{}
	if strings.TrimSpace(b.Success) != "" {
		for _, line := range strings.Split(b.Success, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				criteria = append(criteria, line)
			}
		}
	}
	sourceLine := ""
	if len(b.Sources) > 0 {
		sourceLine = " Allowed sources (fetch only these): " + strings.Join(b.Sources, " ")
	}
	specs := []project.TaskSpec{
		{
			Key: "validate", Title: "Validate demand", Capability: project.CapabilityText,
			Description: fmt.Sprintf("Run the validation experiment for %q: %s. Record what you learned, including who you spoke to, what they said, and whether they would pay.%s", o.Title, o.ValidationExperiment, sourceLine),
			Criteria:    criteria,
		},
		{
			Key: "offer", Title: "Draft the first offer", Capability: project.CapabilityText,
			Description: fmt.Sprintf("Write the first paid offer for %s: %s. Include scope, price, a one-page description, and the exact ask. Customer: %s. Pain: %s.", o.Title, o.FirstOffer, o.Customer, o.Pain),
			DependsOn:   []string{"validate"},
		},
		{
			Key: "outreach", Title: "Build a prospect list and outreach", Capability: project.CapabilityText,
			Description: fmt.Sprintf("Build a list of 25 concrete prospects for %s (%s), with a first personal outreach message and a follow-up. Do not send anything external without approval.", o.Customer, o.Niche),
			DependsOn:   []string{"validate"},
		},
		{
			Key: "decide", Title: "Decide go / adjust / stop", Capability: project.CapabilityDocument,
			Description: "Synthesize the validation evidence, the offer, and the outreach results into a one-page decision: go, adjust, or stop. Include the next three actions and the evidence behind each conclusion.",
			DependsOn:   []string{"validate", "offer", "outreach"},
		},
	}
	if len(b.Sources) > 0 {
		specs[0].AllowedTools = []string{"fetch_url"}
	}
	return out.String(), specs
}

// appendTool adds name to list once, preserving order.
func appendTool(list []string, name string) []string {
	for _, item := range list {
		if item == name {
			return list
		}
	}
	return append(list, name)
}

func (u *UI) approveProjectPlan(ctx context.Context, p project.Project, b projectBrief) error {
	if b.Status == "approved" {
		return nil
	}
	if u.deps.Discovery == nil {
		return fmt.Errorf("discovery is not configured")
	}
	sess, ok, err := u.deps.Discovery.Get(ctx, p.ID)
	if err != nil {
		return err
	}
	if !ok || sess.SelectedID == "" {
		return fmt.Errorf("choose an option before approving the plan")
	}
	var selected *discovery.Option
	for i := range sess.Options {
		if sess.Options[i].ID == sess.SelectedID {
			selected = &sess.Options[i]
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("the selected option is missing from the portfolio")
	}
	var specs []project.TaskSpec
	goalText := fmt.Sprintf("%s: %s", selected.Title, selected.OneLiner)
	if u.deps.Planning != nil {
		ps, has, perr := u.deps.Planning.Get(ctx, p.ID)
		if perr != nil {
			return fmt.Errorf("reading the selected plan: %w", perr)
		}
		if has && ps.Status == "ready" && ps.OptionID == sess.SelectedID &&
			ps.SelectedIndex >= 0 && ps.SelectedIndex < len(ps.Candidates) {
			cand := ps.Candidates[ps.SelectedIndex]
			specs = planning.ToTaskSpecs(cand)
			goalText = fmt.Sprintf("%s: %s", cand.Title, cand.Summary)
		}
	}
	if len(specs) == 0 {
		_, specs = buildProjectPlan(p, b, *selected)
	}
	if len(specs) > 0 && len(b.Sources) > 0 {
		specs[0].Description += " Allowed sources (fetch only these): " + strings.Join(b.Sources, " ")
		specs[0].AllowedTools = appendTool(specs[0].AllowedTools, "fetch_url")
	}
	criteria := []string{}
	if strings.TrimSpace(b.Success) != "" {
		for _, line := range strings.Split(b.Success, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				criteria = append(criteria, line)
			}
		}
	}
	if len(goalText) > 500 {
		goalText = goalText[:500]
	}
	if len(goalText) < 3 {
		goalText = p.Goal
	}
	goalID, tasks, err := u.deps.Projects.CreateDAG(ctx, p.ID, goalText, criteria, specs)
	if err != nil {
		return fmt.Errorf("creating the plan: %w", err)
	}
	b.Status = "approved"
	if err := u.saveBrief(ctx, b); err != nil {
		return err
	}
	if u.deps.Scheduler != nil {
		if _, err := u.deps.Scheduler.Start(ctx, goalID); err != nil {
			return fmt.Errorf("starting the plan: %w", err)
		}
		return nil
	}
	// Fallback: start the first task only. The rest remain pending for the
	// operator or the scheduler.
	if len(tasks) == 0 || u.deps.Engine == nil {
		return nil
	}
	j, err := u.deps.Jobs.Create(ctx, tasks[0].ID, p.ID)
	if err != nil {
		return err
	}
	u.deps.Engine.Enqueue(j.ID)
	return nil
}
