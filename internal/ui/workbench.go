package ui

import (
	"net/http"
	"strings"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/coverage"
	"github.com/tcs76321/athanor/internal/netgrant"
	"github.com/tcs76321/athanor/internal/trigger"
)

// workbenchView is the operator's daily surface for a recurring project: the
// latest accepted work, the sources and grants that feed it, what the ledger
// already covered, and when the next run is.
type workbenchView struct {
	Accepted  []artifact.Artifact
	Grants    []netgrant.Grant
	Coverage  []coverageView
	Triggers  []trigger.Record
	Sources   []sourceStatus
	Tasks     []workbenchTask
	HasCycles bool
}

type workbenchTask struct {
	Title, Key, State, JobID string
}

type coverageView struct {
	Namespace string
	Items     []coverage.Item
}

func (u *UI) handleWorkbench(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := u.deps.Projects.Get(ctx, r.PathValue("id"))
	if err != nil {
		u.fail(w, err)
		return
	}
	view := workbenchView{Sources: u.sourceStatusesForConfig(p.Sources)}
	if u.deps.Artifacts != nil {
		if arts, err := u.deps.Artifacts.ListByProject(ctx, p.ID); err == nil {
			for _, a := range arts {
				if a.Status == artifact.StatusAccepted {
					view.Accepted = append(view.Accepted, a)
					if len(view.Accepted) >= 8 {
						break
					}
				}
			}
		}
	}
	grants := netgrant.NewRepo(u.deps.Store)
	if list, err := grants.ListByProject(ctx, p.ID); err == nil {
		view.Grants = list
	}
	ledger := coverage.NewRepo(u.deps.Store)
	if names, err := ledger.Namespaces(ctx, p.ID); err == nil {
		for _, ns := range names {
			if items, err := ledger.Recent(ctx, p.ID, ns, 12); err == nil && len(items) > 0 {
				view.Coverage = append(view.Coverage, coverageView{Namespace: ns, Items: items})
			}
		}
	}
	if u.deps.Triggers != nil {
		if list, err := u.deps.Triggers.ListByProject(ctx, p.ID); err == nil {
			view.Triggers = list
		}
	}
	if u.deps.Cycles != nil {
		if list, err := u.deps.Cycles.ListByProject(ctx, p.ID); err == nil {
			view.HasCycles = len(list) > 0
		}
	}
	if tasks, err := u.deps.Projects.TasksByProject(ctx, p.ID); err == nil {
		for _, t := range tasks {
			row := workbenchTask{Title: t.Title, Key: t.ID, State: string(t.Status)}
			if jobs, jerr := u.deps.Jobs.ByTask(ctx, t.ID); jerr == nil && len(jobs) > 0 {
				row.JobID = jobs[0].ID
				row.State = string(jobs[0].State)
			}
			view.Tasks = append(view.Tasks, row)
			if len(view.Tasks) >= 30 {
				break
			}
		}
	}
	u.render(w, "Workbench — "+p.Name, workbenchBody, map[string]any{
		"Project": p, "View": view,
	})
}

// handleRevokeGrant removes a project grant. The grant must belong to the
// project in the path; a cross-project id is a 404, not a cross-scope write.
func (u *UI) handleRevokeGrant(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	repo := netgrant.NewRepo(u.deps.Store)
	g, err := repo.Get(r.Context(), r.PathValue("grantID"))
	if err != nil || g.ProjectID != projectID {
		u.fail(w, errGrantNotFound{})
		return
	}
	if err := repo.Revoke(r.Context(), g.ID); err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui/projects/"+projectID+"/workbench", http.StatusSeeOther)
}

type errGrantNotFound struct{}

func (errGrantNotFound) Error() string { return "grant not found" }

// handleAllowlistAdd writes one host to network.allow_list. It is the global
// half of the two-level policy: the operator explicitly widens the ceiling,
// then restarts the Core (the running gateway keeps its old policy).
func (u *UI) handleAllowlistAdd(w http.ResponseWriter, r *http.Request) {
	if u.deps.ConfigPath == "" {
		u.fail(w, errConfigWritebackDisabled{})
		return
	}
	if err := r.ParseForm(); err != nil {
		u.fail(w, err)
		return
	}
	host := strings.TrimSpace(r.FormValue("host"))
	if host == "" {
		u.fail(w, errHostRequired{})
		return
	}
	if err := config.UpdateNetworkAllowlist(u.deps.ConfigPath, []string{host}); err != nil {
		u.fail(w, err)
		return
	}
	u.markRestartRequired(r.Context(), "network.allow_list changed; restart the Core to apply it")
	target := "/ui/settings"
	if projectID := strings.TrimSpace(r.FormValue("project")); projectID != "" {
		target = "/ui/projects/" + projectID + "/workbench"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

type errConfigWritebackDisabled struct{}

func (errConfigWritebackDisabled) Error() string {
	return "config write-back is disabled; set config_path or edit config.yaml"
}

type errHostRequired struct{}

func (errHostRequired) Error() string { return "a host is required" }

const workbenchBody = `
<h1>Workbench — {{.Project.Name}}</h1>
<p class="sub">The daily surface: what was produced, what feeds it, what is already covered, and when it runs next.</p>

<div class="actions">
  <a class="btn secondary" href="/ui/projects/{{.Project.ID}}">Project</a>
  <a class="btn secondary" href="/ui/projects/{{.Project.ID}}/canvas">Canvas</a>
  {{if .View.HasCycles}}<a class="btn secondary" href="/ui/projects/{{.Project.ID}}/cycles/new">New cycle</a>{{end}}
</div>

<h2>Current cycle</h2>
{{if .View.Tasks}}
<table>
  <thead><tr><th>Block</th><th>State</th><th>Run</th></tr></thead>
  <tbody>
  {{range .View.Tasks}}
    <tr>
      <td>{{.Title}}</td>
      <td><span class="pill {{stateClass .State}}">{{.State}}</span></td>
      <td>{{if .JobID}}<a class="mono" href="/ui/jobs/{{.JobID}}">open</a>{{else}}<span class="muted">not run</span>{{end}}</td>
    </tr>
  {{end}}
  </tbody>
</table>
{{else}}<p class="muted">No tasks yet. Create a cycle from a template.</p>{{end}}

<h2>Latest accepted work</h2>
{{if .View.Accepted}}
<div class="grid">
  {{range .View.Accepted}}
  <div class="card">
    <div class="eyebrow">{{.Kind}} · v{{.Version}}</div>
    <p class="mono muted">{{.ID}}</p>
    <p>{{when .CreatedAt}}</p>
    <a class="btn secondary small" href="/ui/artifacts/{{.ID}}">Read</a>
    <a class="btn secondary small" href="/ui/artifacts/{{.ID}}/download">Download</a>
  </div>
  {{end}}
</div>
{{else}}<p class="muted">No accepted artifact yet.</p>{{end}}

<h2>Sources</h2>
{{if .View.Sources}}
<ul>
  {{range .View.Sources}}
  <li><span class="mono">{{.URL}}</span> — {{if .Allowed}}<span class="pill ok">allowed</span>{{else}}<span class="pill warn">not allowed</span>
    <form method="post" action="/ui/network/allowlist">
      <input type="hidden" name="host" value="{{.Host}}">
      <input type="hidden" name="project" value="{{$.Project.ID}}">
      <button class="secondary small">Add to global allowlist</button>
    </form>
  {{end}}</li>
  {{end}}
</ul>
{{else}}<p class="muted">No declared sources. A research block can still use the global profile.</p>{{end}}

<h2>Egress grants</h2>
{{if .View.Grants}}
<table>
  <thead><tr><th>Base</th><th>Reason</th><th>Expires</th><th>Status</th><th></th></tr></thead>
  <tbody>
  {{range .View.Grants}}
    <tr>
      <td class="mono">{{.Scheme}}://{{.Host}}{{.PathPrefix}}</td>
      <td>{{.Reason}}</td>
      <td class="muted">{{if .ExpiresAt}}{{when .ExpiresAt}}{{else}}never{{end}}</td>
      <td><span class="pill">{{.Status}}</span></td>
      <td>
        {{if eq .Status "active"}}
        <form method="post" action="/ui/projects/{{$.Project.ID}}/grants/{{.ID}}/revoke">
          <button class="secondary small">Revoke</button>
        </form>
        {{end}}
      </td>
    </tr>
  {{end}}
  </tbody>
</table>
{{else}}<p class="muted">No project grants. A denied fetch files an approval request.</p>{{end}}

<h2>Coverage</h2>
{{if .View.Coverage}}
{{range .View.Coverage}}
  <h3 class="mono">{{.Namespace}}</h3>
  <ul>
    {{range .Items}}<li><span class="mono">{{.Key}}</span> {{.Title}} <span class="muted">({{when .LastSeen}})</span></li>{{end}}
  </ul>
{{end}}
{{else}}<p class="muted">Nothing recorded yet. The retain block writes this ledger.</p>{{end}}

<h2>Schedule</h2>
{{if .View.Triggers}}
<table>
  <thead><tr><th>Template</th><th>Cron</th><th>Timezone</th><th>Next run (UTC)</th><th>State</th></tr></thead>
  <tbody>
  {{range .View.Triggers}}
    <tr>
      <td class="mono">{{.TemplateID}}</td>
      <td class="mono">{{if .Spec}}{{.Spec}}{{else}}manual{{end}}</td>
      <td class="mono">{{.Timezone}}</td>
      <td class="mono muted">{{if .NextRunAt}}{{.NextRunAt}}{{else}}—{{end}}</td>
      <td><span class="pill">{{if .Enabled}}enabled{{else}}paused{{end}}</span></td>
    </tr>
  {{end}}
  </tbody>
</table>
{{else}}<p class="muted">No scheduled cycle. Create one from a template.</p>{{end}}
`
