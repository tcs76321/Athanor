package ui

const welcomeBody = `
<div class="wizard">
  <aside class="wizard-rail">
    <a class="brand" href="/ui">
      <span class="brand-name">Athanor<span class="ember">.</span></span>
      <span class="brand-tag">the alchemical LLM furnace</span>
    </a>
    <div class="stepper">
      {{range $i, $s := .Steps}}
      <a class="step {{if lt $i $.StepIndex}}done{{end}} {{if eq $s.ID $.Step}}current{{end}}" href="/ui/welcome?step={{$s.ID}}">
        <span class="num">{{add $i 1}}</span>
        <span><span class="t">{{$s.Label}}</span><span class="d">{{$s.Note}}</span></span>
      </a>
      {{end}}
    </div>
    <div class="spacer"></div>
    <form method="post" action="/ui/welcome">
      <input type="hidden" name="step" value="{{.Step}}">
      <button class="ghost small" name="action" value="skip">Skip setup for now</button>
    </form>
    <div class="rail-foot"><span>{{.Runtime}}</span><span>{{.Version}}</span></div>
  </aside>
  <section class="wizard-main">
    {{if eq .Step "welcome"}}
    <div class="eyebrow">First run · once per computer</div>
    <h1>Welcome to Athanor.</h1>
    <p class="lede">A local-first workshop for goals that need more than one answer. Athanor turns a goal into a plan, explores options, runs work in isolated Job Pods, verifies what it produces, and asks you before anything external or irreversible happens.</p>
    <div class="grid cols-3">
      <div class="card"><h3>1 · Set up the machine</h3><p class="muted">Check the Core, Ollama, and persona models. Pull anything missing with one click.</p></div>
      <div class="card"><h3>2 · Create a project</h3><p class="muted">A goal, the limits, and the sources you allow. Athanor asks first, then explores.</p></div>
      <div class="card"><h3>3 · Review the work</h3><p class="muted">Read the result, rate it, and decide what happens next. Nothing leaves your machine by default.</p></div>
    </div>
    <form method="post" action="/ui/welcome" class="actions">
      <input type="hidden" name="step" value="welcome">
      <button class="primary" name="action" value="next">Start setup</button>
    </form>
    {{else if eq .Step "environment"}}
    <div class="eyebrow">Step {{.StepNumber}} of {{.StepCount}}</div>
    <h1>Final environment check.</h1>
    <p class="lede">This is the readiness summary: Podman, Git, Ollama, persona models, memory, disk, state, the Job Pod image, context floors, network posture, and power. Fix anything red before you finish. No separate CLI command is required.</p>
    <div class="actions"><a class="btn secondary small" href="/ui/welcome?step=environment">Run checks again</a></div>
    <div class="card flush">
      <div class="table-wrap">
        <table>
          <thead><tr><th>Check</th><th>Status</th><th>Detail</th></tr></thead>
          <tbody>
          {{range .Health}}<tr>
            <td><b>{{.Name}}</b>{{if .Fix}}<div class="field-help">{{.Fix}}</div>{{end}}</td>
            <td><span class="pill {{if eq .Status "ok"}}ok{{else if eq .Status "warn"}}warn{{else}}bad{{end}}">{{.Status}}</span></td>
            <td class="muted">{{.Detail}}</td>
          </tr>{{end}}
          </tbody>
        </table>
      </div>
    </div>
    <div class="actions">
      <form method="post" action="/ui/welcome"><input type="hidden" name="step" value="environment"><button class="secondary" name="action" value="back">Back</button></form>
      <form method="post" action="/ui/welcome"><input type="hidden" name="step" value="environment"><button class="primary" name="action" value="next">Continue</button></form>
    </div>
    {{else if eq .Step "resources"}}
    <div class="eyebrow">Step {{.StepNumber}} of {{.StepCount}}</div>
    <h1>Resources.</h1>
    <p class="lede">Athanor detected the host capacity below. The Job Pod limits are a sandbox budget, not the model budget: Ollama runs on the host, while each ephemeral pod gets its own memory, CPU, /tmp, and PID cap. The recommended values are prefilled; verify or change them.</p>
    <div class="grid cols-3 tight">
      <div class="card stat"><div class="n">{{if .Resources.TotalMemoryGiB}}{{printf "%.0f" .Resources.TotalMemoryGiB}}{{else}}?{{end}}</div><div class="l">GiB host RAM</div></div>
      <div class="card stat"><div class="n">{{if .Resources.CPUs}}{{.Resources.CPUs}}{{else}}?{{end}}</div><div class="l">CPU cores</div></div>
      <div class="card stat"><div class="n">{{if .Resources.FreeDiskGiB}}{{printf "%.0f" .Resources.FreeDiskGiB}}{{else}}?{{end}}</div><div class="l">GiB free disk</div></div>
    </div>
    {{if eq .Applied "resources"}}<div class="notice">Resource limits saved to <code>{{.ConfigPath}}</code>. Restart the Core (or the host process) to apply them.</div>{{end}}
    <div class="card">
      <form method="post" action="/ui/welcome">
        <input type="hidden" name="step" value="resources">
        <label for="memory_mb">Job Pod memory (MB)</label>
        <input id="memory_mb" type="number" name="memory_mb" min="256" max="{{.Resources.MemoryMaxMB}}" step="256" value="{{.Resources.MemoryMB}}" required>
        <div class="field-help">Host RAM minus a {{.Resources.ReserveMB}} MB reserve is the ceiling ({{.Resources.MemoryMaxMB}} MB).</div>
        <div class="row">
          <label>CPUs
            <input type="number" name="cpus" min="0.25" max="{{.Resources.CPUsMax}}" step="0.25" value="{{printf "%.2f" .Resources.CPUsAllocated}}" required>
          </label>
          <label>/tmp size (MB)
            <input type="number" name="tmpfs_mb" min="64" max="32768" step="64" value="{{.Resources.TmpfsMB}}" required>
          </label>
          <label>PID limit
            <input type="number" name="pids_limit" min="32" max="8192" step="32" value="{{.Resources.PidsLimit}}" required>
          </label>
        </div>
        <p class="muted">The Core is single-slot: one job runs at a time, so these limits bound the active sandbox rather than a pool. A larger Job Pod helps test suites and build tools, not the language model.</p>
        <div class="actions"><button class="primary" name="action" value="apply_resources">Save resource limits</button></div>
      </form>
    </div>
    <div class="actions">
      <form method="post" action="/ui/welcome"><input type="hidden" name="step" value="resources"><button class="secondary" name="action" value="back">Back</button></form>
      <form method="post" action="/ui/welcome"><input type="hidden" name="step" value="resources"><button class="primary" name="action" value="next">Continue</button></form>
    </div>
    {{else if eq .Step "models"}}
    <div class="eyebrow">Step {{.StepNumber}} of {{.StepCount}}</div>
    <h1>Personas and models.</h1>
    <p class="lede">Personas are functional roles, not fixed models. Athanor selects one installed model for each role based on size and family, and you can review or replace the assignment. Models are never downloaded without an explicit pull.</p>
    {{if .ModelsErr}}<div class="notice bad">{{.ModelsErr}}</div>{{end}}
    {{if .Budget.TotalGiB}}
    <div class="card {{if .Budget.Overcommit}}card-accent{{end}}">
      <div class="row row-middle">
        <div><div class="eyebrow">Memory budget</div><p class="muted">Largest installed model {{printf "%.1f" .Budget.LargestGiB}} GiB + Job Pod {{printf "%.1f" .Budget.PodGiB}} GiB + {{printf "%.1f" .Budget.ReserveGiB}} GiB reserve vs {{printf "%.1f" .Budget.TotalGiB}} GiB host RAM.</p></div>
        {{if .Budget.Overcommit}}<span class="pill bad">overcommitted</span>{{else}}<span class="pill ok">fits</span>{{end}}
      </div>
      {{if .Budget.Overcommit}}<p class="del">This combination can thrash the host. Reduce the Job Pod memory in Resources or choose smaller models.</p>{{end}}
    </div>
    {{end}}

    <h2>What the roles mean</h2>
    <div class="grid cols-3">
      {{range .PersonaGuides}}
      <div class="card"><div class="eyebrow">{{.Role}}</div><h3>{{.Title}}</h3><p class="muted">{{.Purpose}}</p></div>
      {{end}}
    </div>

    <h2>Installed models</h2>
    {{if .Models}}
    <div class="card flush"><div class="table-wrap">
      <table><thead><tr><th>Model</th><th>Size</th><th>Family</th></tr></thead><tbody>
      {{range .Models}}<tr><td class="mono">{{.Name}}</td><td>{{gib .SizeBytes}}</td><td>{{if .Family}}{{.Family}}{{else}}—{{end}}</td></tr>{{end}}
      </tbody></table>
    </div></div>
    {{else}}<div class="empty">No models are installed. Pull one below, or run <code>ollama pull &lt;model&gt;</code> on the host.</div>{{end}}

    {{if .Suggestions}}
    <h2>Suggested pulls</h2>
    <div class="card">
      <p class="muted">This configuration expects these models and they are not installed. Sizes are shown by Ollama before the pull; nothing starts automatically.</p>
      <div class="stack">
        {{range .Suggestions}}
        <div class="row row-middle">
          <span class="mono">{{.Model}}</span>
          <span class="muted">{{.Reason}}</span>
          <form method="post" action="/ui/welcome" class="inline-form">
            <input type="hidden" name="step" value="models">
            <input type="hidden" name="action" value="pull">
            <input type="hidden" name="model" value="{{.Model}}">
            <button class="small secondary">Pull</button>
          </form>
        </div>
        {{end}}
      </div>
    </div>
    {{end}}

    {{if .Pulls}}
    <h2>Pulls in progress</h2>
    <div class="card" data-auto-refresh="5000">
      {{range .Pulls}}
      <div class="stack mb-3">
        <div class="row row-middle"><b class="mono">{{.Model}}</b><span class="pill {{if .Done}}ok{{else}}run{{end}}">{{.Status}}</span><span class="muted right">{{.Percent}}%</span></div>
        <progress class="progress" value="{{.Percent}}" max="100">{{.Percent}}%</progress>
        {{if .Error}}<div class="del">{{.Error}}</div>{{end}}
      </div>
      {{end}}
    </div>
    {{end}}

    <h2>Athanor's selection</h2>
    <p class="muted">{{if .HasMeasurements}}The selection uses your stored qualification measurements where available, then falls back to size.{{else}}The selection is size- and family-based. Run the bounded qualification in <a href="/ui/setup">Setup</a> to rank models by measured performance.{{end}}</p>
    {{if .Assignments}}
    {{if eq .Applied "personas"}}<div class="notice">Persona assignment saved to <code>{{.ConfigPath}}</code>. Restart the Core (or the host process) to load it; the running registry keeps the current models until then.</div>{{end}}
    <div class="card flush"><div class="table-wrap">
      <table><thead><tr><th>Role</th><th>Selected model</th><th>Context</th><th>Family</th><th>Why</th></tr></thead><tbody>
      {{range .Assignments}}<tr>
        <td><b>{{.Role}}</b></td>
        <td class="mono">{{.Model}}</td>
        <td>{{if .ContextTarget}}{{.ContextTarget}}{{else}}—{{end}}</td>
        <td>{{if .Family}}{{.Family}}{{else}}—{{end}}</td>
        <td class="muted">{{.Reason}}</td>
      </tr>{{end}}
      </tbody></table>
    </div></div>
    <div class="actions">
      <form method="post" action="/ui/welcome">
        <input type="hidden" name="step" value="models">
        <button class="primary" name="action" value="apply_personas">Use this assignment</button>
      </form>
      <span class="muted small">Writes <code>personas.*</code> to config.yaml with a backup. Restart required.</span>
    </div>
    {{else}}<div class="empty">Install at least one model and Athanor will propose an assignment.</div>{{end}}

    <p class="muted">Need a manual override or the bounded qualification? Open <a href="/ui/setup">Setup</a>; this wizard can continue without applying the assignment.</p>
    <div class="actions">
      <form method="post" action="/ui/welcome"><input type="hidden" name="step" value="models"><button class="secondary" name="action" value="back">Back</button></form>
      <form method="post" action="/ui/welcome"><input type="hidden" name="step" value="models"><button class="primary" name="action" value="next">Continue</button></form>
    </div>
    {{else if eq .Step "qualify"}}
    <div class="eyebrow">Step {{.StepNumber}} of {{.StepCount}}</div>
    <h1>Qualification is optional.</h1>
    <p class="lede">The bounded screen runs a few grounded generator cases and fixed-label judge pairs against the models installed on this machine. It is a local routing check, not the frozen benchmark. You can skip it and use the configured personas.</p>
    <div class="grid cols-2">
      <div class="card"><h3>Why run it</h3><p class="muted">It can propose a better persona routing for this hardware, and it records the evidence for the proposal.</p><div class="actions"><a class="btn" href="/ui/setup">Open setup and qualification</a></div></div>
      <div class="card"><h3>What it costs</h3><p class="muted">A few model calls and some minutes, bounded and resumable. Nothing leaves the machine.</p></div>
    </div>
    <div class="actions">
      <form method="post" action="/ui/welcome"><input type="hidden" name="step" value="qualify"><button class="secondary" name="action" value="back">Back</button></form>
      <form method="post" action="/ui/welcome"><input type="hidden" name="step" value="qualify"><button class="primary" name="action" value="next">Continue</button></form>
    </div>
    {{else if eq .Step "preferences"}}
    <div class="eyebrow">Step {{.StepNumber}} of {{.StepCount}}</div>
    <h1>Make it yours.</h1>
    <p class="lede">Athanor is a local workshop; these preferences stay on this Core. You can change them later in Settings.</p>
    <div class="card">
      <form method="post" action="/ui/theme" data-theme-form>
        <input type="hidden" name="next" value="/ui/welcome?step=preferences">
        <label for="theme">Theme</label>
        <select id="theme" name="theme">
          {{range .Themes}}<option value="{{.ID}}" {{if eq .ID $.Theme}}selected{{end}}>{{.Label}}</option>{{end}}
        </select>
        <div class="field-help">Monokai is the default; Ember Paper is warm and light.</div>
        <div class="actions"><button class="primary">Apply theme</button></div>
      </form>
    </div>
    <div class="card">
      <h3>Notifications</h3>
      <p class="muted">Desktop notifications are opt-in and local. Enable them from the <a href="/ui/notifications">Inbox</a> when you are ready.</p>
    </div>
    <div class="actions">
      <form method="post" action="/ui/welcome"><input type="hidden" name="step" value="preferences"><button class="secondary" name="action" value="back">Back</button></form>
      <form method="post" action="/ui/welcome"><input type="hidden" name="step" value="preferences"><button class="primary" name="action" value="next">Continue</button></form>
    </div>
    {{else}}
    <div class="eyebrow">Step {{.StepNumber}} of {{.StepCount}}</div>
    <h1>Ready to make something.</h1>
    <p class="lede">The Core is set up. Create a project with a goal; Athanor will ask clarifying questions, propose directions if the goal is exploratory, and wait for your approval before work starts.</p>
    <div class="grid cols-3">
      <div class="card"><h3>Core</h3><p class="muted">{{.Runtime}} · {{.Version}}</p></div>
      <div class="card"><h3>Ollama</h3><p class="muted">{{if .OllamaURL}}<code>{{.OllamaURL}}</code>{{else}}not configured{{end}}</p></div>
      <div class="card"><h3>Models</h3><p class="muted">{{len .Roles}} personas configured</p></div>
    </div>
    <div class="actions">
      <form method="post" action="/ui/welcome"><input type="hidden" name="step" value="ready"><button class="primary" name="action" value="complete">Finish and create a project</button></form>
      <form method="post" action="/ui/welcome"><input type="hidden" name="step" value="ready"><button class="secondary" name="action" value="skip">Finish without a project</button></form>
    </div>
    {{end}}
  </section>
</div>
`
