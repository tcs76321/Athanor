package ui

const setupBody = `
<h1>Setup</h1>
<p class="sub">Runtime, models, and the bounded qualification screen. Applying a routing change is a config edit; this page never writes it.</p>

<div class="card"><dl class="kv">
  <dt>supported runtime</dt><dd>Core Pod (ADR-0078)</dd>
  <dt>serving process</dt><dd>{{.Runtime}}</dd>
  <dt>version</dt><dd>{{.Version}}</dd>
  <dt>config</dt><dd><code>{{.ConfigPath}}</code></dd>
  {{if .OllamaURL}}<dt>Ollama</dt><dd><code>{{.OllamaURL}}</code></dd>{{end}}
</dl></div>

<h2>Model inventory</h2>
{{if .ModelsErr}}
<div class="card">
  <p class="del">Could not read the model catalog: {{.ModelsErr}}</p>
  <p class="muted">Check that Ollama is running and the Core can reach it. From the host: <code>ollama list</code>; from the Core: <code>athanor doctor</code>.</p>
</div>
{{else if .Models}}
<table><thead><tr><th>Model</th><th>Size</th><th>Family</th></tr></thead><tbody>
{{range .Models}}<tr><td class="mono">{{.Name}}</td><td>{{gib .SizeBytes}}</td><td>{{.Family}}</td></tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No models are installed. Pull one with <code>ollama pull &lt;model&gt;</code>, then reload.</div>{{end}}

<h2>Personas</h2>
{{if .Roles}}
<table><thead><tr><th>Role</th><th>Configured model</th><th>Family</th><th>Context</th><th>Installed</th></tr></thead><tbody>
{{range .Roles}}<tr>
  <td>{{.Role}}</td><td class="mono">{{.Model}}</td><td>{{.Family}}</td>
  <td>{{.ContextTarget}}</td>
  <td>{{if .Installed}}<span class="pill ok">yes</span>{{else}}<span class="pill bad">missing</span>{{end}}</td>
</tr>{{end}}
</tbody></table>
{{else}}<div class="empty">Configuration is not available to the UI.</div>{{end}}

<h2>Qualification</h2>
{{if .State.Running}}
<div class="card">
  <p><b>{{.State.Done}} / {{.State.Total}}</b> cases complete.</p>
  <p class="muted">This page refreshes every 5 seconds. You can leave it; the run is resumable.</p>
  <script>setTimeout(function(){location.reload()},5000)</script>
</div>
{{else}}
<div class="card">
  <p>Runs four fixed-label judge pairs for <code>security</code> and three deterministic generator cases for <code>main</code>, against the configured model plus the largest installed alternative. It is a machine-local screen, not the frozen benchmark.</p>
  {{if .State.Error}}<p class="del">{{.State.Error}}</p>{{end}}
  {{if .Stale}}<p class="muted">A previous run looks stale; starting again resets it.</p>{{end}}
  <form method="post" action="/ui/setup/qualify">
    {{if .Measurements}}<input type="hidden" name="fresh" value="1">{{end}}
    <button>{{if .Measurements}}Run again{{else}}Run qualification{{end}}</button>
  </form>
</div>
{{end}}

{{if .Measurements}}
<h2>Measurements</h2>
<table><thead><tr><th>Role</th><th>Model</th><th>Score</th><th>Samples</th></tr></thead><tbody>
{{range .Measurements}}<tr>
  <td>{{.Role}}</td><td class="mono">{{.Model}}</td>
  <td><span class="pill {{if ge .Score 0.75}}ok{{else if ge .Score 0.5}}warn{{else}}bad{{end}}">{{printf "%.2f" .Score}}</span></td>
  <td>{{.Samples}}</td>
</tr>{{end}}
</tbody></table>

<h2>Routing proposal</h2>
<table><thead><tr><th>Role</th><th>Configured</th><th>Proposed</th><th>Score</th><th>Why</th></tr></thead><tbody>
{{range .Recommendations}}<tr>
  <td>{{.Role}}</td><td class="mono">{{.Configured}}</td>
  <td>{{if .Replace}}<span class="pill ok">{{.Recommended}}</span>{{else}}<span class="muted">keep</span>{{end}}</td>
  <td>{{printf "%.2f" .Score}}</td><td class="muted">{{.Reason}}</td>
</tr>{{end}}
</tbody></table>
{{if .Apply}}
<div class="card">
  <p>Apply by editing <code>config.yaml</code> and restarting the Core Pod:</p>
  <pre>{{.Apply}}</pre>
</div>
{{else}}<p class="muted">The screen does not support a change; the configured routing stands.</p>{{end}}
{{end}}

<p class="muted">CLI fallback: <code>athanor models -ladder</code>. The CLI writes <code>qualification.json</code> next to the config; the UI keeps its run in the Core's state.</p>
`
