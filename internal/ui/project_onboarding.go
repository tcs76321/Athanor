package ui

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/discovery"
	"github.com/tcs76321/athanor/internal/project"
)

// Discovery is the option-portfolio surface the project onboarding drives.
// Satisfied by *discovery.Service; nil disables the discovery step.
type Discovery interface {
	Generate(ctx context.Context, projectID string, req discovery.Request) (discovery.Session, error)
	Get(ctx context.Context, projectID string) (discovery.Session, bool, error)
	Select(ctx context.Context, projectID, optionID string) error
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
	Status    string
	Error     string
	StartedAt time.Time
	Session   discovery.Session
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
	run := u.discoveryRunFor(p.ID)
	stage := "brief"
	switch {
	case brief.Status == "approved":
		stage = "approved"
	case hasSession && sess.SelectedID != "" && brief.Status == "planned":
		stage = "plan"
	case hasSession && len(sess.Options) > 0:
		stage = "choose"
	case run != nil && run.Status == "running":
		stage = "generating"
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
	plan := ""
	if selected != nil {
		plan, _ = buildProjectPlan(p, brief, *selected)
	}
	u.render(w, "Onboarding "+p.Name, projectOnboardingBody, map[string]any{
		"Project": p, "Brief": brief, "Session": sess, "HasSession": hasSession,
		"Run": run, "Stage": stage, "Selected": selected, "Plan": plan,
		"Status": brief.Status,
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
		return &cp
	}
	return nil
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
	run := &discoveryRun{Status: "running", StartedAt: time.Now()}
	u.discRuns[p.ID] = run
	u.discMu.Unlock()

	req := discovery.Request{
		Goal: brief.Goal, Brief: brief.Limitations, Constraints: brief.Constraints,
		Sources: brief.Sources, Count: 10,
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		sess, err := u.deps.Discovery.Generate(ctx, p.ID, req)
		u.discMu.Lock()
		defer u.discMu.Unlock()
		if err != nil {
			run.Status = "failed"
			run.Error = err.Error()
			return
		}
		run.Status = "done"
		run.Session = sess
	}()
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
	specs := []project.TaskSpec{
		{
			Key: "validate", Title: "Validate demand", Capability: project.CapabilityText,
			Description: fmt.Sprintf("Run the validation experiment for %q: %s. Record what you learned, including who you spoke to, what they said, and whether they would pay.", o.Title, o.ValidationExperiment),
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
	return out.String(), specs
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
	_, specs := buildProjectPlan(p, b, *selected)
	criteria := []string{}
	if strings.TrimSpace(b.Success) != "" {
		for _, line := range strings.Split(b.Success, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				criteria = append(criteria, line)
			}
		}
	}
	goalText := fmt.Sprintf("%s: %s", selected.Title, selected.OneLiner)
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
