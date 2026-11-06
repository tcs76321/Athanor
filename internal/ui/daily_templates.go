package ui

const jobsBody = `
<h1>Runs</h1>
<p class="sub">Every job the Core has started, newest first. Filter by state or project, then open the watch view or the full trace.</p>
<div class="card">
<form method="get" action="/ui/jobs">
  <div class="row">
    <label>state
      <select name="state"><option value="">all</option>
      {{$state := .State}}{{range .States}}<option value="{{.}}" {{if eq . $state}}selected{{end}}>{{.}}</option>{{end}}
      </select>
    </label>
    <label>project
      <select name="project"><option value="">all</option>
      {{$project := .Project}}{{range .Projects}}<option value="{{.ID}}" {{if eq .ID $project}}selected{{end}}>{{.Name}}</option>{{end}}
      </select>
    </label>
  </div>
  <div class="actions"><button>Filter</button><a class="btn secondary" href="/ui/jobs">Clear</a><a class="btn secondary" href="/ui/digest">Digest</a></div>
</form>
</div>
{{if .Jobs}}
<div class="card flush"><div class="table-wrap">
<table><thead><tr><th>Run</th><th>Project</th><th>Task</th><th>State</th><th>Attempt</th><th>Started</th><th>Finished</th><th></th></tr></thead><tbody>
{{range .Jobs}}<tr>
  <td><a href="/ui/jobs/{{.ID}}" class="mono">{{.ID | short}}</a></td>
  <td>{{if .ProjectID}}<a href="/ui/projects/{{.ProjectID}}">{{if .ProjectName}}{{.ProjectName}}{{else}}{{.ProjectID | short}}{{end}}</a>{{end}}</td>
  <td class="muted">{{if .TaskTitle}}{{.TaskTitle}}{{else}}{{.TaskID | short}}{{end}}</td>
  <td><span class="pill {{stateClass .State}}">{{.State}}</span></td>
  <td>{{.Attempt}}</td>
  <td class="muted">{{.Started | when}}</td>
  <td class="muted">{{.Finished | when}}</td>
  <td><a href="/ui/jobs/{{.ID}}/trace">trace</a></td>
</tr>{{end}}
</tbody></table>
</div></div>
<p class="muted">Showing the newest 200 runs. Narrow the filter to find older work.</p>
{{else}}<div class="empty">No runs match this filter.</div>{{end}}
`

const digestBody = `
<h1>Morning digest</h1>
<p class="sub">The last {{.Hours}} hours, {{.Digest.Since | when}} → {{.Digest.Until | when}}. This is a reading surface, not an approval: decisions still happen in the Inbox.</p>
<form method="get" action="/ui/digest" class="actions">
  <label>window
    <select name="hours">
      <option value="12" {{if eq .Hours 12}}selected{{end}}>12 hours</option>
      <option value="24" {{if eq .Hours 24}}selected{{end}}>24 hours</option>
      <option value="72" {{if eq .Hours 72}}selected{{end}}>3 days</option>
      <option value="168" {{if eq .Hours 168}}selected{{end}}>7 days</option>
    </select>
  </label>
  <button>Refresh</button>
</form>

<div class="grid cols-4 tight">
  <div class="card stat"><div class="n">{{.Digest.JobsCompleted}}</div><div class="l">jobs completed</div></div>
  <div class="card stat"><div class="n">{{.Digest.JobsFailed}}</div><div class="l">jobs failed</div></div>
  <div class="card stat"><div class="n">{{.Digest.Artifacts.Accepted}}</div><div class="l">artifacts accepted</div></div>
  <div class="card stat"><div class="n">{{.Digest.Tokens}}</div><div class="l">tokens spent</div></div>
  <div class="card stat"><div class="n">{{.Digest.TasksDone}}</div><div class="l">tasks done</div></div>
  <div class="card stat"><div class="n">{{.Digest.GoalsCompleted}}</div><div class="l">goals completed</div></div>
  <div class="card stat"><div class="n">{{.Digest.PendingHITL}}</div><div class="l">waiting for you</div></div>
  <div class="card stat"><div class="n">{{.Digest.ActiveAlarms}}</div><div class="l">active alarms</div></div>
</div>

{{if .Digest.Failures}}
<h2>Failures worth a look</h2>
<div class="card">
{{range .Digest.Failures}}<div class="ev"><a href="/ui/jobs/{{.JobID}}" class="mono">{{.JobID | short}}</a> <span class="muted">· {{.ProjectID | short}}</span><br>{{.Reason}}</div>{{end}}
</div>
{{end}}

{{if .Pending}}
<h2>Waiting for you</h2>
<div class="card">{{range .Pending}}<div class="ev">{{.}}</div>{{end}}
<div class="actions"><a class="btn" href="/ui/notifications">Open the Inbox</a></div></div>
{{end}}

{{if .Digest.Daydream.Actions}}
<h2>Daydream</h2>
<div class="card">
<p><b>{{.Digest.Daydream.Actions}}</b> idle actions · {{.Digest.Daydream.ArtifactsProduced}} draft artifacts · {{.Digest.Daydream.CorrectionsProposed}} corrections · {{.Digest.Daydream.InsightsProposed}} insights · {{.Digest.Daydream.ChunksProcessed}} chunks · {{.Digest.Daydream.TokensSaved}} tokens saved.</p>
<div class="actions"><a class="btn secondary" href="/ui/daydream">Open daydream</a></div>
</div>
{{end}}

<h2>Recently accepted</h2>
{{if .Accepted}}
<div class="card flush"><div class="table-wrap">
<table><thead><tr><th>Artifact</th><th>Project</th><th>Version</th><th>When</th></tr></thead><tbody>
{{range .Accepted}}<tr>
  <td><a href="/ui/artifacts/{{.ID}}">{{.Kind}}</a></td>
  <td class="muted">{{.Project}}</td><td>v{{.Version}}</td>
  <td class="muted">{{.Created | when}}</td>
</tr>{{end}}
</tbody></table>
</div></div>
{{else}}<div class="empty">No accepted artifacts yet.</div>{{end}}
`
