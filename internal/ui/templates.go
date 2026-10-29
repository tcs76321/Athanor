package ui

// Server-rendered page bodies. Each is parsed together with document() at
// request time (small pages; the cost is negligible and avoids an embed
// step). html/template auto-escapes every interpolated value.

const css = `
:root{
  --bg:#0c0f14;--panel:#131822;--panel2:#1a2130;--line:#242d3b;
  --text:#dde3ec;--muted:#8b96a8;--accent:#7aa2f7;--accent2:#9d7cf7;
  --ok:#7ee2a8;--warn:#f2c46d;--bad:#ff8f8f;--radius:10px;
}
*{box-sizing:border-box}
html{color-scheme:dark}
body{margin:0;background:var(--bg);color:var(--text);
  font:15px/1.55 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif}
a{color:var(--accent);text-decoration:none}
a:hover{text-decoration:underline}
:focus-visible{outline:2px solid var(--accent);outline-offset:2px;border-radius:4px}
.skip{position:absolute;left:-9999px;top:0;background:var(--panel2);padding:8px 12px;z-index:10}
.skip:focus{left:12px;top:8px}
header{position:sticky;top:0;z-index:5;display:flex;gap:18px;align-items:center;
  padding:10px 20px;background:rgba(19,24,34,.92);backdrop-filter:blur(8px);
  border-bottom:1px solid var(--line)}
.brand{font-weight:700;letter-spacing:.03em;color:var(--text)}
.brand span{color:var(--accent)}
nav{display:flex;gap:2px;flex-wrap:wrap}
nav a{padding:6px 11px;border-radius:999px;color:var(--muted);font-size:14px}
nav a:hover{background:var(--panel2);color:var(--text);text-decoration:none}
nav a[aria-current="page"]{background:var(--panel2);color:var(--text)}
main{max-width:1120px;margin:0 auto;padding:26px 20px 72px}
h1{font-size:26px;line-height:1.2;margin:0 0 6px;letter-spacing:-.01em}
h2{font-size:16px;margin:26px 0 10px;color:#b9c3d3;letter-spacing:.01em}
h3{font-size:14px;margin:16px 0 6px;color:#b9c3d3}
p{margin:6px 0}
.sub{color:var(--muted);margin:0 0 18px}
.card{background:var(--panel);border:1px solid var(--line);border-radius:var(--radius);
  padding:16px 18px;margin:0 0 18px}
.card h2{margin-top:0}
.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(190px,1fr));gap:14px;margin-bottom:8px}
.stat{display:block;color:var(--text)}
.stat .n{font-size:28px;font-weight:650;letter-spacing:-.02em;line-height:1.1}
.stat .l{color:var(--muted);font-size:13px;text-transform:uppercase;letter-spacing:.06em}
.stat:hover{text-decoration:none;border-color:#33405a}
.pill{display:inline-block;padding:2px 9px;border-radius:999px;font-size:12px;
  border:1px solid var(--line);background:var(--panel2);color:var(--muted);white-space:nowrap}
.pill.ok{color:var(--ok);border-color:#24523a;background:#122019}
.pill.warn{color:var(--warn);border-color:#5a4a24;background:#1f1a10}
.pill.bad{color:var(--bad);border-color:#5a2b2b;background:#201313}
.pill.run{color:var(--accent);border-color:#2b3f66;background:#121a28}
table{width:100%;border-collapse:collapse;font-size:14px}
th,td{text-align:left;padding:8px 10px;border-bottom:1px solid var(--line);vertical-align:top}
th{color:var(--muted);font-weight:600;font-size:12px;text-transform:uppercase;letter-spacing:.05em}
tbody tr:hover{background:rgba(255,255,255,.015)}
.muted{color:var(--muted)}
.mono,pre,code{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
pre{background:#0a0d12;border:1px solid var(--line);border-radius:8px;padding:12px;
  overflow:auto;font-size:13px;line-height:1.45;max-height:60vh}
code{background:#0a0d12;border:1px solid var(--line);border-radius:4px;padding:1px 5px;font-size:13px}
.del{color:var(--bad)}.add{color:var(--ok)}
form{margin:0}
label{display:block;margin:12px 0 2px;font-size:13px;color:#b9c3d3}
input,select,textarea{width:100%;background:#0a0d12;color:var(--text);
  border:1px solid #2c3748;border-radius:7px;padding:8px 10px;font:inherit;font-size:14px}
input[type=checkbox]{width:auto;margin-right:6px}
textarea{resize:vertical}
button,.btn{display:inline-block;background:#22304a;border:1px solid #34456a;color:#dbe7ff;
  border-radius:7px;padding:7px 13px;font:inherit;font-size:14px;cursor:pointer;margin:8px 6px 0 0}
button:hover,.btn:hover{background:#2a3a58;text-decoration:none}
button.secondary,.btn.secondary{background:var(--panel2);border-color:var(--line);color:var(--text)}
button.danger{background:#3a1d22;border-color:#5a2b2b;color:#ffd7d7}
.row{display:flex;gap:10px;flex-wrap:wrap;align-items:flex-end}
.row>*{flex:1 1 180px}
.actions{display:flex;gap:8px;flex-wrap:wrap;align-items:center;margin-top:10px}
.empty{padding:18px;border:1px dashed var(--line);border-radius:8px;color:var(--muted);text-align:center}
.split{display:grid;grid-template-columns:1.6fr 1fr;gap:18px}
@media(max-width:820px){.split{grid-template-columns:1fr}header{padding:10px 14px;gap:10px}main{padding:20px 14px 56px}}
.ev{border-left:2px solid var(--line);padding:4px 0 4px 10px;margin:6px 0;font-size:13px}
.ev b{color:#c7d3e6;font-weight:600}
#events{max-height:52vh;overflow:auto}
`

const dashboardJS = `
const path=location.pathname;
document.querySelectorAll('header nav a').forEach(a=>{
  const href=a.getAttribute('href');
  const active=href==='/ui'?path==='/ui':path===href||path.startsWith(href+'/');
  if(active)a.setAttribute('aria-current','page');
});
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

// document wraps a server-rendered body in the shared page shell. The page
// title is NOT folded into the template source: it is interpolated as
// {{.Title}} data so html/template escapes it. Folding a dynamic title (e.g.
// a project name the LLM generated) into the source would (a) inject it
// unescaped into <title>, and (b) let a name containing "{{" panic
// template.Must or evaluate as an action — both closed by keeping it in data.
func document(body string) string {
	return `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} — Athanor</title>
<style>` + css + `</style></head>
<body>
<a class="skip" href="#main">Skip to content</a>
<header>
  <a class="brand" href="/ui">Athanor<span>.</span></a>
  <nav aria-label="Main">
    <a href="/ui">Dashboard</a>
    <a href="/ui/projects">Projects</a>
    <a href="/ui/reviews">Reviews</a>
    <a href="/ui/corrections">Corrections</a>
    <a href="/ui/statistics">Statistics</a>
  </nav>
</header>
<main id="main">` + body + `</main>
<script>` + dashboardJS + `</script>
</body></html>`
}

const reviewsBody = `
<h1>Review queue</h1>
<p class="sub">Accepted work with no human review. The queue is blinded: kind and project, no model or content.</p>
{{if .Queue}}
<div class="grid">
{{range .Queue}}
  <div class="card">
    <div><span class="pill">{{.Artifact.Kind}}</span> <span class="muted">v{{.Artifact.Version}}</span></div>
    <h3><a href="/ui/artifacts/{{.Artifact.ID}}">Review this {{.Artifact.Kind}}</a></h3>
    <div class="muted">{{.ProjectName}}</div>
    <div class="muted">{{.Artifact.CreatedAt | ts}}</div>
  </div>
{{end}}
</div>
{{else}}<div class="empty">Nothing to review. Accepted artifacts appear here until a human rates them.</div>{{end}}

<h2>Recent reviews</h2>
{{if .Recent}}
<table><thead><tr><th>When</th><th>Artifact</th><th>Score</th><th>Accepted</th><th>Note</th></tr></thead><tbody>
{{range .Recent}}<tr>
  <td class="muted">{{.CreatedAt}}</td>
  <td><a href="/ui/artifacts/{{.ArtifactID}}" class="mono">{{.ArtifactID | short}}</a></td>
  <td>{{printf "%.2f" .Score}}</td>
  <td>{{if .Accepted}}<span class="pill ok">yes</span>{{else}}<span class="pill bad">no</span>{{end}}</td>
  <td class="muted">{{.Note}}</td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No reviews recorded yet.</div>{{end}}
`

const errorBody = `
<h1>Something went wrong</h1>
<div class="card">
  <p class="del">{{.Error}}</p>
  <p class="muted">The daemon is still running. Nothing was changed by this request.</p>
  <div class="actions"><a class="btn" href="/ui">Back to dashboard</a></div>
</div>
`

const dashboardBody = `
<h1>Dashboard</h1>
<p class="sub">What needs you, and what the machine is doing.</p>
<div class="grid">
  <div class="card stat"><div class="n">{{len .Active}}</div><div class="l">active jobs</div></div>
  <a class="card stat" href="/ui/reviews"><div class="n">{{.ReviewCount}}</div><div class="l">artifacts to review</div></a>
  <div class="card stat"><div class="n">{{len .Pending}}</div><div class="l">pending approvals</div></div>
  <div class="card stat"><div class="n">{{if .Frozen}}Frozen{{else}}Active{{end}}</div><div class="l">kill switch</div></div>
</div>

{{if .Pending}}
<div class="card">
<h2>Needs your decision</h2>
{{range .Pending}}
<form method="post" action="/ui/approvals/{{.ID}}" class="ev">
  <div><b>{{.Type}}</b> <span class="pill {{if eq .Severity "critical"}}bad{{else}}warn{{end}}">{{.Severity}}</span>
    <span class="muted">job {{.JobID | short}}</span></div>
  <div class="muted mono">{{.PayloadJSON}}</div>
  <div class="actions">
    <button name="action" value="approve">Approve</button>
    <button name="action" value="reject" class="secondary">Reject</button>
    <button name="action" value="defer" class="secondary">Defer 1h</button>
    <input type="text" name="note" placeholder="note (optional)" style="flex:1 1 220px">
  </div>
</form>
{{end}}
</div>
{{end}}

{{if .ReviewCount}}
<div class="card">
  <b>{{.ReviewCount}} accepted artifact(s) waiting for a review.</b>
  <span class="muted">The queue is blinded: kind and project only.</span>
  <div class="actions"><a class="btn" href="/ui/reviews">Open review queue</a></div>
</div>
{{end}}

<h2>Active jobs</h2>
{{if .Active}}
<table><thead><tr><th>Job</th><th>Project</th><th>Phase</th><th></th></tr></thead><tbody>
{{range .Active}}<tr>
  <td><a href="/ui/jobs/{{.ID}}" class="mono">{{.ID | short}}</a></td>
  <td>{{index $.ProjectNames .ProjectID}}</td>
  <td><span class="pill {{stateClass .State}}" id="phase-{{.ID}}">{{.State}}</span></td>
  <td><a href="/ui/jobs/{{.ID}}">watch</a></td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No job is running. Create a project or submit a goal.</div>{{end}}

<h2>Recently finished</h2>
{{if .Terminal}}
<table><thead><tr><th>Job</th><th>Project</th><th>State</th><th>Finished</th></tr></thead><tbody>
{{range .Terminal}}<tr>
  <td><a href="/ui/jobs/{{.ID}}" class="mono">{{.ID | short}}</a></td>
  <td>{{index $.ProjectNames .ProjectID}}</td>
  <td><span class="pill {{stateClass .State}}">{{.State}}</span></td>
  <td class="muted">{{if .FinishedAt}}{{.FinishedAt | ts}}{{end}}</td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">Nothing finished yet.</div>{{end}}
`

const projectsBody = `
<h1>Projects</h1>
<p><a href="/ui/projects/new">+ New project</a></p>
<table><thead><tr><th>Name</th><th>Archetype</th><th>Goal</th></tr></thead><tbody>
{{range .Projects}}<tr><td><a href="/ui/projects/{{.ID}}">{{.Name}}</a></td><td>{{.Archetype}}</td><td class="muted">{{.Goal}}</td></tr>
{{else}}<tr><td colspan="3" class="muted">none</td></tr>{{end}}
</tbody></table>
`

const newProjectBody = `
<h1>New project</h1>
{{if .Error}}<p class="del">{{.Error}}</p>{{end}}
<form method="post" action="/ui/projects">
<div><label>name <input name="name" value="{{.Name}}" size="40" required></label></div>
<div><label>archetype <select name="archetype">
<option value="code" {{if eq .Archetype "code"}}selected{{end}}>code</option>
<option value="text" {{if eq .Archetype "text"}}selected{{end}}>text</option>
<option value="document" {{if eq .Archetype "document"}}selected{{end}}>document</option>
<option value="data" {{if eq .Archetype "data"}}selected{{end}}>data</option>
<option value="media" {{if eq .Archetype "media"}}selected{{end}}>media</option>
</select></label></div>
<div><label>goal <input name="goal" value="{{.Goal}}" size="80" minlength="20" maxlength="500" required></label></div>
<div><label>repository path <input name="repository_path" value="{{.RepositoryPath}}" size="80" placeholder="/absolute/path/to/repo"></label></div>
<div><label>test command <input name="test_command" value="{{.TestCommand}}" size="40" placeholder="pytest -q"></label></div>
<div><label>acceptance criteria (one per line)<br><textarea name="criteria" rows="4" cols="80">{{.Criteria}}</textarea></label></div>
<div><label><input type="checkbox" name="start" value="1" {{if .Start}}checked{{end}}> start the first job immediately</label></div>
<button>Create project</button>
</form>
<p class="muted">A repository-backed code project is indexed once before the first job. The full pass remains available through the CLI.</p>
`

const interviewBody = `
<h1>Plan: {{.Project.Name}}</h1>
<p class="muted">{{.Project.Goal}}</p>
<p>Status: <b>{{.Interview.Status}}</b></p>
<h2>Clarify (optional, resumable)</h2>
<form method="post" action="/ui/projects/{{.Project.ID}}/interview">
{{range .Questions}}
<div><label>{{.Prompt}}<br><textarea name="{{.ID}}" rows="2" cols="80">{{index $.Answers .ID}}</textarea></label>
{{if .Help}}<span class="muted">{{.Help}}</span>{{end}}</div>
{{end}}
<button>Save answers and build plan</button>
</form>
<h2>Plan</h2>
{{if .Plan}}<pre>{{.Plan}}</pre>{{else}}<p class="muted">Answer the questions and save, or skip the interview to use the goal and criteria as the plan.</p>{{end}}
<h2>Decision</h2>
<form method="post" action="/ui/projects/{{.Project.ID}}/interview/approve"><button>Approve plan and start work</button></form>
<form method="post" action="/ui/projects/{{.Project.ID}}/interview/skip"><button>Skip interview and start work</button></form>
<p><a href="/ui/projects/{{.Project.ID}}">back to project</a></p>
`

const projectBody = `
<h1>{{.Project.Name}} <span class="muted">({{.Project.Archetype}})</span></h1>
<p class="muted">{{.Project.Goal}}</p>
<p><a href="/ui/projects/{{.Project.ID}}/interview">Plan / interview</a></p>
<h2>New goal</h2>
<form method="post" action="/ui/projects/{{.Project.ID}}/goals">
<div><label>goal <textarea name="goal" rows="3" cols="80" minlength="20" maxlength="500" required></textarea></label></div>
<div><label>acceptance criteria (one per line)<br><textarea name="criteria" rows="3" cols="80"></textarea></label></div>
<button>Submit goal</button>
</form>
<h2>Artifacts</h2>
<table><thead><tr><th>Kind</th><th>Version</th><th>Status</th><th>Commit</th><th></th></tr></thead><tbody>
{{range .Artifacts}}<tr><td><a href="/ui/artifacts/{{.ID}}">{{.Kind}}</a></td><td><a href="/ui/artifacts/{{.ID}}">v{{.Version}}</a></td><td>{{.Status}}</td><td>{{.GitCommit | short}}</td><td>{{if .SupersedesID}}<a href="/ui/artifacts/{{.ID}}/diff">diff</a>{{end}}</td></tr>
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
{{range .Artifacts}}<tr><td><a href="/ui/artifacts/{{.ID}}">{{.Kind}}</a></td><td><a href="/ui/artifacts/{{.ID}}">v{{.Version}}</a></td><td>{{.Status}}</td><td>{{if .SupersedesID}}<a href="/ui/artifacts/{{.ID}}/diff">diff</a>{{end}}</td></tr>
{{else}}<tr><td colspan="4" class="muted">none</td></tr>{{end}}
</tbody></table>
<h2>Events</h2>
<div id="events"></div>
<script>` + watchJS + `</script>
`

const artifactBody = `
<h1>Artifact {{.Artifact.ID | short}} <span class="muted">{{.Artifact.Kind}} v{{.Artifact.Version}} / {{.Artifact.Status}}</span></h1>
<p class="muted">project {{.Artifact.ProjectID | short}} · job {{.Artifact.JobID | short}} · commit {{.Artifact.GitCommit | short}}</p>
<pre>{{.Content}}</pre>
<h2>Review</h2>
<form method="post" action="/ui/artifacts/{{.Artifact.ID}}/review">
<div><label>score (0–1) <input name="score" size="6" value="1.0" required></label>
<label><input type="checkbox" name="accepted" value="1" checked> accepted</label></div>
<div><label>note <input name="note" size="70" placeholder="what worked, what is missing"></label></div>
<button>Record review</button>
</form>
<h2>Previous reviews</h2>
<table><thead><tr><th>When</th><th>Reviewer</th><th>Score</th><th>Accepted</th><th>Note</th></tr></thead><tbody>
{{range .Reviews}}<tr><td>{{.CreatedAt}}</td><td>{{.Reviewer}}</td><td>{{printf "%.2f" .Score}}</td><td>{{.Accepted}}</td><td>{{.Note}}</td></tr>
{{else}}<tr><td colspan="5" class="muted">none</td></tr>{{end}}
</tbody></table>
`

const diffBody = `
<h1>Diff {{.Artifact.Kind}} v{{.Artifact.Version}} <span class="muted">{{.Artifact.ID | short}}</span></h1>
<pre>{{range .Lines}}<span{{if eq .Kind "-"}} class="del"{{else if eq .Kind "+"}} class="add"{{end}}>{{.Kind}} {{.Text}}</span>
{{end}}</pre>
`

const statisticsBody = `
<h1>Statistics</h1>
<p>{{.Jobs}} captured outcome(s) · {{.Accepted}} accepted · {{.Tokens}} total tokens</p>
<h2>Strategy insights (§13.4)</h2>
<table><thead><tr><th>Polarity</th><th>Status</th><th>Feature</th><th>Cohort</th><th>Accept / baseline</th><th>Statement</th></tr></thead><tbody>
{{range .Insights}}<tr>
<td>{{.Polarity}}</td><td>{{.Status}}</td><td>{{.Pattern.Feature}}={{.Pattern.Value}}</td>
<td>{{.Evidence.CohortJobs}}</td><td>{{printf "%.2f" .Evidence.AcceptRate}} / {{printf "%.2f" .Evidence.BaselineAcceptRate}}</td>
<td>{{.Statement}}</td>
</tr>
{{else}}<tr><td colspan="6" class="muted">none</td></tr>{{end}}
</tbody></table>
<p class="muted">Promote/mute from the CLI: athanor strategy promote|mute -id &lt;id&gt;.</p>
`
