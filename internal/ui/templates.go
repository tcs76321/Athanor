package ui

// Server-rendered page bodies. Each is parsed together with document() at
// request time (small pages; the cost is negligible and avoids an embed
// step). html/template auto-escapes every interpolated value.

// document wraps a server-rendered body in the shared page shell. The page
// title is NOT folded into the template source: it is interpolated as
// {{.Title}} data so html/template escapes it. Folding a dynamic title (e.g.
// a project name the LLM generated) into the source would (a) inject it
// unescaped into <title>, and (b) let a name containing "{{" panic
// template.Must or evaluate as an action — both closed by keeping it in data.
func document(body string) string {
	return `<!doctype html>
<html lang="en" data-theme="{{.Theme}}" data-base-theme="{{.Theme}}"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} — Athanor</title>
<link rel="stylesheet" href="/ui/assets/app.css">
<script src="/ui/assets/htmx.min.js" defer></script>
<script src="/ui/assets/app.js" defer></script>
</head>
<body>
{{if .Bare}}` + body + `{{else}}
<a class="skip" href="#main">Skip to content</a>
<div class="app">
  <aside class="rail">
    <a class="brand" href="/ui">
      <span class="brand-name">Athanor<span class="ember">.</span></span>
      <span class="brand-tag">the alchemical LLM furnace</span>
    </a>
    <nav aria-label="Work">
      <a href="/ui"><svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true"><path d="M3 11.5 12 4l9 7.5"/><path d="M5.5 10v9h13v-9"/></svg>Home</a>
      <a href="/ui/notifications"><svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true"><path d="M4 5h16v12H7l-3 3z"/></svg>Inbox <span class="badge" id="alert-count" hidden></span></a>
      <a href="/ui/projects"><svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true"><path d="M3 6h7l2 2h9v11H3z"/></svg>Projects</a>
      <a href="/ui/artifacts"><svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true"><path d="M5 3h10l4 4v14H5z"/><path d="M15 3v5h4"/></svg>Library</a>
      <a href="/ui/reviews">Reviews <span class="badge" id="review-count" hidden></span></a>
    </nav>
    <div class="group-label">Insights</div>
    <nav aria-label="Insights">
      <a href="/ui/statistics">Statistics</a>
      <a href="/ui/memory">Memory</a>
      <a href="/ui/events?category=network">Network</a>
      <a href="/ui/events?category=security">Security</a>
      <a href="/ui/daydream">Daydream</a>
    </nav>
    <div class="group-label">System</div>
    <nav aria-label="System">
      <a href="/ui/setup">Setup</a>
      <a href="/ui/runtime">Runtime</a>
      <a href="/ui/events">Events</a>
      <a href="/ui/corrections">Corrections</a>
      <a href="/ui/settings">Settings</a>
      <a href="/ui/sitemap">Sitemap</a>
    </nav>
    <div class="spacer"></div>
    <div class="rail-foot">
      {{if .Runtime}}<span><span class="dot"></span>{{.Runtime}}</span>{{end}}
      {{if .Version}}<span>{{.Version}}</span>{{end}}
    </div>
  </aside>
  <div class="work">
    <div class="topbar">
      <div class="crumbs"><a href="/ui">Athanor</a><span class="sep">/</span><span>{{.Title}}</span></div>
      <form class="search" method="get" action="/ui/search" role="search">
        <input type="search" name="q" placeholder="Search projects, artifacts, jobs…  (⌘K)" aria-label="Search" value="{{.Query}}">
      </form>
      <div class="top-actions">
        <button type="button" class="btn secondary small" data-palette-open>⌘K</button>
        <a class="btn primary small" href="/ui/projects/new">+ New project</a>
      </div>
    </div>
    {{if .DevRuntime}}<div class="banner">Development runtime: this is the unsupported host process. The supported runtime is the Core Pod — run <code>sh scripts/install.sh</code> for the real setup, or continue here for development.</div>{{end}}
    <main id="main">` + body + `</main>
  </div>
</div>
<div class="palette" id="palette" role="dialog" aria-modal="true" aria-label="Command palette" aria-hidden="true">
  <div class="palette-box">
    <input id="palette-input" type="search" placeholder="Search or jump to…" autocomplete="off" aria-label="Search or jump to">
    <div class="palette-results" id="palette-results"></div>
  </div>
</div>
{{end}}
</body></html>`
}

// themeNames is the closed set of themes the Settings page may select. The
// CSS defines every one; an unknown value falls back to monokai.
var themeNames = []struct{ ID, Label string }{
	{"monokai", "Monokai (default)"},
	{"ember", "Ember Paper (warm light)"},
	{"monokai-light", "Monokai Light"},
	{"nord", "Nord"},
	{"solarized-light", "Solarized Light"},
	{"contrast", "High Contrast"},
}

func themeLabel(id string) string {
	for _, t := range themeNames {
		if t.ID == id {
			return t.Label
		}
	}
	return themeNames[0].Label
}

func validTheme(id string) bool {
	for _, t := range themeNames {
		if t.ID == id {
			return true
		}
	}
	return false
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
    <div class="muted">{{.Artifact.CreatedAt | when}}</div>
  </div>
{{end}}
</div>
{{else}}<div class="empty">Nothing to review. Accepted artifacts appear here until a human rates them.</div>{{end}}

<h2>Recent reviews</h2>
{{if .Recent}}
<table><thead><tr><th>When</th><th>Artifact</th><th>Score</th><th>Accepted</th><th>Note</th></tr></thead><tbody>
{{range .Recent}}<tr>
  <td class="muted">{{.CreatedAt | when}}</td>
  <td><a href="/ui/artifacts/{{.ArtifactID}}" class="mono">{{.ArtifactID | short}}</a></td>
  <td>{{printf "%.2f" .Score}}</td>
  <td>{{if .Accepted}}<span class="pill ok">yes</span>{{else}}<span class="pill bad">no</span>{{end}}</td>
  <td class="muted">{{.Note}}</td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No reviews recorded yet.</div>{{end}}
`

const notificationsBody = `
<h1>Notifications</h1>
<p class="sub">What needs you, in one place. Desktop notifications are opt-in and stay local.</p>
<div class="actions">
  <button id="enable-notifications">Enable desktop notifications</button>
  <span id="notify-status" class="muted"></span>
</div>
<div class="grid">
  <a class="card stat" href="/ui"><div class="n">{{.Counts.Pending}}</div><div class="l">{{plural .Counts.Pending "pending approval" "pending approvals"}}</div></a>
  <a class="card stat" href="/ui/reviews"><div class="n">{{.Counts.Reviews}}</div><div class="l">{{plural .Counts.Reviews "artifact" "artifacts"}} to review</div></a>
  <a class="card stat" href="#alarms"><div class="n">{{.Counts.Alarms}}</div><div class="l">{{plural .Counts.Alarms "active alarm" "active alarms"}}</div></a>
  <a class="card stat" href="#failures"><div class="n">{{.Counts.Failures}}</div><div class="l">{{plural .Counts.Failures "failed job" "failed jobs"}} (24h)</div></a>
</div>

<h2>Pending approvals</h2>
{{if .Pending}}<div class="card">{{range .Pending}}<div class="ev"><a href="/ui">{{.}}</a></div>{{end}}
<div class="actions"><a class="btn" href="/ui">Decide on the dashboard</a></div></div>
{{else}}<div class="empty">Nothing waiting for approval.</div>{{end}}

<h2>Review queue</h2>
{{if .Reviews}}<div class="card">{{range .Reviews}}<div class="ev">{{.}}</div>{{end}}
<div class="actions"><a class="btn" href="/ui/reviews">Open review queue</a></div></div>
{{else}}<div class="empty">No accepted work waiting for a review.</div>{{end}}

<h2 id="alarms">Active alarms</h2>
{{if .Alarms}}
<table><thead><tr><th>Level</th><th>Category</th><th>Message</th><th>When</th></tr></thead><tbody>
{{range .Alarms}}<tr>
  <td><span class="pill {{if eq .Level "critical"}}bad{{else if eq .Level "alert"}}warn{{end}}">{{.Level}}</span></td>
  <td>{{.Category}}</td><td>{{.Message}}</td><td class="muted">{{.CreatedAt | when}}</td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No active alarms.</div>{{end}}

<h2 id="failures">Failed jobs (24h)</h2>
{{if .Failures}}
<table><thead><tr><th>Job</th><th>Project</th><th>When</th></tr></thead><tbody>
{{range .Failures}}<tr>
  <td><a href="/ui/jobs/{{.ID}}/trace" class="mono">{{.ID | short}}</a></td>
  <td><a href="/ui/projects/{{.ProjectID}}">{{.ProjectID | short}}</a></td>
  <td class="muted">{{.UpdatedAt | when}}</td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No failed jobs in the last 24 hours.</div>{{end}}
`

const errorBody = `
<h1>Something went wrong</h1>
<div class="card">
  <p class="del">{{.Error}}</p>
  <p class="muted">The Core is still running. Nothing was changed by this request.</p>
  <div class="actions"><a class="btn" href="/ui">Back to dashboard</a></div>
</div>
`

const dashboardBody = `
<h1>Dashboard</h1>
<p class="sub">What needs you, and what the machine is doing.</p>
<div class="actions"><a class="btn" href="/ui/projects/new">+ New project</a></div>
<div class="grid">
  <div class="card stat"><div class="n">{{len .Active}}</div><div class="l">{{plural (len .Active) "active job" "active jobs"}}</div></div>
  <a class="card stat" href="/ui/reviews"><div class="n">{{.ReviewCount}}</div><div class="l">{{plural .ReviewCount "artifact" "artifacts"}} to review</div></a>
  <div class="card stat"><div class="n">{{len .Pending}}</div><div class="l">{{plural (len .Pending) "pending approval" "pending approvals"}}</div></div>
  <div class="card stat"><div class="n">{{if .Frozen}}Frozen{{else}}Not frozen{{end}}</div><div class="l">kill switch</div></div>
</div>

<h2>Last 24 hours</h2>
<div class="grid">
  <div class="card stat"><div class="n">{{.Digest.JobsCompleted}}</div><div class="l">jobs completed</div></div>
  <div class="card stat"><div class="n">{{.Digest.JobsFailed}}</div><div class="l">jobs failed</div></div>
  <div class="card stat"><div class="n">{{.Digest.Artifacts.Accepted}}</div><div class="l">artifacts accepted</div></div>
  <div class="card stat"><div class="n">{{.Digest.Tokens}}</div><div class="l">tokens spent</div></div>
</div>
{{if .Digest.Failures}}
<div class="card">
  <h3>Failures worth a look</h3>
  {{range .Digest.Failures}}<div class="ev"><a href="/ui/jobs/{{.JobID}}" class="mono">{{.JobID | short}}</a> — {{.Reason}}</div>{{end}}
</div>
{{end}}

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
    <input type="text" name="note" placeholder="note (optional)" class="grow">
  </div>
</form>
{{end}}
</div>
{{end}}

{{if .ReviewCount}}
<div class="card">
  <b>{{.ReviewCount}} accepted {{plural .ReviewCount "artifact" "artifacts"}} waiting for a review.</b>
  <span class="muted">The queue is blinded: kind and project only.</span>
  <div class="actions"><a class="btn" href="/ui/reviews">Open review queue</a></div>
</div>
{{end}}

<h2>Active jobs</h2>
{{if .Active}}
<table><thead><tr><th>Job</th><th>Project</th><th>Phase</th><th></th></tr></thead><tbody>
{{range .Active}}<tr>
  <td><a href="/ui/jobs/{{.ID}}" class="mono">{{.ID | short}}</a></td>
  <td><a href="/ui/projects/{{.ProjectID}}">{{index $.ProjectNames .ProjectID}}</a></td>
  <td><span class="pill {{stateClass .State}}" id="phase-{{.ID}}">{{.State}}</span></td>
  <td><a href="/ui/jobs/{{.ID}}">watch</a></td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No job is running. <a href="/ui/projects/new">Create a project</a> to get started.</div>{{end}}

<h2>Recently finished</h2>
{{if .Terminal}}
<table><thead><tr><th>Job</th><th>Project</th><th>State</th><th>Finished</th></tr></thead><tbody>
{{range .Terminal}}<tr>
  <td><a href="/ui/jobs/{{.ID}}" class="mono">{{.ID | short}}</a></td>
  <td><a href="/ui/projects/{{.ProjectID}}">{{index $.ProjectNames .ProjectID}}</a></td>
  <td><span class="pill {{stateClass .State}}">{{.State}}</span></td>
  <td class="muted">{{if .FinishedAt}}{{.FinishedAt | when}}{{end}}</td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">Nothing finished yet.</div>{{end}}

<h2>Recently accepted</h2>
{{if .RecentArtifacts}}
<table><thead><tr><th>Kind</th><th>Project</th><th>Version</th><th>Accepted</th><th></th></tr></thead><tbody>
{{range .RecentArtifacts}}<tr>
  <td><span class="pill">{{.Kind}}</span></td>
  <td><a href="/ui/projects/{{.ProjectID}}">{{index $.ProjectNames .ProjectID}}</a></td>
  <td>v{{.Version}}</td>
  <td class="muted">{{.CreatedAt | when}}</td>
  <td><a href="/ui/artifacts/{{.ID}}">open</a></td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No accepted artifacts yet.</div>{{end}}
`

const projectsBody = `
<h1>Projects</h1>
<p class="sub">Each project is one goal, its criteria, and the work history behind it.</p>
<div class="actions"><a class="btn" href="/ui/projects/new">+ New project</a></div>
{{if .Projects}}
<table><thead><tr><th>Name</th><th>Archetype</th><th>Status</th><th>Goal</th><th>Created</th></tr></thead><tbody>
{{range .Projects}}<tr>
  <td><a href="/ui/projects/{{.ID}}">{{.Name}}</a></td>
  <td><span class="pill">{{join .Capabilities}}</span></td>
  <td><span class="pill">{{.Status}}</span></td>
  <td class="muted">{{.Goal}}</td>
  <td class="muted">{{.CreatedAt | when}}</td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No projects yet. <a href="/ui/projects/new">Create the first one.</a></div>{{end}}
`

const newProjectBody = `
<h1>New project</h1>
<p class="sub">A project holds one goal, its criteria, and the work that follows. You do not choose a type: the system infers what it can produce and how to verify it from the goal and repository.</p>
<div class="actions"><a class="btn secondary" href="/ui/projects">All projects</a></div>
{{if .Error}}<div class="card"><p class="del">{{.Error}}</p></div>{{end}}
<div class="card">
<form method="post" action="/ui/projects">
  <label>Name <input name="name" value="{{.Name}}" required></label>
  <label>Goal <textarea name="goal" rows="2" minlength="3" maxlength="500" required>{{.Goal}}</textarea></label>
  <div class="field-help">A sentence is enough. The onboarding brief adds the limits, sources, and definition of success before any work starts.</div>
  <label>Repository path (optional) <input name="repository_path" value="{{.RepositoryPath}}" placeholder="/absolute/path/to/repo"></label>
  <label>Acceptance criteria (optional, one per line) <textarea name="criteria" rows="4">{{.Criteria}}</textarea></label>
  <div class="actions">
    <button name="start" value="" class="primary">Create and continue to onboarding</button>
    <button name="start" value="1" class="secondary">Create and start now</button>
  </div>
</form>
</div>
<p class="muted">Onboarding asks for clarification, limits, web sources, and success criteria, then explores directions for exploratory goals. Approving a plan starts work. A repository-backed project is indexed once before the first job.</p>
`

const interviewBody = `
<h1>Plan: {{.Project.Name}}</h1>
<p class="sub">{{.Project.Goal}}</p>
<div class="actions"><a class="btn secondary" href="/ui/projects/{{.Project.ID}}">Back to project</a></div>
<p>Status:
  <span class="pill {{if interviewDone .Interview.Status}}ok{{else}}warn{{end}}">{{interviewLabel .Interview.Status}}</span>
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
{{if .Plan}}<div class="card"><div class="prose">{{markdown .Plan}}</div></div>
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
    <p>This plan is <b>{{interviewLabel .Interview.Status}}</b>. Work has started.</p>
    <div class="actions"><a class="btn" href="/ui/projects/{{.Project.ID}}">Back to project</a></div>
  {{end}}
</div>
`

const projectBody = `
<h1>{{.Project.Name}} <span class="pill">{{join .Project.Capabilities}}</span></h1>
<p class="sub">{{.Project.Goal}}</p>
<div class="actions">
  <a class="btn" href="/ui/projects/{{.Project.ID}}/onboarding">Onboarding / explore</a>
  <a class="btn secondary" href="/ui/projects/{{.Project.ID}}/interview">Plan / interview</a>
  <a class="btn secondary" href="#submit-goal">Submit a goal</a>
  <a class="btn secondary" href="/ui/projects">All projects</a>
</div>

<div class="card">
  <div class="grid">
    <div><div class="l muted">Status</div><div><span class="pill">{{.Project.Status}}</span></div></div>
    <div><div class="l muted">Capabilities</div><div>{{join .Project.Capabilities}}</div></div>
    <div><div class="l muted">Repository</div><div class="mono wrap">{{if .Project.RepositoryPath}}{{.Project.RepositoryPath}}{{else}}—{{end}}</div></div>
    <div><div class="l muted">Verification</div><div class="mono wrap">{{if .Project.TestCommand}}{{.Project.TestCommand}}{{else}}No test command detected; the judge decides{{end}}</div></div>
    <div><div class="l muted">Created</div><div>{{.Project.CreatedAt | when}}</div></div>
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
    <td class="muted">{{if .StartedAt}}{{.StartedAt | when}}{{end}}</td>
  </tr>{{end}}
  </tbody></table>
  {{else}}<p class="muted">No job yet. Approve the plan or submit a goal.</p>{{end}}
</div>
{{else}}<div class="empty">No tasks yet.</div>{{end}}

<h2 id="submit-goal">Submit a goal</h2>
<div class="card">
<form method="post" action="/ui/projects/{{.Project.ID}}/goals">
  <label>goal <textarea name="goal" rows="3" minlength="3" maxlength="500" required></textarea></label>
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

<h2>Reject / correct an artifact</h2>
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
<input name="severity" placeholder="severity" class="w-90">
<input name="derived_rule" placeholder="rule" class="w-160">
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
<p class="sub"><a href="/ui/projects/{{.Job.ProjectID}}">{{.ProjectName}}</a> · task <span class="mono">{{.Job.TaskID | short}}</span>
  {{if .Job.PausedFrom}}· paused from {{.Job.PausedFrom}}{{end}}</p>
<div id="watch" data-job="{{.Job.ID}}"></div>

<div class="actions">
<form method="post" action="/ui/jobs/{{.Job.ID}}/control">
  {{if eq .Job.State "paused"}}<button name="action" value="resume">Resume</button>
  {{else if not .Job.State.Terminal}}<button name="action" value="pause" class="secondary">Pause</button>{{end}}
  {{if not .Job.State.Terminal}}<button name="action" value="cancel" class="danger">Cancel</button>{{end}}
  {{if .Job.State.Terminal}}<button name="action" value="retry">Retry</button>{{end}}
</form>
<a class="btn secondary" href="/ui/jobs/{{.Job.ID}}/trace">Full trace</a>
</div>

<div class="split">
  <div>
    <h2>Live output</h2>
    {{if .Job.State.Terminal}}
    <div class="empty">This job is finished. Live token output is shown while a job runs and is not retained here; open the artifact or the full trace.</div>
    {{else}}
    <pre id="tokens"></pre>
    {{end}}
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
  <h2>Interruptions</h2>
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
    {{if eq .Artifact.Kind "code"}}<pre>{{.Content}}</pre>{{else}}<div class="prose">{{markdown .Content}}</div>{{end}}
  </div>
  <div>
    <div class="card">
      <h2>How to run</h2>
      {{if .Project}}
        {{if .Project.TestCommand}}<p>Test: <code>{{.Project.TestCommand}}</code></p>
        {{else}}<p class="muted">No test command is configured for this project.</p>{{end}}
        {{if .Project.RepositoryPath}}<p>Repository: <code class="wrap">{{.Project.RepositoryPath}}</code></p>{{end}}
      {{else}}<p class="muted">Project metadata is unavailable.</p>{{end}}
    </div>

    <div class="card">
      <h2>What it cost</h2>
      {{if .HasOutcome}}
        <p><b>{{.Outcome.TokenCost}}</b> tokens · {{.Outcome.WallTime}} · {{resultLabel .Outcome.Result}}</p>
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
<h2>Strategy insights</h2>
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

<h2>Outcomes</h2>
{{if .Outcomes}}
<table><thead><tr><th>Job</th><th>Result</th><th>Score</th><th>Tokens</th><th>Wall</th><th>Arm</th><th>Reasoning</th><th>Refine / reframe</th></tr></thead><tbody>
{{range .Outcomes}}<tr>
  <td><a href="/ui/jobs/{{.JobID}}/trace" class="mono">{{.JobID | short}}</a></td>
  <td><span class="pill {{if eq .Result "accepted_new"}}ok{{else if eq .Result "failed"}}bad{{end}}">{{.Result}}</span></td>
  <td>{{printf "%.2f" .Score}}</td><td>{{.TokenCost}}</td><td>{{.WallTime}}</td>
  <td>{{if .ThinkArm}}<span class="pill">{{.ThinkArm}}</span>{{end}}</td>
  <td>{{.ReasoningTokens}}</td><td>{{.Refines}} / {{.Reframes}}</td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No captured outcomes yet.</div>{{end}}
`
