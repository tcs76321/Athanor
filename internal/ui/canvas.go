package ui

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/tcs76321/athanor/internal/cycle"
	"github.com/tcs76321/athanor/internal/firstuse"
	"github.com/tcs76321/athanor/internal/workflow"
)

// cycleGraphView is the canvas's data contract. The canvas is a view over the
// cycle's draft graph; it never executes a block itself.
type cycleGraphView struct {
	Cycle    cycleView            `json:"cycle"`
	Project  projectView          `json:"project"`
	Graph    workflow.Graph       `json:"graph"`
	Registry []workflow.BlockSpec `json:"registry"`
	Editable bool                 `json:"editable"`
}

type cycleView struct {
	ID              string `json:"id"`
	ProjectID       string `json:"project_id"`
	Ordinal         int    `json:"ordinal"`
	Kind            string `json:"kind"`
	Goal            string `json:"goal"`
	State           string `json:"state"`
	TemplateID      string `json:"template_id,omitempty"`
	TemplateVersion int    `json:"template_version,omitempty"`
	FrameJSON       string `json:"frame_json,omitempty"`
}

type projectView struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Goal       string   `json:"goal"`
	Repository string   `json:"repository,omitempty"`
	Capability []string `json:"capabilities,omitempty"`
}

func (u *UI) requireCycles(w http.ResponseWriter) bool {
	if u.deps.Cycles == nil {
		http.Error(w, "cycle service is not configured", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func (u *UI) handleProjectCanvas(w http.ResponseWriter, r *http.Request) {
	if !u.requireCycles(w) {
		return
	}
	ctx := r.Context()
	p, err := u.deps.Projects.Get(ctx, r.PathValue("id"))
	if err != nil {
		u.fail(w, err)
		return
	}
	cycles, err := u.deps.Cycles.ListByProject(ctx, p.ID)
	if err != nil {
		u.fail(w, err)
		return
	}
	if len(cycles) == 0 {
		u.render(w, "Canvas — "+p.Name, canvasEmptyBody, map[string]any{
			"Bare": true, "Canvas": true, "Project": p,
		})
		return
	}
	selected := cycles[len(cycles)-1]
	if id := r.URL.Query().Get("cycle"); id != "" {
		for _, c := range cycles {
			if c.ID == id {
				selected = c
				break
			}
		}
	} else {
		for _, c := range cycles {
			if c.State == cycle.StateDraft || c.State == cycle.StateApproved {
				selected = c
			}
		}
	}
	u.render(w, "Canvas — "+p.Name, canvasBody, map[string]any{
		"Bare": true, "Canvas": true, "Project": p, "Cycle": selected,
		"Cycles": cycles, "Registry": workflow.Registry(),
		"CanEdit": selected.State == cycle.StateDraft || selected.State == cycle.StateApproved,
	})
}

func (u *UI) handleCycleGraph(w http.ResponseWriter, r *http.Request) {
	if !u.requireCycles(w) {
		return
	}
	ctx := r.Context()
	c, err := u.deps.Cycles.Get(ctx, r.PathValue("id"))
	if err != nil {
		u.writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "cycle not found"})
		return
	}
	p, err := u.deps.Projects.Get(ctx, c.ProjectID)
	if err != nil {
		u.writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "project not found"})
		return
	}
	var graph workflow.Graph
	if strings.TrimSpace(c.GraphJSON) != "" && c.GraphJSON != "{}" {
		if err := json.Unmarshal([]byte(c.GraphJSON), &graph); err != nil {
			u.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "cycle graph is invalid: " + err.Error()})
			return
		}
	}
	u.writeJSON(w, http.StatusOK, cycleGraphView{
		Cycle: cycleView{
			ID: c.ID, ProjectID: c.ProjectID, Ordinal: c.Ordinal,
			Kind: string(c.Kind), Goal: c.Goal, State: string(c.State),
			TemplateID: c.TemplateID, TemplateVersion: c.TemplateVersion,
			FrameJSON: c.FrameJSON,
		},
		Project: projectView{
			ID: p.ID, Name: p.Name, Goal: p.Goal,
			Repository: p.RepositoryPath, Capability: p.Capabilities,
		},
		Graph:    graph,
		Registry: workflow.Registry(),
		Editable: c.State == cycle.StateDraft || c.State == cycle.StateApproved,
	})
}

func (u *UI) handleCycleGraphSave(w http.ResponseWriter, r *http.Request) {
	if !u.requireCycles(w) {
		return
	}
	ctx := r.Context()
	c, err := u.deps.Cycles.Get(ctx, r.PathValue("id"))
	if err != nil {
		u.writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "cycle not found"})
		return
	}
	if c.State != cycle.StateDraft && c.State != cycle.StateApproved {
		u.writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "cycle is not editable in state " + string(c.State)})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		u.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "reading graph: " + err.Error()})
		return
	}
	var graph workflow.Graph
	if err := json.Unmarshal(body, &graph); err != nil {
		u.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid graph JSON: " + err.Error()})
		return
	}
	normalized, err := workflow.Normalize(graph)
	if err != nil {
		u.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	graph = normalized
	raw, err := json.Marshal(graph)
	if err != nil {
		u.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := u.deps.Cycles.SetGraph(ctx, c.ID, string(raw)); err != nil {
		u.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	u.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "graph": graph})
}

func (u *UI) handleCycleStart(w http.ResponseWriter, r *http.Request) {
	if !u.requireCycles(w) {
		return
	}
	if u.deps.Starter == nil {
		u.writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "scheduler is not configured"})
		return
	}
	ctx := r.Context()
	c, err := u.deps.Cycles.Get(ctx, r.PathValue("id"))
	if err != nil {
		u.writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "cycle not found"})
		return
	}
	deps := firstuse.Deps{Projects: u.deps.Projects, Events: u.deps.Store, Freezer: u.deps.Freezer}
	res, err := firstuse.StartDraft(ctx, deps, u.deps.Cycles, u.deps.Starter, c.ID)
	if err != nil {
		u.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	u.writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "cycle": res.Cycle.ID, "state": res.Cycle.State,
		"goal": res.GoalID, "jobs": res.JobIDs,
	})
}

func (u *UI) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

const canvasEmptyBody = `
<div class="canvas-shell">
  <header class="canvas-head">
    <a class="btn secondary small" href="/ui/projects/{{.Project.ID}}">← Project</a>
    <div><h1>{{.Project.Name}}</h1><p class="muted">No cycles yet.</p></div>
  </header>
  <div class="canvas-empty-state">
    <h2>Start with a conversation</h2>
    <p class="muted">A project's timeline begins with a reviewed frame and a cycle-0 graph. The conversation onboarding creates the first draft; this canvas is where you refine and start it.</p>
    <a class="btn primary" href="/ui/projects/{{.Project.ID}}/onboarding">Open onboarding</a>
    <a class="btn secondary" href="/ui/projects/{{.Project.ID}}/cycles/new">Start from a template</a>
  </div>
</div>
`

const canvasBody = `
<div class="canvas-shell" id="canvas-shell"
     data-project-id="{{.Project.ID}}" data-cycle-id="{{.Cycle.ID}}"
     data-can-edit="{{.CanEdit}}">
  <header class="canvas-head">
    <a class="btn secondary small" href="/ui/projects/{{.Project.ID}}">← Project</a>
    <div class="canvas-title">
      <h1>{{.Project.Name}}</h1>
      <p class="muted"><span class="pill">{{.Cycle.Kind}}</span> <span class="pill">{{.Cycle.State}}</span> <span class="mono">{{.Cycle.Goal}}</span></p>
    </div>
    <div class="canvas-actions">
      <select id="canvas-cycle-select" aria-label="Cycle">
        {{range .Cycles}}<option value="{{.ID}}" {{if eq .ID $.Cycle.ID}}selected{{end}}>{{.Ordinal}} · {{.Kind}} · {{.State}}</option>{{end}}
      </select>
      <button type="button" class="btn secondary small" id="canvas-undo" disabled>Undo</button>
      <button type="button" class="btn secondary small" id="canvas-save" {{if not .CanEdit}}disabled{{end}}>Save draft</button>
      <button type="button" class="btn primary small" id="canvas-start" {{if not .CanEdit}}disabled{{end}}>Start cycle</button>
      <span id="canvas-status" class="canvas-status" role="status" aria-live="polite"></span>
    </div>
  </header>
  <div class="canvas-main">
    <aside class="canvas-palette">
      <h2>Blocks</h2>
      <p class="muted small">Click to add. Drag nodes; scroll to zoom; drag the background to pan.</p>
      <div id="canvas-palette-list">
        {{range .Registry}}<button type="button" class="canvas-block-btn" data-kind="{{.Kind}}" {{if not $.CanEdit}}disabled{{end}}>
          <span class="canvas-block-title">{{.Title}}</span>
          <span class="canvas-block-kind mono">{{.Kind}}</span>
        </button>{{end}}
      </div>
    </aside>
    <div class="canvas-stage" id="canvas-stage">
      <svg id="canvas-svg" role="application" aria-label="Cycle graph canvas">
        <g id="canvas-world"></g>
      </svg>
      <div class="canvas-hint" id="canvas-hint">Select a node to edit it. Use the palette to add a block.</div>
    </div>
    <aside class="canvas-inspector" id="canvas-inspector">
      <h2>Inspector</h2>
      <p class="muted" id="canvas-inspector-empty">Nothing selected.</p>
      <div id="canvas-inspector-body" hidden>
        <label class="field"><span>Title</span><input id="insp-title" type="text" maxlength="120"></label>
        <label class="field"><span>Instructions</span><textarea id="insp-instructions" rows="5"></textarea></label>
        <label class="field"><span>Search queries (one per line; research blocks)</span><textarea id="insp-queries" rows="3"></textarea></label>
        <label class="field"><span>Acceptance (one per line)</span><textarea id="insp-acceptance" rows="5"></textarea></label>
        <div class="canvas-insp-meta" id="insp-meta"></div>
        <div class="actions">
          <button type="button" class="btn secondary small" id="insp-apply">Apply</button>
          <button type="button" class="btn secondary small" id="insp-connect">Connect…</button>
          <button type="button" class="btn danger small" id="insp-delete">Delete block</button>
        </div>
        <div id="insp-connect-box" hidden>
          <label class="field"><span>Connect this block to</span>
            <select id="insp-connect-target"></select>
          </label>
          <button type="button" class="btn secondary small" id="insp-connect-do">Add edge</button>
        </div>
      </div>
    </aside>
  </div>
</div>
`
