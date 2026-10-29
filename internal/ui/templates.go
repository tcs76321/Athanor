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
const pre=document.getElementById('tokens');
let stick=true;
pre.addEventListener('scroll',()=>{stick=pre.scrollTop+pre.clientHeight>=pre.scrollHeight-24});
function evLine(p){
  let d={};try{d=JSON.parse(p.data)}catch(_){}
  const label=d.event||p.category||'event';
  const keys=['phase','state','to','winner','passed','score','reason','decision','unit','iter'];
  const parts=[];
  for(const k of keys){if(d[k]!==undefined&&d[k]!==''&&parts.length<4)parts.push(k+'='+d[k])}
  const ts=p.ts?String(p.ts).slice(11,19)+' ':'';
  return ts+label+(parts.length?' · '+parts.join(' · '):'');
}
const streams=new EventSource('/ui/jobs/'+jobID+'/stream');
streams.addEventListener('token',e=>{
  let t; try{t=JSON.parse(e.data)}catch(_){return}
  pre.textContent+=t;
  if(stick)pre.scrollTop=pre.scrollHeight;
});
streams.addEventListener('event',e=>{
  let p; try{p=JSON.parse(e.data)}catch(_){return}
  const div=document.createElement('div');div.className='ev';
  div.textContent=evLine(p);
  document.getElementById('events').prepend(div);
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
<div class="actions"><a class="btn" href="/ui/projects/new">+ New project</a></div>
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
<p class="sub">Each project is one goal, its criteria, and the work history behind it.</p>
<div class="actions"><a class="btn" href="/ui/projects/new">+ New project</a></div>
{{if .Projects}}
<table><thead><tr><th>Name</th><th>Archetype</th><th>Status</th><th>Goal</th><th>Created</th></tr></thead><tbody>
{{range .Projects}}<tr>
  <td><a href="/ui/projects/{{.ID}}">{{.Name}}</a></td>
  <td><span class="pill">{{.Archetype}}</span></td>
  <td><span class="pill">{{.Status}}</span></td>
  <td class="muted">{{.Goal}}</td>
  <td class="muted">{{.CreatedAt | ts}}</td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No projects yet. <a href="/ui/projects/new">Create the first one.</a></div>{{end}}
`

const newProjectBody = `
<h1>New project</h1>
<p class="sub">A project holds one goal, its criteria, and the jobs that work on it.</p>
{{if .Error}}<div class="card"><p class="del">{{.Error}}</p></div>{{end}}
<div class="card">
<form method="post" action="/ui/projects">
  <label>Name <input name="name" value="{{.Name}}" required></label>
  <div class="row">
    <label>Archetype <select name="archetype">
      <option value="code" {{if eq .Archetype "code"}}selected{{end}}>code</option>
      <option value="text" {{if eq .Archetype "text"}}selected{{end}}>text</option>
      <option value="document" {{if eq .Archetype "document"}}selected{{end}}>document</option>
      <option value="data" {{if eq .Archetype "data"}}selected{{end}}>data</option>
      <option value="media" {{if eq .Archetype "media"}}selected{{end}}>media</option>
    </select></label>
    <label>Test command <input name="test_command" value="{{.TestCommand}}" placeholder="pytest -q"></label>
  </div>
  <label>Goal <input name="goal" value="{{.Goal}}" minlength="20" maxlength="500" required></label>
  <label>Repository path <input name="repository_path" value="{{.RepositoryPath}}" placeholder="/absolute/path/to/repo"></label>
  <label>Acceptance criteria (one per line) <textarea name="criteria" rows="4">{{.Criteria}}</textarea></label>
  <div class="actions">
    <button name="start" value="">Create and review plan</button>
    <button name="start" value="1" class="secondary">Create and start now</button>
  </div>
</form>
</div>
<p class="muted">A repository-backed code project is indexed once before the first job. The interview is optional and skippable; approving a plan starts work.</p>
`

const interviewBody = `
<h1>Plan: {{.Project.Name}}</h1>
<p class="sub">{{.Project.Goal}}</p>
<p>Status:
  <span class="pill {{if interviewDone .Interview.Status}}ok{{else}}warn{{end}}">{{.Interview.Status}}</span>
</p>

{{if not (interviewDone .Interview.Status)}}
<div class="card">
  <h2>Clarify (optional, resumable)</h2>
  <form method="post" action="/ui/projects/{{.Project.ID}}/interview">
  {{range .Questions}}
    <label>{{.Prompt}}
      <textarea name="{{.ID}}" rows="2">{{index $.Answers .ID}}</textarea>
    </label>
    {{if .Help}}<div class="muted">{{.Help}}</div>{{end}}
  {{end}}
  <button>Save answers and build plan</button>
  </form>
</div>
{{end}}

<h2>Plan</h2>
{{if .Plan}}<div class="card"><pre>{{.Plan}}</pre></div>
{{else}}<div class="empty">Answer the questions and save, or skip the interview to use the goal and criteria as the plan.</div>{{end}}

<div class="card">
  <h2>Decision</h2>
  {{if not (interviewDone .Interview.Status)}}
    <p class="muted">Approving starts the first job. Skipping uses the goal and criteria as the plan. Both are reversible through the project page.</p>
    <div class="actions">
    <form method="post" action="/ui/projects/{{.Project.ID}}/interview/approve"><button>Approve plan and start work</button></form>
    <form method="post" action="/ui/projects/{{.Project.ID}}/interview/skip"><button class="secondary">Skip interview and start work</button></form>
    </div>
  {{else}}
    <p>This plan is <b>{{.Interview.Status}}</b>. Work has started.</p>
    <div class="actions"><a class="btn" href="/ui/projects/{{.Project.ID}}">Back to project</a></div>
  {{end}}
</div>
`

const projectBody = `
<h1>{{.Project.Name}} <span class="pill">{{.Project.Archetype}}</span></h1>
<p class="sub">{{.Project.Goal}}</p>
<div class="actions">
  <a class="btn" href="/ui/projects/{{.Project.ID}}/interview">Plan / interview</a>
  <a class="btn secondary" href="/ui/projects">All projects</a>
</div>

<div class="card">
  <div class="grid">
    <div><div class="l muted">Status</div><div><span class="pill">{{.Project.Status}}</span></div></div>
    <div><div class="l muted">Repository</div><div class="mono">{{if .Project.RepositoryPath}}{{.Project.RepositoryPath}}{{else}}—{{end}}</div></div>
    <div><div class="l muted">Test command</div><div class="mono">{{if .Project.TestCommand}}{{.Project.TestCommand}}{{else}}—{{end}}</div></div>
    <div><div class="l muted">Created</div><div>{{.Project.CreatedAt | ts}}</div></div>
  </div>
</div>

<h2>Work</h2>
{{range .Work}}
<div class="card">
  <h3>{{if .Task.Title}}{{.Task.Title}}{{else}}{{.Task.Description}}{{end}}
    <span class="pill">{{.Task.Status}}</span></h3>
  {{if .Task.Criteria}}<p class="muted">Criteria: {{join .Task.Criteria}}</p>{{end}}
  {{if .Jobs}}
  <table><thead><tr><th>Job</th><th>State</th><th>Attempt</th><th>Started</th></tr></thead><tbody>
  {{range .Jobs}}<tr>
    <td><a href="/ui/jobs/{{.ID}}" class="mono">{{.ID | short}}</a></td>
    <td><span class="pill {{stateClass .State}}">{{.State}}</span></td>
    <td>{{.Attempt}}</td>
    <td class="muted">{{if .StartedAt}}{{.StartedAt | ts}}{{end}}</td>
  </tr>{{end}}
  </tbody></table>
  {{else}}<p class="muted">No job yet. Approve the plan or submit a goal.</p>{{end}}
</div>
{{else}}<div class="empty">No tasks yet.</div>{{end}}

<h2>Submit a goal</h2>
<div class="card">
<form method="post" action="/ui/projects/{{.Project.ID}}/goals">
  <label>goal <textarea name="goal" rows="3" minlength="20" maxlength="500" required></textarea></label>
  <label>acceptance criteria (one per line) <textarea name="criteria" rows="3"></textarea></label>
  <button>Submit goal</button>
</form>
</div>

<h2>Artifacts</h2>
{{if .Artifacts}}
<table><thead><tr><th>Kind</th><th>Version</th><th>Status</th><th>Commit</th><th></th></tr></thead><tbody>
{{range .Artifacts}}<tr>
  <td><a href="/ui/artifacts/{{.ID}}">{{.Kind}}</a></td>
  <td><a href="/ui/artifacts/{{.ID}}">v{{.Version}}</a></td>
  <td><span class="pill {{stateClass .Status}}">{{.Status}}</span></td>
  <td class="mono muted">{{.GitCommit | short}}</td>
  <td>{{if .SupersedesID}}<a href="/ui/artifacts/{{.ID}}/diff">diff</a>{{end}}</td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No artifacts yet.</div>{{end}}

<h2>Corrections</h2>
{{if .Corrections}}
<table><thead><tr><th>Category</th><th>Severity</th><th>Scope</th><th>Status</th><th>Rule</th></tr></thead><tbody>
{{range .Corrections}}<tr><td>{{.Category}}</td><td>{{.Severity}}</td><td>{{.Scope}}</td><td>{{.Status}}</td><td class="muted">{{.DerivedRule}}</td></tr>
{{end}}
</tbody></table>
{{else}}<div class="empty">No corrections for this project.</div>{{end}}

<h2>Reject / correct an artifact (§18.4)</h2>
<div class="card">
<form method="post" action="/ui/projects/{{.Project.ID}}/corrections">
  <label>artifact id <input name="artifact_id" placeholder="artifact uuid"></label>
  <div class="row">
    <label>category <select name="category" required>
      <option value="architecture">architecture</option><option value="style">style</option>
      <option value="testing">testing</option><option value="security">security</option>
      <option value="performance">performance</option><option value="tooling">tooling</option>
      <option value="documentation">documentation</option><option value="other">other</option></select></label>
    <label>severity <select name="severity" required>
      <option value="low">low</option><option value="medium" selected>medium</option>
      <option value="high">high</option><option value="critical">critical</option></select></label>
    <label>scope <select name="scope" required>
      <option value="project" selected>project</option><option value="global">global</option></select></label>
  </div>
  <label>reason <input name="reason" required></label>
  <label>desired behavior <input name="desired_behavior" required></label>
  <input type="hidden" name="source" value="user_rejection">
  <button>Record rejection</button>
</form>
</div>
`

const correctionsBody = `
<h1>Corrections</h1>
<p class="sub">Prescriptive lessons captured from rejections and failures. Active rows are ranked into future prompts.</p>
{{if .Corrections}}
<div class="card">
<table><thead><tr><th>Category</th><th>Severity</th><th>Scope</th><th>Status</th><th>Applied</th><th>Rule</th><th></th></tr></thead><tbody>
{{range .Corrections}}<tr>
<td>{{.Category}}</td><td><span class="pill {{if eq .Severity "critical"}}bad{{else if eq .Severity "high"}}warn{{end}}">{{.Severity}}</span></td>
<td>{{.Scope}}</td><td>{{.Status}}</td><td>{{.AppliedCount}}</td>
<td class="muted">{{.DerivedRule}}</td>
<td>
<form method="post" action="/ui/corrections/{{.ID}}">
<button name="status" value="muted" class="secondary">Mute</button>
<button name="status" value="active">Promote</button>
<input name="severity" placeholder="severity" style="width:90px">
<input name="derived_rule" placeholder="rule" style="width:160px">
<button name="edit" value="1" class="secondary">Edit</button>
</form>
</td></tr>
{{end}}
</tbody></table>
</div>
{{else}}<div class="empty">No active corrections. Reject an artifact from its project page to record one.</div>{{end}}
`

const watchBody = `
<h1>Job <span class="mono">{{.Job.ID | short}}</span>
  <span class="pill {{stateClass .Job.State}}">{{.Job.State}}</span></h1>
<p class="sub">{{.ProjectName}} · task <span class="mono">{{.Job.TaskID | short}}</span>
  {{if .Job.PausedFrom}}· paused from {{.Job.PausedFrom}}{{end}}</p>
<div id="watch" data-job="{{.Job.ID}}"></div>

<div class="actions">
<form method="post" action="/ui/jobs/{{.Job.ID}}/control">
  {{if eq .Job.State "paused"}}<button name="action" value="resume">Resume</button>
  {{else if not .Job.State.Terminal}}<button name="action" value="pause" class="secondary">Pause</button>{{end}}
  {{if not .Job.State.Terminal}}<button name="action" value="cancel" class="danger">Cancel</button>{{end}}
  {{if .Job.State.Terminal}}<button name="action" value="retry">Retry</button>{{end}}
</form>
</div>

<div class="split">
  <div>
    <h2>Live output</h2>
    <pre id="tokens"></pre>
  </div>
  <div>
    <h2>Events</h2>
    <div id="events">
    {{range .Events}}<div class="ev"><span class="muted">{{.TS | ts}}</span> {{prettyEvent .DataJSON}}</div>
    {{else}}<div class="empty">No events yet.</div>{{end}}
    </div>
  </div>
</div>

<div class="card">
  <h2>Interruptions (§20.4)</h2>
  <form method="post" action="/ui/jobs/{{.Job.ID}}/note">
    <label>note <input name="note" placeholder="injected at the next safe point"></label>
    <button>Queue note</button>
  </form>
  {{range .Notes}}<div class="muted">{{.Status}}: {{.Text}}</div>{{end}}
</div>

<h2>Artifacts</h2>
{{if .Artifacts}}
<table><thead><tr><th>Kind</th><th>Version</th><th>Status</th><th></th></tr></thead><tbody>
{{range .Artifacts}}<tr><td><a href="/ui/artifacts/{{.ID}}">{{.Kind}}</a></td><td><a href="/ui/artifacts/{{.ID}}">v{{.Version}}</a></td>
<td><span class="pill {{stateClass .Status}}">{{.Status}}</span></td>
<td>{{if .SupersedesID}}<a href="/ui/artifacts/{{.ID}}/diff">diff</a>{{end}}</td></tr>
{{end}}
</tbody></table>
{{else}}<div class="empty">No artifacts yet.</div>{{end}}
<script>` + watchJS + `</script>
`

const artifactBody = `
<h1>Artifact <span class="mono">{{.Artifact.ID | short}}</span></h1>
<p class="sub">
  <span class="pill">{{.Artifact.Kind}}</span> v{{.Artifact.Version}}
  <span class="pill {{stateClass .Artifact.Status}}">{{.Artifact.Status}}</span>
  {{if .Project}}· <a href="/ui/projects/{{.Project.ID}}">{{.Project.Name}}</a>{{end}}
  · job <a href="/ui/jobs/{{.Artifact.JobID}}" class="mono">{{.Artifact.JobID | short}}</a>
  {{if .Artifact.GitCommit}}· commit <span class="mono">{{.Artifact.GitCommit | short}}</span>{{end}}
</p>

<div class="split">
  <div class="card">
    <h2>What was built</h2>
    <pre>{{.Content}}</pre>
  </div>
  <div>
    <div class="card">
      <h2>How to run</h2>
      {{if .Project}}
        {{if .Project.TestCommand}}<p>Test: <code>{{.Project.TestCommand}}</code></p>
        {{else}}<p class="muted">No test command is configured for this project.</p>{{end}}
        {{if .Project.RepositoryPath}}<p>Repository: <code>{{.Project.RepositoryPath}}</code></p>{{end}}
      {{else}}<p class="muted">Project metadata is unavailable.</p>{{end}}
    </div>

    <div class="card">
      <h2>What it cost</h2>
      {{if .HasOutcome}}
        <p><b>{{.Outcome.TokenCost}}</b> tokens · {{.Outcome.WallTime}} · {{.Outcome.Result}}</p>
        {{if .Outcome.ThinkArm}}<p class="muted">reasoning arm: {{.Outcome.ThinkArm}} · {{.Outcome.ReasoningTokens}} estimated reasoning tokens</p>{{end}}
      {{else}}<p class="muted">No captured outcome for this job.</p>{{end}}
    </div>

    <div class="card">
      <h2>Known issues</h2>
      {{range .Issues}}<div class="ev"><b>{{.Category}}</b> <span class="pill warn">{{.Severity}}</span><br>{{.UserFeedback}}</div>
      {{else}}<p class="muted">None recorded against this artifact.</p>{{end}}
    </div>

    <div class="card">
      <h2>Human review</h2>
      <form method="post" action="/ui/artifacts/{{.Artifact.ID}}/review">
        <label>score (0–1)
          <input type="number" name="score" min="0" max="1" step="0.05" value="1" required>
        </label>
        <label><input type="checkbox" name="accepted" value="1" checked> accepted</label>
        <label>note
          <input name="note" placeholder="what worked, what is missing">
        </label>
        <button>Record review</button>
      </form>
      <h3>Previous reviews</h3>
      {{if .Reviews}}
      <table><thead><tr><th>When</th><th>By</th><th>Score</th><th></th></tr></thead><tbody>
      {{range .Reviews}}<tr><td class="muted">{{.CreatedAt}}</td><td>{{.Reviewer}}</td><td>{{printf "%.2f" .Score}}</td>
      <td>{{if .Accepted}}<span class="pill ok">accepted</span>{{else}}<span class="pill bad">rejected</span>{{end}}</td></tr>
      {{if .Note}}<tr><td colspan="4" class="muted">{{.Note}}</td></tr>{{end}}{{end}}
      </tbody></table>
      {{else}}<p class="muted">No reviews yet.</p>{{end}}
    </div>
  </div>
</div>
`

const diffBody = `
<h1>Diff: {{.Artifact.Kind}} v{{.Artifact.Version}}</h1>
<p class="sub">Compared with the version it superseded. <span class="mono">{{.Artifact.ID | short}}</span></p>
<div class="card">
<pre>{{range .Lines}}<span{{if eq .Kind "-"}} class="del"{{else if eq .Kind "+"}} class="add"{{end}}>{{.Kind}} {{.Text}}</span>
{{end}}</pre>
</div>
<div class="actions"><a class="btn secondary" href="/ui/artifacts/{{.Artifact.ID}}">Back to artifact</a></div>
`

const statisticsBody = `
<h1>Statistics</h1>
<p class="sub">Captured outcomes and the strategy insights mined from them.</p>
<div class="grid">
  <div class="card stat"><div class="n">{{.Jobs}}</div><div class="l">captured outcomes</div></div>
  <div class="card stat"><div class="n">{{.Accepted}}</div><div class="l">accepted</div></div>
  <div class="card stat"><div class="n">{{.Tokens}}</div><div class="l">tokens spent</div></div>
</div>
<h2>Strategy insights (§13.4)</h2>
{{if .Insights}}
<table><thead><tr><th>Polarity</th><th>Status</th><th>Feature</th><th>Cohort</th><th>Accept / baseline</th><th>Statement</th></tr></thead><tbody>
{{range .Insights}}<tr>
<td><span class="pill {{if eq .Polarity "winning"}}ok{{else}}bad{{end}}">{{.Polarity}}</span></td>
<td>{{.Status}}</td><td>{{.Pattern.Feature}}={{.Pattern.Value}}</td>
<td>{{.Evidence.CohortJobs}}</td><td>{{printf "%.2f" .Evidence.AcceptRate}} / {{printf "%.2f" .Evidence.BaselineAcceptRate}}</td>
<td>{{.Statement}}</td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No strategy insights yet. They appear after enough outcomes accumulate.</div>{{end}}
<p class="muted">Promote/mute from the CLI: <code>athanor strategy promote|mute -id &lt;id&gt;</code>.</p>
`
