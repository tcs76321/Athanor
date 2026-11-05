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
    <div class="eyebrow">Step 2 of 6</div>
    <h1>Environment check.</h1>
    <p class="lede">These are the things Athanor can verify from inside the Core. Host-level checks (Podman socket, Job Pod image, memory, disk) are reported by <code>athanor doctor</code>.</p>
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
    {{else if eq .Step "models"}}
    <div class="eyebrow">Step 3 of 6</div>
    <h1>Persona models.</h1>
    <p class="lede">Athanor assigns models to roles. A small model can handle ingestion; a stronger one handles hard reasoning and judgment. Missing models are shown here; pull them explicitly — Athanor never downloads gigabytes silently.</p>
    {{if .ModelsErr}}<div class="notice bad">{{.ModelsErr}}</div>{{end}}
    <div class="card flush">
      <div class="table-wrap">
        <table>
          <thead><tr><th>Role</th><th>Model</th><th>Family</th><th>Context</th><th>Installed</th><th></th></tr></thead>
          <tbody>
          {{range .Roles}}<tr>
            <td><b>{{.Role}}</b></td>
            <td class="mono">{{.Model}}</td>
            <td>{{if .Family}}{{.Family}}{{else}}—{{end}}</td>
            <td>{{.ContextTarget}}</td>
            <td>{{if .Installed}}<span class="pill ok">yes</span>{{else}}<span class="pill bad">missing</span>{{end}}</td>
            <td>{{if not .Installed}}
              <form method="post" action="/ui/welcome" class="inline-form">
                <input type="hidden" name="step" value="models">
                <input type="hidden" name="action" value="pull">
                <input type="hidden" name="model" value="{{.Model}}">
                <button class="small">Pull</button>
              </form>
            {{end}}</td>
          </tr>{{end}}
          </tbody>
        </table>
      </div>
    </div>
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
    <p class="muted">Need alternatives or a bounded qualification? Open <a href="/ui/setup">Setup</a>; this wizard can continue without it.</p>
    <div class="actions">
      <form method="post" action="/ui/welcome"><input type="hidden" name="step" value="models"><button class="secondary" name="action" value="back">Back</button></form>
      <form method="post" action="/ui/welcome"><input type="hidden" name="step" value="models"><button class="primary" name="action" value="next">Continue</button></form>
    </div>
    {{else if eq .Step "qualify"}}
    <div class="eyebrow">Step 4 of 6</div>
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
    <div class="eyebrow">Step 5 of 6</div>
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
    <div class="eyebrow">Step 6 of 6</div>
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
