package ui

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/tcs76321/athanor/internal/architect"
	"github.com/tcs76321/athanor/internal/cycle"
	"github.com/tcs76321/athanor/internal/workflow"
)

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
		"Project": p, "Templates": workflow.Templates(),
	})
}

// handleCreateCycle creates a draft cycle from a built-in template. The project
// supplies the sources, capabilities, and goal; the template supplies the
// graph. Compilation happens when the user starts the cycle.
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
	c, err := u.deps.Cycles.Create(ctx, p.ID, cycle.Kind(tpl.CycleKind), p.Goal)
	if err != nil {
		u.fail(w, err)
		return
	}
	graphJSON, err := json.Marshal(tpl.Graph)
	if err != nil {
		u.fail(w, err)
		return
	}
	if err := u.deps.Cycles.SetGraph(ctx, c.ID, string(graphJSON)); err != nil {
		u.fail(w, err)
		return
	}
	if err := u.deps.Cycles.SetGraphRef(ctx, c.ID, tpl.ID, tpl.Version, ""); err != nil {
		u.fail(w, err)
		return
	}
	frame := architect.Frame{
		Name: p.Name, Goal: p.Goal, Outcome: p.Goal,
		Repository: p.RepositoryPath, Sources: append([]string(nil), p.Sources...),
		Capabilities: append([]string(nil), p.Capabilities...),
		FirstCycle:   tpl.CycleKind,
		ReviewPolicy: "internal work may proceed; external or irreversible actions require approval",
	}
	frameJSON, err := json.Marshal(frame)
	if err != nil {
		u.fail(w, err)
		return
	}
	if err := u.deps.Cycles.SetFrame(ctx, c.ID, string(frameJSON), frame.ReviewPolicy); err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui/projects/"+p.ID+"/canvas?cycle="+c.ID, http.StatusSeeOther)
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
