package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/discovery"
	"github.com/tcs76321/athanor/internal/hitl"
	"github.com/tcs76321/athanor/internal/intake"
	"github.com/tcs76321/athanor/internal/planning"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
)

// sourceStatus is the allowlist state of one human-supplied research URL.
type sourceStatus struct {
	URL     string
	Host    string
	Allowed bool
}

// sourceStatusesForConfig is the concrete helper the handlers use.
func (u *UI) sourceStatusesForConfig(sources []string) []sourceStatus {
	var allow []string
	if u.deps.Config != nil {
		allow = u.deps.Config.Network.AllowList
	}
	return sourceStatusesWithAllow(sources, allow)
}

func sourceStatusesWithAllow(sources, allow []string) []sourceStatus {
	out := make([]sourceStatus, 0, len(sources))
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

func (u *UI) handleProjectOnboarding(w http.ResponseWriter, r *http.Request) {
	if u.deps.Intake == nil {
		u.fail(w, fmt.Errorf("guided intake is not configured"))
		return
	}
	st, err := u.deps.Intake.State(r.Context(), r.PathValue("id"))
	if err != nil {
		u.fail(w, err)
		return
	}
	selected := selectedOption(st)
	planPreview := ""
	if selected != nil {
		previewProject := st.Project
		allowed := make([]string, 0, len(previewProject.Sources))
		for _, s := range u.sourceStatusesForConfig(previewProject.Sources) {
			if s.Allowed {
				allowed = append(allowed, s.URL)
			}
		}
		previewProject.Sources = allowed
		planPreview, _ = intake.BuildDeterministicPlan(previewProject, *selected)
	}
	var selectedPlan *planning.Candidate
	if st.HasPlan {
		if st.Plan.SelectedIndex >= 0 && st.Plan.SelectedIndex < len(st.Plan.Candidates) {
			selectedPlan = &st.Plan.Candidates[st.Plan.SelectedIndex]
		} else if len(st.Plan.Candidates) == 1 && st.Plan.Candidates[0].Status == "ready" {
			selectedPlan = &st.Plan.Candidates[0]
		}
	}
	u.render(w, "Onboarding "+st.Project.Name, projectOnboardingBody, map[string]any{
		"Project": st.Project, "State": st, "Stage": string(st.Stage),
		"Selected": selected, "PlanPreview": planPreview,
		"Sources":      u.sourceStatusesForConfig(st.Project.Sources),
		"SelectedPlan": selectedPlan,
		"Requested":    r.URL.Query().Get("requested") != "",
	})
}

func selectedOption(st intake.State) *discovery.Option {
	if !st.HasDirections || st.Directions.SelectedID == "" {
		return nil
	}
	for i := range st.Directions.Options {
		if st.Directions.Options[i].ID == st.Directions.SelectedID {
			return &st.Directions.Options[i]
		}
	}
	return nil
}

func (u *UI) handleProjectOnboardingPost(w http.ResponseWriter, r *http.Request) {
	if u.deps.Intake == nil {
		u.fail(w, fmt.Errorf("guided intake is not configured"))
		return
	}
	if err := r.ParseForm(); err != nil {
		u.fail(w, fmt.Errorf("invalid form: %w", err))
		return
	}
	ctx := r.Context()
	projectID := r.PathValue("id")
	action := r.FormValue("action")
	switch action {
	case "save_intent", "discover", "save_and_plan":
		onRamp := strings.TrimSpace(r.FormValue("onramp"))
		p, err := u.deps.Intake.SaveIntent(ctx, projectID, project.Intent{
			Name:           strings.TrimSpace(r.FormValue("name")),
			Goal:           strings.TrimSpace(r.FormValue("goal")),
			RepositoryPath: strings.TrimSpace(r.FormValue("repository_path")),
			Boundaries:     strings.TrimSpace(r.FormValue("boundaries")),
			Sources:        splitCriteria(r.FormValue("sources")),
			OnRamp:         onRamp,
		})
		if err != nil {
			u.fail(w, err)
			return
		}
		switch action {
		case "discover":
			if err := u.deps.Intake.StartDirections(ctx, projectID); err != nil {
				u.fail(w, err)
				return
			}
		case "save_and_plan":
			mode := ""
			if p.OnRamp == project.OnRampDirect {
				mode = "single"
			}
			if err := u.deps.Intake.StartPlans(ctx, projectID, mode); err != nil {
				u.fail(w, err)
				return
			}
		}
	case "set_onramp":
		if err := u.deps.Intake.SetOnRamp(ctx, projectID, strings.TrimSpace(r.FormValue("onramp"))); err != nil {
			u.fail(w, err)
			return
		}
	case "select":
		if err := u.deps.Intake.SelectDirection(ctx, projectID, r.FormValue("option")); err != nil {
			u.fail(w, err)
			return
		}
	case "generate_plans":
		if err := u.deps.Intake.StartPlans(ctx, projectID, strings.TrimSpace(r.FormValue("mode"))); err != nil {
			u.fail(w, err)
			return
		}
	case "select_plan":
		if err := u.deps.Intake.SelectPlan(ctx, projectID, r.FormValue("candidate")); err != nil {
			u.fail(w, err)
			return
		}
	case "approve":
		if _, err := u.deps.Intake.Approve(ctx, projectID, r.FormValue("candidate")); err != nil {
			if !errors.Is(err, intake.ErrAlreadyApproved) {
				u.fail(w, err)
				return
			}
		}
	case "cancel_discovery", "cancel_planning":
		u.deps.Intake.Cancel(projectID)
	case "request_access":
		source := strings.TrimSpace(r.FormValue("source"))
		host := ""
		if parsed, perr := url.Parse(source); perr == nil {
			host = strings.ToLower(parsed.Hostname())
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
			"host": host, "url": source, "project": projectID,
			"instruction": "Add this host to network.allow_list in config.yaml, then restart or reload the Core. The UI does not rewrite network policy.",
		})
		expires := time.Now().Add(24 * time.Hour)
		if _, err := u.deps.HITLRepo.Create(ctx, hitl.Request{
			ProjectID: projectID, Type: hitl.TypeNetworkAccess,
			Severity: hitl.SeverityHigh, PayloadJSON: string(payload),
			ExpiresAt: &expires,
		}); err != nil {
			u.fail(w, err)
			return
		}
		u.auditProject(projectID, "network_access_requested", map[string]any{"host": host, "url": source})
		http.Redirect(w, r, "/ui/projects/"+projectID+"/onboarding?requested=1", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/ui/projects/"+projectID+"/onboarding", http.StatusSeeOther)
}

// handleProjectIntent updates the project's intent from the Settings tab.
func (u *UI) handleProjectIntent(w http.ResponseWriter, r *http.Request) {
	if u.deps.Intake == nil {
		u.fail(w, fmt.Errorf("guided intake is not configured"))
		return
	}
	if err := r.ParseForm(); err != nil {
		u.fail(w, fmt.Errorf("invalid form: %w", err))
		return
	}
	id := r.PathValue("id")
	_, err := u.deps.Intake.SaveIntent(r.Context(), id, project.Intent{
		Name:           strings.TrimSpace(r.FormValue("name")),
		Goal:           strings.TrimSpace(r.FormValue("goal")),
		RepositoryPath: strings.TrimSpace(r.FormValue("repository_path")),
		Boundaries:     strings.TrimSpace(r.FormValue("boundaries")),
		Sources:        splitCriteria(r.FormValue("sources")),
	})
	if err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui/projects/"+id+"#settings", http.StatusSeeOther)
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
