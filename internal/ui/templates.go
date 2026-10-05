package ui

// Server-rendered page bodies. Each is parsed together with document() at
// request time (small pages; the cost is negligible and avoids an embed
// step). html/template auto-escapes every interpolated value.

const css = `
body{font:14px/1.5 ui-monospace,Menlo,monospace;margin:0;background:#0f1115;color:#d7dae0}
header{padding:10px 16px;background:#171a21;border-bottom:1px solid #262b36;display:flex;gap:16px;align-items:center}
header a{color:#8ab4f8;text-decoration:none}
nav a{margin-right:12px}
main{padding:16px;max-width:1100px}
h1{font-size:18px}h2{font-size:15px;margin-top:22px;color:#a9b1bd}
table{border-collapse:collapse;width:100%}th,td{text-align:left;padding:3px 8px;border-bottom:1px solid #222731}
th{color:#8b93a1;font-weight:600}
form{margin:6px 0;padding:6px;border:1px solid #222731;border-radius:6px}
input,select,textarea{background:#0b0d11;color:#d7dae0;border:1px solid #2b3240;border-radius:4px;padding:3px}
button{background:#233;border:1px solid #345;color:#cfe;border-radius:4px;padding:3px 8px;cursor:pointer}
pre{background:#0b0d11;border:1px solid #222731;padding:8px;overflow:auto}
.del{color:#ff8b8b}.add{color:#8bffa8}
.muted{color:#7b8494}
#tokens{white-space:pre-wrap}
`

const dashboardJS = `
const es=new EventSource('/ui/events');
es.addEventListener('event',e=>{
  let p; try{p=JSON.parse(e.data)}catch(_){return}
  if(p.category!=='jobs'||!p.job_id)return;
  let d; try{d=JSON.parse(p.data)}catch(_){return}
  if(d.event==='transition'&&d.to){const el=document.getElementById('phase-'+p.job_id);if(el)el.textContent=d.to}
});
`

const watchJS = `
const root=document.getElementById('watch');const jobID=root.dataset.job;
const streams=new EventSource('/ui/jobs/'+jobID+'/stream');
streams.addEventListener('token',e=>{
  let t; try{t=JSON.parse(e.data)}catch(_){return}
  document.getElementById('tokens').textContent+=t;
});
streams.addEventListener('event',e=>{
  let p; try{p=JSON.parse(e.data)}catch(_){return}
  const li=document.createElement('div');
  li.textContent=p.ts+' '+p.category+' '+p.data;
  document.getElementById('events').prepend(li);
});
`

func document(title, body string) string {
	return `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>` + title + ` — Athanor</title>
<style>` + css + `</style></head>
<body>
<header><b>Athanor</b><nav><a href="/ui">Dashboard</a><a href="/ui/projects">Projects</a><a href="/ui/corrections">Corrections</a></nav></header>
<main>` + body + `</main>
<script>` + dashboardJS + `</script>
</body></html>`
}

const dashboardBody = `
<h1>Dashboard</h1>
<p>Power: {{if .Frozen}}<b>FROZEN (kill switch)</b>{{else}}active{{end}}</p>
<h2>Active jobs</h2>
<table><thead><tr><th>Job</th><th>Project</th><th>Phase</th><th></th></tr></thead><tbody>
{{range .Active}}<tr><td><a href="/ui/jobs/{{.ID}}">{{.ID | short}}</a></td><td>{{.ProjectID | short}}</td><td id="phase-{{.ID}}">{{.State}}</td><td><a href="/ui/jobs/{{.ID}}">watch</a></td></tr>
{{else}}<tr><td colspan="4" class="muted">none</td></tr>{{end}}
</tbody></table>
<h2>Pending approvals</h2>
{{range .Pending}}
<form method="post" action="/ui/approvals/{{.ID}}">
<div>{{.Type}} / {{.Severity}} — job {{.JobID | short}}</div>
<div class="muted">{{.PayloadJSON}}</div>
<button name="action" value="approve">Approve</button>
<button name="action" value="reject">Reject</button>
<button name="action" value="defer">Defer 1h</button>
<input type="text" name="note" placeholder="note">
</form>
{{else}}<p class="muted">none</p>{{end}}
<h2>Recently finished</h2>
<table><thead><tr><th>Job</th><th>State</th><th>Finished</th></tr></thead><tbody>
{{range .Terminal}}<tr><td><a href="/ui/jobs/{{.ID}}">{{.ID | short}}</a></td><td>{{.State}}</td><td>{{if .FinishedAt}}{{.FinishedAt | ts}}{{end}}</td></tr>
{{else}}<tr><td colspan="3" class="muted">none</td></tr>{{end}}
</tbody></table>
`

const projectsBody = `
<h1>Projects</h1>
<table><thead><tr><th>Name</th><th>Archetype</th><th>Goal</th></tr></thead><tbody>
{{range .Projects}}<tr><td><a href="/ui/projects/{{.ID}}">{{.Name}}</a></td><td>{{.Archetype}}</td><td class="muted">{{.Goal}}</td></tr>
{{else}}<tr><td colspan="3" class="muted">none</td></tr>{{end}}
</tbody></table>
`

const projectBody = `
<h1>{{.Project.Name}} <span class="muted">({{.Project.Archetype}})</span></h1>
<p class="muted">{{.Project.Goal}}</p>
<h2>Artifacts</h2>
<table><thead><tr><th>Kind</th><th>Version</th><th>Status</th><th>Commit</th><th></th></tr></thead><tbody>
{{range .Artifacts}}<tr><td>{{.Kind}}</td><td>v{{.Version}}</td><td>{{.Status}}</td><td>{{.GitCommit | short}}</td><td>{{if .SupersedesID}}<a href="/ui/artifacts/{{.ID}}/diff">diff</a>{{end}}</td></tr>
{{else}}<tr><td colspan="5" class="muted">none</td></tr>{{end}}
</tbody></table>
<h2>Corrections</h2>
<table><thead><tr><th>Category</th><th>Severity</th><th>Scope</th><th>Status</th><th>Rule</th></tr></thead><tbody>
{{range .Corrections}}<tr><td>{{.Category}}</td><td>{{.Severity}}</td><td>{{.Scope}}</td><td>{{.Status}}</td><td>{{.DerivedRule}}</td></tr>
{{else}}<tr><td colspan="5" class="muted">none</td></tr>{{end}}
</tbody></table>
<h2>Reject / correct an artifact (§18.4)</h2>
<form method="post" action="/ui/projects/{{.Project.ID}}/corrections">
<div><label>artifact id <input name="artifact_id" size="40"></label></div>
<div><label>category <select name="category" required>
<option value="architecture">architecture</option><option value="style">style</option>
<option value="testing">testing</option><option value="security">security</option>
<option value="performance">performance</option><option value="tooling">tooling</option>
<option value="documentation">documentation</option><option value="other">other</option></select></label>
<label>severity <select name="severity" required>
<option value="low">low</option><option value="medium" selected>medium</option>
<option value="high">high</option><option value="critical">critical</option></select></label>
<label>scope <select name="scope" required>
<option value="project" selected>project</option><option value="global">global</option></select></label></div>
<div><label>reason <input name="reason" size="60" required></label></div>
<div><label>desired behavior <input name="desired_behavior" size="60" required></label></div>
<input type="hidden" name="source" value="user_rejection">
<button>Record rejection</button>
</form>
`

const correctionsBody = `
<h1>Corrections</h1>
<table><thead><tr><th>Category</th><th>Severity</th><th>Scope</th><th>Status</th><th>Applied</th><th>Rule</th><th></th></tr></thead><tbody>
{{range .Corrections}}<tr>
<td>{{.Category}}</td><td>{{.Severity}}</td><td>{{.Scope}}</td><td>{{.Status}}</td><td>{{.AppliedCount}}</td>
<td>{{.DerivedRule}}</td>
<td>
<form method="post" action="/ui/corrections/{{.ID}}">
<button name="status" value="muted">Mute</button>
<button name="status" value="active">Promote</button>
<input name="severity" placeholder="severity" size="8">
<input name="derived_rule" placeholder="rule" size="24">
<button name="edit" value="1">Edit</button>
</form>
</td></tr>
{{else}}<tr><td colspan="7" class="muted">none</td></tr>{{end}}
</tbody></table>
`

const watchBody = `
<h1>Job {{.Job.ID | short}} <span class="muted">{{.Job.State}}</span></h1>
<div id="watch" data-job="{{.Job.ID}}"></div>
<p>Project {{.Job.ProjectID | short}} · Task {{.Job.TaskID | short}}{{if .Job.PausedFrom}} · paused from {{.Job.PausedFrom}}{{end}}</p>
<form method="post" action="/ui/jobs/{{.Job.ID}}/control">
<button name="action" value="pause">Pause</button>
<button name="action" value="resume">Resume</button>
<button name="action" value="cancel">Cancel</button>
<button name="action" value="retry">Retry</button>
</form>
<h2>Live output</h2>
<pre id="tokens"></pre>
<h2>Interruptions (§20.4)</h2>
<form method="post" action="/ui/jobs/{{.Job.ID}}/note">
<input name="note" size="60" placeholder="note injected at the next safe point">
<button>Queue note</button>
</form>
{{range .Notes}}<div class="muted">{{.Status}}: {{.Text}}</div>{{end}}
<h2>Artifacts</h2>
<table><thead><tr><th>Kind</th><th>Version</th><th>Status</th><th></th></tr></thead><tbody>
{{range .Artifacts}}<tr><td>{{.Kind}}</td><td>v{{.Version}}</td><td>{{.Status}}</td><td>{{if .SupersedesID}}<a href="/ui/artifacts/{{.ID}}/diff">diff</a>{{end}}</td></tr>
{{else}}<tr><td colspan="4" class="muted">none</td></tr>{{end}}
</tbody></table>
<h2>Events</h2>
<div id="events"></div>
<script>` + watchJS + `</script>
`

const diffBody = `
<h1>Diff {{.Artifact.Kind}} v{{.Artifact.Version}} <span class="muted">{{.Artifact.ID | short}}</span></h1>
<pre>{{range .Lines}}<span{{if eq .Kind "-"}} class="del"{{else if eq .Kind "+"}} class="add"{{end}}>{{.Kind}} {{.Text}}</span>
{{end}}</pre>
`
