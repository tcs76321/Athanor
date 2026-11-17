package ui

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/firstuse"
	"github.com/tcs76321/athanor/internal/trigger"
	"github.com/tcs76321/athanor/internal/workflow"
)

// templateCard is one built-in template plus its default schedule, so the form
// can prefill the cron field.
type templateCard struct {
	workflow.Template
	Schedule string
}

func templateCards() []templateCard {
	var cards []templateCard
	for _, tpl := range workflow.Templates() {
		card := templateCard{Template: tpl}
		for _, tr := range tpl.Triggers {
			if tr.Kind == workflow.TriggerSchedule {
				card.Schedule = tr.Spec
				break
			}
		}
		cards = append(cards, card)
	}
	return cards
}

// handleNewCycle lists the built-in templates. A cycle created here is a draft:
// the canvas opens it for editing and nothing runs until the user starts it.
func (u *UI) handleNewCycle(w http.ResponseWriter, r *http.Request) {
	if !u.requireCycles(w) {
		return
	}
	p, err := u.deps.Projects.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		u.fail(w, err)
		return
	}
	u.render(w, "New cycle — "+p.Name, newCycleBody, map[string]any{
		"Project": p, "Templates": templateCards(), "LocalTimezone": time.Local.String(),
	})
}

// handleCreateCycle creates a draft cycle from a built-in template and records
// its schedule. The project supplies the sources, capabilities, and goal; the
// template supplies the graph. Compilation happens when the cycle is started.
func (u *UI) handleCreateCycle(w http.ResponseWriter, r *http.Request) {
	if !u.requireCycles(w) {
		return
	}
	if err := r.ParseForm(); err != nil {
		u.fail(w, err)
		return
	}
	ctx := r.Context()
	p, err := u.deps.Projects.Get(ctx, r.PathValue("id"))
	if err != nil {
		u.fail(w, err)
		return
	}
	tpl, ok := workflow.TemplateByID(strings.TrimSpace(r.FormValue("template_id")))
	if !ok {
		http.Error(w, "unknown template", http.StatusNotFound)
		return
	}
	c, err := firstuse.CreateTemplateDraft(ctx, u.deps.Cycles, p, tpl, "")
	if err != nil {
		u.fail(w, err)
		return
	}
	schedule := strings.TrimSpace(r.FormValue("schedule"))
	timezone := strings.TrimSpace(r.FormValue("timezone"))
	if timezone == "" {
		timezone = "UTC"
	}
	if u.deps.Triggers != nil {
		for _, tr := range tpl.Triggers {
			spec := tr.Spec
			if tr.Kind == workflow.TriggerSchedule && schedule != "" {
				spec = schedule
			}
			if _, err := u.deps.Triggers.EnsureIn(ctx, p.ID, tpl.ID, trigger.Kind(tr.Kind), spec, tr.Enabled, timezone); err != nil {
				slog.Warn("ui: recording cycle trigger", "project", p.ID, "template", tpl.ID, "err", err)
			}
		}
	}
	http.Redirect(w, r, "/ui/projects/"+p.ID+"/canvas?cycle="+c.ID, http.StatusSeeOther)
}

// handleTriggerToggle pauses or re-enables a project's scheduled cycle. A
// paused trigger keeps its row and spec; re-enabling computes the next slot.
func (u *UI) handleTriggerToggle(w http.ResponseWriter, r *http.Request) {
	if u.deps.Triggers == nil {
		http.Error(w, "scheduler is not configured", http.StatusServiceUnavailable)
		return
	}
	projectID := r.PathValue("id")
	tr, err := u.deps.Triggers.Get(r.Context(), r.PathValue("triggerID"))
	if err != nil || tr.ProjectID != projectID {
		http.Error(w, "trigger not found", http.StatusNotFound)
		return
	}
	if err := u.deps.Triggers.SetEnabled(r.Context(), tr.ID, !tr.Enabled); err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui/projects/"+projectID+"#scheduled", http.StatusSeeOther)
}

const newCycleBody = `
<h1>New cycle — {{.Project.Name}}</h1>
<p class="sub">A cycle is one bounded run. Start from a template, then edit its graph in the canvas. Nothing runs until you start it.</p>

<div class="grid">
  {{range .Templates}}
  <div class="card">
    <div class="eyebrow">{{.Name}} · v{{.Version}} · {{.CycleKind}}</div>
    <p class="muted">{{len .Graph.Blocks}} blocks · {{len .Graph.Edges}} connections</p>
    <ul>
      {{range .Graph.Blocks}}<li><strong>{{if .Title}}{{.Title}}{{else}}{{.Kind}}{{end}}</strong> <span class="mono muted">{{.Kind}}</span></li>{{end}}
    </ul>
    <form method="post" action="/ui/projects/{{$.Project.ID}}/cycles">
      <input type="hidden" name="template_id" value="{{.ID}}">
      {{if .Schedule}}<label class="field"><span>Schedule (5-field cron, local time)</span>
        <input name="schedule" value="{{.Schedule}}" maxlength="80"></label>
      <label class="field"><span>Timezone</span>
        <input name="timezone" value="{{$.LocalTimezone}}" maxlength="64" placeholder="UTC"></label>
      <p class="muted small">Enabled when you create the draft; the scheduler starts a new cycle at the next matching minute in this timezone.</p>
      {{end}}
      <button class="btn primary">Create draft</button>
    </form>
  </div>
  {{end}}
</div>

<div class="actions">
  <a class="btn secondary" href="/ui/projects/{{.Project.ID}}">Back to project</a>
  <a class="btn secondary" href="/ui/projects/{{.Project.ID}}/canvas">Canvas</a>
</div>
`
