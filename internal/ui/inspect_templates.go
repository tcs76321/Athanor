package ui

// Read-only inspector pages (events, artifacts, trace, memory, daydream,
// runtime). These expose what the daemon actually records; nothing here writes.

const inspectTabs = `
<div class="tabs">
  <a href="/ui/events" {{if eq .Tab "events"}}aria-current="page"{{end}}>Events</a>
  <a href="/ui/artifacts" {{if eq .Tab "artifacts"}}aria-current="page"{{end}}>Artifacts</a>
  <a href="/ui/memory" {{if eq .Tab "memory"}}aria-current="page"{{end}}>Memory</a>
  <a href="/ui/daydream" {{if eq .Tab "daydream"}}aria-current="page"{{end}}>Daydream</a>
  <a href="/ui/runtime" {{if eq .Tab "runtime"}}aria-current="page"{{end}}>Runtime</a>
</div>`

const eventsBody = inspectTabs + `
<h1>Events</h1>
<p class="sub">Every recorded step, newest first. Filter by job, project, category, level, or text.</p>
<div class="card">
<form method="get" action="/ui/events">
  <div class="row">
    <label>category
      <select name="category"><option value="">all</option>
      {{$cat := .Filter.Get "category"}}{{range .Categories}}<option value="{{.}}" {{if eq . $cat}}selected{{end}}>{{.}}</option>{{end}}
      </select>
    </label>
    <label>level
      <select name="level"><option value="">all</option>
      {{$lvl := .Filter.Get "level"}}
      <option value="debug" {{if eq "debug" $lvl}}selected{{end}}>debug</option>
      <option value="info" {{if eq "info" $lvl}}selected{{end}}>info</option>
      <option value="warn" {{if eq "warn" $lvl}}selected{{end}}>warn</option>
      <option value="error" {{if eq "error" $lvl}}selected{{end}}>error</option>
      </select>
    </label>
    <label>job <input name="job" value="{{.Filter.Get "job"}}" placeholder="job id"></label>
  </div>
  <div class="row">
    <label>project <input name="project" value="{{.Filter.Get "project"}}" placeholder="project id"></label>
    <label>search <input name="q" value="{{.Filter.Get "q"}}" placeholder="substring in the event JSON"></label>
  </div>
  <div class="actions"><button>Filter</button><a class="btn secondary" href="/ui/events">Clear</a></div>
</form>
</div>

{{if .Events}}
<table><thead><tr><th>When</th><th>Category</th><th>Level</th><th>Event</th><th>Job</th></tr></thead><tbody>
{{range .Events}}<tr>
  <td class="muted">{{.TS | ts}}</td>
  <td><span class="pill">{{.Category}}</span></td>
  <td><span class="pill {{if eq .Level "error"}}bad{{else if eq .Level "warn"}}warn{{end}}">{{.Level}}</span></td>
  <td><b>{{.Name}}</b><div class="muted">{{.Summary}}</div>
    <details><summary>raw event</summary><pre>{{.DataJSON}}</pre></details></td>
  <td>{{if .JobID}}<a href="/ui/jobs/{{.JobID}}/trace" class="mono">{{.JobID | short}}</a>{{end}}</td>
</tr>{{end}}
</tbody></table>
{{if .NextURL}}<div class="actions"><a class="btn" href="{{.NextURL}}">Older events</a></div>
{{else}}<p class="muted">End of the log.</p>{{end}}
{{else}}<div class="empty">No events match this filter.</div>{{end}}
`

const artifactIndexBody = inspectTabs + `
<h1>Artifacts</h1>
<p class="sub">Every artifact the daemon has produced, newest first, whatever its status.</p>
{{if .Artifacts}}
<table><thead><tr><th>Kind</th><th>Version</th><th>Status</th><th>Project</th><th>Job</th><th>Created</th><th></th></tr></thead><tbody>
{{range .Artifacts}}<tr>
  <td><span class="pill">{{.Kind}}</span></td>
  <td>v{{.Version}}</td>
  <td><span class="pill {{stateClass .Status}}">{{.Status}}</span></td>
  <td><a href="/ui/projects/{{.ProjectID}}">{{index $.ProjectNames .ProjectID}}</a></td>
  <td><a href="/ui/jobs/{{.JobID}}/trace" class="mono">{{.JobID | short}}</a></td>
  <td class="muted">{{.CreatedAt | when}}</td>
  <td><a href="/ui/artifacts/{{.ID}}">open</a></td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No artifacts yet.</div>{{end}}
`

const traceBody = inspectTabs + `
<h1>Trace <span class="mono">{{.Job.ID | short}}</span>
  <span class="pill {{stateClass .Job.State}}">{{.Job.State}}</span></h1>
<p class="sub">
  {{if .Project}}<a href="/ui/projects/{{.Project.ID}}">{{.Project.Name}}</a>{{end}}
  · task <span class="mono">{{.Job.TaskID | short}}</span>
  · attempt {{.Job.Attempt}}
  {{if .Job.StartedAt}}· started {{.Job.StartedAt | when}}{{end}}
  {{if .Job.FinishedAt}}· finished {{.Job.FinishedAt | when}}{{end}}
</p>
<div class="actions">
  <a class="btn secondary" href="/ui/jobs/{{.Job.ID}}">Watch</a>
  <a class="btn secondary" href="/ui/events?job={{.Job.ID}}">Paginated events</a>
</div>

<div class="grid">
  <div class="card stat"><div class="n">{{.EventCount}}</div><div class="l">events</div></div>
  <div class="card stat"><div class="n">{{len .Calls}}</div><div class="l">model calls</div></div>
  <div class="card stat"><div class="n">{{.TotalTokens}}</div><div class="l">tokens</div></div>
  <div class="card stat"><div class="n">{{len .Evals}}</div><div class="l">evaluation records</div></div>
  <div class="card stat"><div class="n">{{len .Artifacts}}</div><div class="l">artifacts</div></div>
</div>

{{if .Units}}
<div class="card">
  <h2>Task analysis</h2>
  <table><thead><tr><th>Unit</th><th>Mode</th><th>Goal</th><th>Falsifier</th></tr></thead><tbody>
  {{range .Units}}<tr><td class="mono">{{.ID}}</td><td><span class="pill {{if eq .Mode "deliberate"}}warn{{end}}">{{.Mode}}</span></td>
  <td>{{.Goal}}{{if .Reason}}<div class="muted">{{.Reason}}</div>{{end}}</td><td class="muted">{{.Falsifier}}</td></tr>{{end}}
  </tbody></table>
</div>
{{end}}

<h2>Model calls</h2>
{{if not .Capture}}<p class="muted">Payload capture is off. Enable <code>logging.capture_llm_payloads</code> to record the exact prompt messages and response; existing calls stay counts-only.</p>{{end}}
{{if .Calls}}
<table><thead><tr><th>Phase</th><th>Persona</th><th>Model</th><th>Prompt</th><th>Completion</th><th>Reasoning</th><th>Think</th><th>Temp</th><th>Payload</th></tr></thead><tbody>
{{range .Calls}}<tr>
  <td>{{.Phase}}</td><td>{{.Persona}}</td><td class="mono">{{.Model}}</td>
  <td>{{.Prompt}}</td><td>{{.Completion}}</td><td>{{.Reasoning}}</td>
  <td><span class="pill {{if eq .Think "off"}}{{else}}warn{{end}}">{{.Think}}</span></td><td>{{.Temperature}}</td>
  <td>{{if or .Response .PromptMessages}}<details><summary>open</summary>
    {{range .PromptMessages}}<b>{{.Role}}</b><pre>{{.Content}}</pre>{{end}}
    {{if .Response}}<b>response</b><pre>{{.Response}}</pre>{{end}}
    {{if .Thinking}}<b>thinking</b><pre>{{.Thinking}}</pre>{{end}}
    </details>{{else}}<span class="muted">—</span>{{end}}</td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No model calls recorded.</div>{{end}}

<h2>Checks and tool calls</h2>
{{if .Checks}}
<table><thead><tr><th>Check</th><th>Result</th><th>When</th><th>Detail</th></tr></thead><tbody>
{{range .Checks}}<tr>
  <td>{{.Name}}<div class="muted">{{.Summary}}</div></td>
  <td>{{if eq .Passed "pass"}}<span class="pill ok">pass</span>{{else if eq .Passed "fail"}}<span class="pill bad">fail</span>{{else}}<span class="pill">—</span>{{end}}</td>
  <td class="muted">{{.TS}}</td>
  <td><details><summary>open</summary><pre>{{.DataJSON}}</pre></details></td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No checks or tool calls recorded.</div>{{end}}
{{if .Actions}}
<h3>Persisted action rows</h3>
<table><thead><tr><th>Seq</th><th>Tool</th><th>Status</th><th>When</th><th>Request / response</th></tr></thead><tbody>
{{range .Actions}}<tr>
  <td>{{.Seq}}</td><td class="mono">{{.Tool}}</td>
  <td><span class="pill {{if eq .Status "failed"}}bad{{else if eq .Status "succeeded"}}ok{{end}}">{{.Status}}</span></td>
  <td class="muted">{{.CreatedAt}}</td>
  <td><details><summary>open</summary>
    <b>request</b><pre>{{.RequestJSON}}</pre>
    <b>response</b><pre>{{.ResponseJSON}}</pre>
  </details></td>
</tr>{{end}}
</tbody></table>
{{end}}

<h2>Evaluation records</h2>
{{if .Evals}}
<table><thead><tr><th>Score</th><th>Passed</th><th>Failed tests</th><th>Missing criteria</th><th>Summary</th></tr></thead><tbody>
{{range .Evals}}<tr>
  <td>{{printf "%.2f" .Score}}</td><td>{{.PassedTests}}</td>
  <td class="muted">{{join .FailedTests}}</td><td class="muted">{{join .MissingCriteria}}</td>
  <td>{{.Summary}}</td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No evaluation records.</div>{{end}}

<h2>Artifacts</h2>
{{if .Artifacts}}
<table><thead><tr><th>Kind</th><th>Version</th><th>Status</th><th></th></tr></thead><tbody>
{{range .Artifacts}}<tr><td>{{.Kind}}</td><td>v{{.Version}}</td>
<td><span class="pill {{stateClass .Status}}">{{.Status}}</span></td>
<td><a href="/ui/artifacts/{{.ID}}">open</a>{{if .SupersedesID}} · <a href="/ui/artifacts/{{.ID}}/diff">diff</a>{{end}}</td></tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No artifacts.</div>{{end}}

{{if .ContextEvents}}
<h2>Context and memory</h2>
<div class="card">
{{range .ContextEvents}}<div class="ev"><span class="muted">{{.TS | ts}}</span> {{prettyEvent .DataJSON}}</div>{{end}}
</div>
{{end}}

{{if .Notes}}
<div class="card"><h2>Interruptions</h2>{{range .Notes}}<div class="ev">{{.}}</div>{{end}}</div>
{{end}}

<h2>Strategy</h2>
<div class="card">
<details><summary>Profile</summary><pre>{{tojson .Profile}}</pre></details>
{{if .HasOutcome}}<details><summary>Outcome</summary><pre>{{tojson .Outcome}}</pre></details>
{{else}}<p class="muted">No outcome captured yet.</p>{{end}}
</div>

<h2>Timeline <span class="muted">(last {{len .Events}} of {{.EventCount}})</span></h2>
{{range .Events}}<details><summary>{{.TS | ts}} · {{.Name}}</summary><pre>{{.DataJSON}}</pre></details>{{end}}
`

const memoryBody = inspectTabs + `
<h1>Memory</h1>
<p class="sub">Compacted memory, context chunks, the Dormant Index, and embeddings.</p>
<div class="grid">
  <div class="card stat"><div class="n">{{.Counts.Compacted}}</div><div class="l">compacted</div></div>
  <div class="card stat"><div class="n">{{.Counts.Chunks}}</div><div class="l">chunks</div></div>
  <div class="card stat"><div class="n">{{.Counts.Dormant}}</div><div class="l">dormant summaries</div></div>
  <div class="card stat"><div class="n">{{.Counts.Embeddings}}</div><div class="l">embeddings</div></div>
</div>

<h2>Compacted memory</h2>
{{if .Compacted}}
<table><thead><tr><th>Kind</th><th>Profile</th><th>Persona</th><th>Source</th><th>Bytes</th><th>Created</th><th></th></tr></thead><tbody>
{{range .Compacted}}<tr>
  <td><span class="pill">{{.Kind}}</span></td><td>{{.ProfileType}}</td><td>{{.Persona}}</td>
  <td class="muted">{{.SourceBytes}} → {{.CompactedBytes}}</td>
  <td>{{if .ProjectID}}<a href="/ui/projects/{{.ProjectID}}" class="mono">{{.ProjectID | short}}</a>{{end}}</td>
  <td class="muted">{{.CreatedAt}}</td>
  <td><a href="/ui/memory/compacted/{{.ID}}">open</a></td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No compacted memory.</div>{{end}}

<h2>Context chunks</h2>
{{if .Chunks}}
<table><thead><tr><th>Source</th><th>Kind</th><th>Lines</th><th>Bytes</th><th>Created</th><th></th></tr></thead><tbody>
{{range .Chunks}}<tr>
  <td class="mono">{{.SourceRelpath}}</td><td>{{.Kind}}</td>
  <td>{{.LineStart}}–{{.LineEnd}}</td><td>{{.Bytes}}</td>
  <td class="muted">{{.CreatedAt}}</td>
  <td><a href="/ui/memory/chunks/{{.ID}}">open</a></td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No context chunks.</div>{{end}}

<h2>Dormant Index</h2>
{{if .Dormant}}
<table><thead><tr><th>Chunk</th><th>Status</th><th>Summary</th><th>Created</th></tr></thead><tbody>
{{range .Dormant}}<tr>
  <td><a href="/ui/memory/chunks/{{.ChunkID}}" class="mono">{{.ChunkID | short}}</a></td>
  <td><span class="pill {{if eq .Status "ready"}}ok{{else if eq .Status "failed"}}bad{{else}}warn{{end}}">{{.Status}}</span></td>
  <td class="muted">{{.Summary}}</td><td class="muted">{{.CreatedAt}}</td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">The Dormant Index is empty.</div>{{end}}

<h2>Indexed sources</h2>
{{if .Sources}}
<table><thead><tr><th>Project</th><th>Path</th><th>Lang</th><th>Size</th><th>Chunks</th><th>Status</th><th>Indexed</th></tr></thead><tbody>
{{range .Sources}}<tr>
  <td><a href="/ui/projects/{{.ProjectID}}" class="mono">{{.ProjectID | short}}</a></td>
  <td class="mono">{{.Relpath}}</td><td>{{.Lang}}</td><td>{{.Size}}</td><td>{{.Chunks}}</td>
  <td><span class="pill {{if eq .Status "ok"}}ok{{else if eq .Status "failed"}}bad{{end}}">{{.Status}}</span></td>
  <td class="muted">{{.IndexedAt}}</td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No indexed sources.</div>{{end}}

<h2>Embeddings</h2>
{{if .Embeddings}}
<table><thead><tr><th>Owner</th><th>Source</th><th>Model</th><th>Dim</th><th>Project</th><th>Created</th></tr></thead><tbody>
{{range .Embeddings}}<tr>
  <td class="mono">{{.OwnerID | short}}</td><td>{{.SourceKind}}</td><td class="mono">{{.Model}}</td>
  <td>{{.Dim}}</td>
  <td>{{if .ProjectID}}<a href="/ui/projects/{{.ProjectID}}" class="mono">{{.ProjectID | short}}</a>{{end}}</td>
  <td class="muted">{{.CreatedAt}}</td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No embeddings.</div>{{end}}

<h2>Active chunks</h2>
{{if .Active}}
<table><thead><tr><th>Scope</th><th>Chunk</th><th>Updated</th></tr></thead><tbody>
{{range .Active}}<tr>
  <td class="mono">{{.ScopeID}}</td>
  <td><a href="/ui/memory/chunks/{{.ChunkID}}" class="mono">{{.ChunkID | short}}</a></td>
  <td class="muted">{{.UpdatedAt}}</td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No active chunks.</div>{{end}}
`

const memoryDetailBody = `
<h1>{{.Title}}</h1>
<p class="sub">{{.Kind}}</p>
<div class="card"><dl class="kv">{{range $k, $v := .Meta}}<dt>{{$k}}</dt><dd>{{$v}}</dd>{{end}}</dl></div>
<div class="card"><pre>{{.Content}}</pre></div>
<div class="actions"><a class="btn secondary" href="/ui/memory">Back to memory</a></div>
`

const daydreamBody = inspectTabs + `
<h1>Daydream</h1>
<p class="sub">Idle-time consolidation, exploration, and strategy mining.</p>
{{if .Logs}}
<table><thead><tr><th>Action</th><th>Persona</th><th>Window</th><th>Chunks</th><th>Tokens saved</th><th>Dormant</th><th></th></tr></thead><tbody>
{{range .Logs}}<tr>
  <td><span class="pill">{{.Action}}</span></td><td>{{.Persona}}</td>
  <td class="muted">{{.StartedAt}} → {{.FinishedAt}}</td>
  <td>{{.Chunks}}</td><td>{{.TokensSaved}}</td><td>{{.Dormant}}</td>
  <td><details><summary>output</summary>
    <pre>artifacts: {{.ArtifactsJSON}}
corrections: {{.CorrectionsJSON}}
insights: {{.InsightsJSON}}</pre>
  </details></td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No daydream passes recorded.</div>{{end}}
`

const runtimeBody = inspectTabs + `
<h1>Runtime</h1>
<p class="sub">Read-only view of the daemon's personas, budget, alarms, and decision history.</p>
<div class="grid">
  <div class="card stat"><div class="n">{{.Counts.Jobs}}</div><div class="l">jobs</div></div>
  <div class="card stat"><div class="n">{{.Counts.Events}}</div><div class="l">events</div></div>
  <div class="card stat"><div class="n">{{.Counts.Artifacts}}</div><div class="l">artifacts</div></div>
  <div class="card stat"><div class="n">{{.Counts.Outcomes}}</div><div class="l">outcomes</div></div>
  <div class="card stat"><div class="n">{{.Counts.State}}</div><div class="l">state rows</div></div>
  <div class="card stat"><div class="n">{{.Counts.Quarantined}}</div><div class="l">quarantined files</div></div>
</div>

<h2>Personas</h2>
{{if .Personas}}
<table><thead><tr><th>Role</th><th>Model</th><th>Family</th><th>Context target</th></tr></thead><tbody>
{{range .Personas}}<tr><td>{{.Role}}</td><td class="mono">{{.Model}}</td><td>{{.Family}}</td><td>{{.ContextTarget}}</td></tr>{{end}}
</tbody></table>
{{else}}<div class="empty">Configuration is not available to the UI.</div>{{end}}

<h2>Horizon budget</h2>
{{if .HasBudget}}
<div class="card"><dl class="kv">
  <dt>window</dt><dd>{{.Budget.Window}} → {{.Budget.End}}</dd>
  <dt>tokens</dt><dd>{{.Budget.TokensCommitted}} committed of {{.Budget.TokensCeiling}}</dd>
  <dt>interactive left</dt><dd>{{.Budget.InteractiveLeft}}</dd>
  <dt>background left</dt><dd>{{.Budget.BackgroundLeft}}</dd>
</dl></div>
{{else}}<div class="empty">The budget ledger is disabled (both ceilings are 0).</div>{{end}}

<h2>Alarms</h2>
{{if .Alarms}}
<table><thead><tr><th>Level</th><th>Category</th><th>Message</th><th>Status</th><th>When</th></tr></thead><tbody>
{{range .Alarms}}<tr>
  <td><span class="pill {{if eq .Level "critical"}}bad{{else if eq .Level "alert"}}warn{{end}}">{{.Level}}</span></td>
  <td>{{.Category}}</td><td>{{.Message}}</td><td>{{.Status}}</td><td class="muted">{{.CreatedAt}}</td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No alarms.</div>{{end}}

<h2>Human decisions</h2>
{{if .Decisions}}
<table><thead><tr><th>Type</th><th>Severity</th><th>Status</th><th>Job</th><th>When</th><th>Payload</th></tr></thead><tbody>
{{range .Decisions}}<tr>
  <td>{{.Type}}</td><td>{{.Severity}}</td><td>{{.Status}}</td>
  <td>{{if .JobID}}<a href="/ui/jobs/{{.JobID}}/trace" class="mono">{{.JobID | short}}</a>{{end}}</td>
  <td class="muted">{{.CreatedAt}}</td>
  <td><details><summary>open</summary><pre>{{.Payload}}</pre></details></td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No HITL requests.</div>{{end}}

<h2>Quarantined files</h2>
{{if .Quarantine}}
<table><thead><tr><th>Path</th><th>Reason</th><th>Pipeline</th><th>Size</th><th>When</th></tr></thead><tbody>
{{range .Quarantine}}<tr>
  <td class="mono">{{.Relpath}}</td><td>{{.Reason}}</td><td>{{.Pipeline}}</td>
  <td>{{.Size}}</td><td class="muted">{{.IngestedAt}}</td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No quarantined files.</div>{{end}}

<h2>System state</h2>
{{if .State}}
<table><thead><tr><th>Key</th><th>Value</th></tr></thead><tbody>
{{range .State}}<tr><td class="mono">{{.Key}}</td>
<td><details><summary>open</summary><pre>{{.Value}}</pre></details></td></tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No per-job state rows.</div>{{end}}

{{if .ConfigPath}}<p class="muted">config: <code>{{.ConfigPath}}</code></p>{{end}}
`
