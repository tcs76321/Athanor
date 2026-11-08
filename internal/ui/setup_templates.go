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
</dl>
<p class="muted">On the host, the installer config is <code>~/.config/athanor/config.yaml</code>; restart the Core Pod after editing it.</p>
</div>

<h2>Model inventory</h2>
{{if .ModelsErr}}
<div class="card">
  <p class="del">Could not read the model catalog: {{.ModelsErr}}</p>
  <p class="muted">Check that Ollama is running and the Core can reach it. From the host: <code>ollama list</code>; from the Core: <code>athanor doctor</code>.</p>
</div>
{{else if .Models}}
<table><thead><tr><th>Model</th><th>Size</th><th>Family</th></tr></thead><tbody>
{{range .Models}}<tr><td class="mono">{{.Name}}</td><td>{{gib .SizeBytes}}</td><td>{{if .Family}}{{.Family}}{{else}}—{{end}}</td></tr>{{end}}
</tbody></table>
{{else}}<div class="empty">No models are installed. Pull one with <code>ollama pull &lt;model&gt;</code>, then reload.</div>{{end}}

<h2>Personas</h2>
{{if .Applied}}<div class="notice">Routing saved. The Core applies it on restart; if a restart banner is still shown, run the command it names.</div>{{end}}
{{if .Roles}}
<form method="post" action="/ui/setup/personas">
<div class="card flush"><div class="table-wrap">
<table><thead><tr><th>Role</th><th>Model</th><th>Family</th><th>Context target</th><th>Installed</th></tr></thead><tbody>
{{range .Roles}}<tr>
  <td>{{.Role}}</td>
  <td><select name="model_{{.Role}}" aria-label="{{.Role}} model">
    {{$role := .}}{{range $.Models}}<option value="{{.Name}}" {{if eq .Name $role.Model}}selected{{end}}>{{.Name}}</option>{{end}}
  </select></td>
  <td>{{.Family}}</td>
  <td><input type="number" name="context_{{.Role}}" value="{{.ContextTarget}}" min="2048" max="1048576" step="1024" class="w-90"></td>
  <td>{{if .Installed}}<span class="pill ok">yes</span>{{else}}<span class="pill bad">missing</span>{{end}}</td>
</tr>{{end}}
</tbody></table>
</div></div>
<div class="actions"><button class="primary">Save routing{{if .Roles}} and restart{{end}}</button></div>
<p class="muted small">The model list is the installed catalog. Context targets are working windows, capped by each model's measured capacity; run <code>athanor models -audit</code> to refresh resident sizes.</p>
</form>
{{else}}<div class="empty">Configuration is not available to the UI.</div>{{end}}

<h2>Measured model fit</h2>
{{if .ModelAudit}}
<p class="muted">Measured by <code>athanor models -audit</code> at {{.ModelAudit.GeneratedAt}}. Host {{gib .ModelAudit.HostBytes}}, reserve {{gib .ModelAudit.ReserveBytes}}, budget {{gib .ModelAudit.BudgetBytes}}. Loaded is the actual weights + KV size Ollama reported, not the file size on disk.</p>
{{if .ModelAuditFailing}}<div class="notice bad">Measured fit rejects {{len .ModelAuditFailing}} role(s). Fix the persona model or re-run the audit.</div>{{end}}
<table><thead><tr><th>Role</th><th>Model</th><th>Context</th><th>File</th><th>Loaded</th><th>Fit</th></tr></thead><tbody>
{{range .ModelAudit.Roles}}<tr>
  <td>{{.Role}}</td><td class="mono">{{.Model}}</td><td>{{.ContextTarget}}</td>
  <td>{{gib .FileBytes}}</td><td>{{gib .LoadedBytes}}</td>
  <td>{{if .Fits}}<span class="pill ok">fits</span>{{else}}<span class="pill bad">{{if .Error}}{{.Error}}{{else}}does not fit{{end}}</span>{{end}}</td>
</tr>{{end}}
</tbody></table>
{{if .ModelAudit.Resident}}<p class="muted">Resident at audit end: {{join .ModelAudit.Resident}}</p>{{end}}
{{else}}
<div class="notice">No measured model audit is attached to this Core. Run <code>athanor models -audit</code> on the host and restart Athanor to measure weights + KV at the configured context.</div>
{{end}}

<h2>Qualification (optional)</h2>
{{if .Qualification.Running}}
<div class="card" data-live-card="setup-qualification" data-live-interval="2000">
  <div class="row row-middle"><h3>Qualification running</h3><span class="pill run">{{.Qualification.Percent}}%</span></div>
  <progress class="progress" value="{{.Qualification.Percent}}" max="100">{{.Qualification.Percent}}%</progress>
  <p><b>{{.Qualification.Done}} / {{.Qualification.Total}}</b> case runs complete{{if .Qualification.Waiting}} · <b>waiting for the current job to finish</b>{{else if .Qualification.CurrentModel}} · measuring <b class="mono">{{.Qualification.CurrentModel}}</b>{{end}}{{if .Qualification.Elapsed}} · {{.Qualification.Elapsed}} elapsed{{end}}{{if .Qualification.Remaining}} · about {{.Qualification.Remaining}} remaining{{else}} · estimating remaining time…{{end}}</p>
  <p class="muted">{{if eq .Qualification.Tier "minimal"}}The Minimal screen covers each capability twice on every model.{{else if eq .Qualification.Tier "balanced"}}The Balanced screen runs the whole standard ladder.{{else}}The Complete screen runs every deep case.{{end}} It keeps running if you leave this page and resumes after a Core restart; it waits rather than evicting a model a job is using.</p>
  <form method="post" action="/ui/setup/qualify/cancel">
    <input type="hidden" name="next" value="/ui/setup">
    <button class="danger small" data-confirm="Stop this qualification run? Completed cases are kept and the run can resume later.">Stop qualification</button>
  </form>
</div>
{{else}}
<div class="card">
  <p>Measures every pool model on the grounded generator and judge screen. The result ranks the pool and feeds the functional roster; it is a machine-local prior, not the frozen capability benchmark. Paused and running jobs keep inference: the screen waits for them instead of evicting their model.</p>
  {{if .Qualification.Error}}<p class="del">{{.Qualification.Error}}</p>{{end}}
  {{if .Qualification.Interrupted}}<p class="muted">A previous run was interrupted. Choosing a depth resumes it from the completed cases.</p>{{else if .Stale}}<p class="muted">A previous run has not reported progress for a long time. Choosing a depth starts it again from the completed cases.</p>{{end}}
  <form method="post" action="/ui/setup/qualify" class="tier-options">
    <button type="submit" name="tier" value="minimal" class="tier-option {{if eq .Tier "minimal"}}selected{{end}}">
      <span class="tier-name">Minimal</span>
      <span class="tier-desc">Two grounded cases per capability. · <span data-live-text="qual-minimal-estimate">{{.Qualification.MinimalEstimate}}</span></span>
    </button>
    <button type="submit" name="tier" value="balanced" class="tier-option {{if eq .Tier "balanced"}}selected{{end}}">
      <span class="tier-badge">Recommended</span>
      <span class="tier-name">Balanced</span>
      <span class="tier-desc">Every case in the standard ladder. · <span data-live-text="qual-balanced-estimate">{{.Qualification.BalancedEstimate}}</span></span>
    </button>
    <button type="submit" name="tier" value="complete" class="tier-option {{if eq .Tier "complete"}}selected{{end}}">
      <span class="tier-name">Complete</span>
      <span class="tier-desc">Every case in the deep ladder. · <span data-live-text="qual-complete-estimate">{{.Qualification.CompleteEstimate}}</span></span>
    </button>
  </form>
  {{if .HasMeasurements}}
  <form method="post" action="/ui/setup/qualify">
    <input type="hidden" name="tier" value="{{.Tier}}">
    <input type="hidden" name="fresh" value="1">
    <button class="secondary small" data-confirm="Discard every recorded case and measure the pool again?">Run again from scratch</button>
  </form>
  {{end}}
</div>
{{end}}

{{if .PoolRanked}}
<h2>Measured ranking</h2>
<p class="muted">A smoothed success rate on this machine from the grounded screen. Samples are the number of cases behind the score; the ranking is a prior, and real job outcomes update it over time.</p>
<div class="card flush"><div class="table-wrap">
<table><thead><tr><th>Model</th><th>Family</th><th>Size</th><th>Overall</th><th>Code</th><th>Data</th><th>Document</th><th>Judgment</th><th>Security</th><th>Speed</th><th>Samples</th></tr></thead><tbody>
{{range .PoolRanked}}<tr>
  <td class="mono">{{.Name}}</td>
  <td>{{if .Family}}{{.Family}}{{else}}—{{end}}</td>
  <td class="mono">{{gib .SizeBytes}}</td>
  <td><span class="pill {{if ge .Score 0.75}}ok{{else if ge .Score 0.5}}warn{{else}}bad{{end}}">{{printf "%.0f%%" (mul .Score 100)}}</span></td>
  <td>{{if .Code}}{{printf "%.0f%%" (mul .Code 100)}}{{else}}—{{end}}</td>
  <td>{{if .Data}}{{printf "%.0f%%" (mul .Data 100)}}{{else}}—{{end}}</td>
  <td>{{if .Document}}{{printf "%.0f%%" (mul .Document 100)}}{{else}}—{{end}}</td>
  <td>{{if .Judgment}}{{printf "%.0f%%" (mul .Judgment 100)}}{{else}}—{{end}}</td>
  <td>{{if .Security}}{{printf "%.0f%%" (mul .Security 100)}}{{else}}—{{end}}</td>
  <td>{{if .Speed}}{{printf "%.0f" .Speed}}{{else}}—{{end}}</td>
  <td>{{.Samples}}</td>
</tr>{{end}}
</tbody></table>
</div></div>
<p class="muted small">Persona routing is edited above. The composition pool uses the same measurements, so a saved persona table and the measured ranking cannot silently disagree.</p>
{{end}}

{{if .QualDetails}}
<h2>Case detail</h2>
<p class="muted">Every recorded case, the question it was asked, the expected answer, and what it produced.</p>
{{range .QualDetails}}
<details class="case-detail">
  <summary><b class="mono">{{.Model}}</b> — {{.Passed}}/{{.Total}} passed</summary>
  <div class="table-wrap">
  <table><thead><tr><th>Case</th><th>Capability</th><th>Result</th><th>Question / expected / answer</th></tr></thead><tbody>
  {{range .Cases}}<tr>
    <td class="mono">{{.Kind}} · {{.CaseID}}</td>
    <td>{{if .Capability}}{{.Capability}}{{else}}—{{end}}</td>
    <td>{{if .Passed}}<span class="pill ok">pass</span>{{else}}<span class="pill bad">fail</span>{{end}}</td>
    <td><details><summary>{{printf "%.120s" .Prompt}}</summary>
      <div class="stack">
        <div><b>Question</b><pre class="wrap">{{.Prompt}}</pre></div>
        {{if .Want}}<div><b>Expected</b><pre class="wrap">{{.Want}}</pre></div>{{end}}
        {{if .Answer}}<div><b>Answer</b><pre class="wrap">{{.Answer}}</pre></div>{{end}}
        {{if .Error}}<div class="del">{{.Error}}</div>{{end}}
        {{if .DurationMS}}<div class="muted">Took {{.DurationMS}} ms</div>{{end}}
      </div>
    </details></td>
  </tr>{{end}}
  </tbody></table>
  </div>
</details>
{{end}}
{{end}}
`
