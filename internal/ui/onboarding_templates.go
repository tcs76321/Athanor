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
      <div class="card"><h3>1 · Set up the machine</h3><p class="muted">Check the Core, Ollama, and the model pool. Pull anything you want with one click.</p></div>
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
    <p class="lede">This is the readiness summary: Podman, Git, Ollama, the model pool, memory, disk, state, the Job Pod image, context floors, network posture, and power. Fix anything red before you finish. Missing legacy persona models are a warning, not a failure; the pool is authoritative. No separate CLI command is required.</p>
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
    <p class="lede">Athanor adapts its own container footprint to the runtime and sizes each Job Pod to the task. Ollama runs outside this runtime on the host, so keep as much host memory free as possible: close heavy apps and avoid loading more than one model.</p>
    <div class="grid cols-3 tight">
      <div class="card stat"><div class="n">{{if .Resources.TotalMemoryGiB}}{{printf "%.0f" .Resources.TotalMemoryGiB}}{{else}}?{{end}}</div><div class="l">GiB runtime RAM</div></div>
      <div class="card stat"><div class="n">{{if .Resources.CPUs}}{{.Resources.CPUs}}{{else}}?{{end}}</div><div class="l">CPU cores</div></div>
      <div class="card stat"><div class="n">{{if .Resources.FreeDiskGiB}}{{printf "%.0f" .Resources.FreeDiskGiB}}{{else}}?{{end}}</div><div class="l">GiB free disk</div></div>
    </div>
    {{if eq .Applied "resources"}}<div class="notice">New Job Pod limits saved. The Core restarted itself and is using them now.</div>{{end}}
    <div class="notice">
      Host: {{printf "%.0f" .Resources.HostMemoryGiB}} GiB total, with about {{printf "%.0f" .Resources.OutsideRuntimeGiB}} GiB outside Athanor's runtime for macOS and Ollama. macOS cannot reserve host RAM for one process, so this is a budget, not a fence.
    </div>
    <div class="card">
      <div class="eyebrow">Sandbox limits</div>
      <p class="muted">These are the Job Pod's hard limits. Athanor derives useful defaults from runtime capacity, and you can adjust them. Applying a change restarts the Core automatically.</p>
      <form method="post" action="/ui/welcome">
        <input type="hidden" name="step" value="resources">
        <div class="grid cols-2">
          <label>Memory ceiling (MB)
            <input type="number" name="memory_mb" value="{{.Resources.MemoryMB}}" min="256" max="{{.Resources.MemoryMaxMB}}" step="256">
            <span class="field-help">Runtime memory {{printf "%.0f" .Resources.TotalMemoryGiB}} GiB minus a {{.Resources.ReserveMB}} MB reserve; max {{.Resources.MemoryMaxMB}} MB.</span>
          </label>
          <label>CPUs
            <input type="number" name="cpus" value="{{printf "%.2f" .Resources.CPUsAllocated}}" min="0.5" max="{{printf "%.2f" .Resources.CPUsMax}}" step="0.25">
            <span class="field-help">Runtime has {{.Resources.CPUs}} core(s).</span>
          </label>
          <label>/tmp size (MB)
            <input type="number" name="tmpfs_mb" value="{{.Resources.TmpfsMB}}" min="64" max="32768" step="64">
            <span class="field-help">Scratch space inside the Job Pod.</span>
          </label>
          <label>PID limit
            <input type="number" name="pids_limit" value="{{.Resources.PidsLimit}}" min="32" max="8192" step="32">
            <span class="field-help">Bounds process fan-out inside a Job Pod.</span>
          </label>
        </div>
        <div class="actions"><button class="primary" name="action" value="apply_resources">Apply and restart Core</button></div>
      </form>
    </div>
    <div class="actions">
      <form method="post" action="/ui/welcome"><input type="hidden" name="step" value="resources"><button class="secondary" name="action" value="back">Back</button></form>
      <form method="post" action="/ui/welcome"><input type="hidden" name="step" value="resources"><button class="primary" name="action" value="next">Continue</button></form>
    </div>
    {{else if eq .Step "models"}}
    <div class="eyebrow">Step {{.StepNumber}} of {{.StepCount}}</div>
    <h1>Build the model pool.</h1>
    <p class="lede">Pull the models you want Athanor to consider. You control membership; Athanor controls the ranked roster and the cast. Four or more models enable the full deliberation repertoire. Nothing is downloaded without an explicit pull.</p>
    {{if .ModelsErr}}<div class="notice bad">{{.ModelsErr}}</div>{{end}}
    {{if .Budget.HostGiB}}
    <div class="card {{if .Budget.Overcommit}}card-accent{{end}}">
      <div class="row row-middle">
        <div><div class="eyebrow">Host inference budget</div><p class="muted">{{if .Budget.Model}}Largest configured persona <b>{{.Budget.Model}}</b> uses {{printf "%.1f" .Budget.ModelGiB}} GiB{{if .Budget.ModelMeasured}} measured{{else}} estimated from file size{{end}}. Host {{printf "%.1f" .Budget.HostGiB}} GiB − {{printf "%.1f" .Budget.ReserveGiB}} GiB OS/apps reserve{{if .Budget.VmGiB}} − {{printf "%.1f" .Budget.VmGiB}} GiB Podman runtime{{end}} = {{printf "%.1f" .Budget.InferenceGiB}} GiB for inference.{{else}}No configured persona model found.{{end}}</p></div>
        {{if .Budget.Model}}{{if .Budget.ModelFits}}<span class="pill ok">model fits budget</span>{{else}}<span class="pill bad">model exceeds budget</span>{{end}}{{end}}
      </div>
      <p class="muted small">Installed-but-unused models do not count. The measured audit uses the same host reserve; {{if .Budget.ModelMeasured}}this size comes from the audit{{else}}run <code>athanor models -audit</code> for resident size{{end}}.</p>
      {{if .Budget.Overcommit}}<p class="del">Combined host pressure is {{printf "%.1f" .Budget.OverBy}} GiB over: the model, Podman runtime, and OS reserve together exceed host RAM. Reduce the runtime, lower context, or choose smaller persona models.</p>{{end}}
    </div>
    <div class="card {{if not .Budget.RuntimeFits}}card-accent{{end}}">
      <div class="row row-middle">
        <div><div class="eyebrow">Runtime sandbox</div><p class="muted">Job Pod ceiling {{printf "%.1f" .Budget.PodGiB}} GiB + {{printf "%.1f" .Budget.RuntimeReserve}} GiB runtime reserve vs {{printf "%.1f" .Budget.RuntimeGiB}} GiB runtime RAM. Headroom {{printf "%.1f" .Budget.RuntimeHeadroom}} GiB.</p></div>
        {{if .Budget.RuntimeFits}}<span class="pill ok">sandbox fits</span>{{else}}<span class="pill bad">sandbox exceeds runtime</span>{{end}}
      </div>
      <p class="muted small">Job Pod memory is inside the Podman runtime, not an additional host model cost. Per-job limits scale from this ceiling by task capability.</p>
    </div>
    {{end}}

    <h2>The pool</h2>
    <div class="card {{if not .PoolReady}}card-accent{{end}}">
      <div class="row row-middle">
        <div>
          <div class="eyebrow">{{.PoolCount}} model(s) in the pool</div>
          <p class="muted">{{if .PoolReady}}The full repertoire is available: heterogeneous candidates, an independent judge when a different family exists, and second opinions within budget.{{else}}Athanor runs in a degraded mode until four or more models are in the pool. It still works; it has fewer independent perspectives to draw on.{{end}}</p>
        </div>
        <span class="pill {{if .PoolReady}}ok{{else}}warn{{end}}">{{if .PoolReady}}full repertoire{{else}}degraded{{end}}</span>
        <span class="pill">machine {{.Pool.MachineScore}}/100</span>
      </div>
      {{if .PoolWarning}}<p class="del">{{.PoolWarning}}</p>{{end}}
      <p class="muted small">A minimum of {{.PoolMin}} is recommended, with a sanity cap of 100. Models do not need different sizes or families; ties are broken by name, and diversity is measured rather than assumed.</p>
    </div>

    <h2>Installed models</h2>
    {{if .PoolRows}}
    <div class="card flush"><div class="table-wrap">
      <table><thead><tr><th>Model</th><th>Size</th><th>Family</th><th>Context</th><th>Status</th><th></th></tr></thead><tbody>
      {{range .PoolRows}}<tr>
        <td class="mono">{{.Name}}</td>
        <td>{{gib .SizeBytes}}</td>
        <td>{{if .Family}}{{.Family}}{{else}}—{{end}}</td>
        <td>{{if .ContextTarget}}{{.ContextTarget}}{{else}}—{{end}}</td>
        <td>{{if .TooLarge}}<span class="pill bad">too large for this machine</span>{{else if .InPool}}<span class="pill ok">{{if .Qualified}}qualified {{printf "%.0f%%" (mul .Score 100)}}{{else}}in pool{{end}}</span>{{else}}<span class="pill">removed</span>{{end}}</td>
        <td>
          {{if .InPool}}
          <form method="post" action="/ui/welcome" class="inline-form">
            <input type="hidden" name="step" value="models">
            <input type="hidden" name="action" value="pool_remove">
            <input type="hidden" name="model" value="{{.Name}}">
            <button class="small secondary">Remove</button>
          </form>
          {{else}}
          <form method="post" action="/ui/welcome" class="inline-form">
            <input type="hidden" name="step" value="models">
            <input type="hidden" name="action" value="pool_add">
            <input type="hidden" name="model" value="{{.Name}}">
            <button class="small secondary">Add</button>
          </form>
          {{end}}
        </td>
      </tr>{{end}}
      </tbody></table>
    </div></div>
    {{else}}<div class="empty">No models are installed. Pull one below, or run <code>ollama pull &lt;model&gt;</code> on the host.</div>{{end}}

    {{if .Suggestions}}
    <h2>Core set for this machine</h2>
    <div class="card">
      <p class="muted">Athanor suggests at most four models for a machine this size. Nothing is required: pull only what you want, and you can remove any model from the pool later.</p>
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

    <h2>Rank the pool</h2>
    <p class="muted">{{if .HasMeasurements}}The ranking uses measured results from this computer. You can re-run it on the next step; removing a model here removes it from future casts.{{else}}The next step measures each pool model on a small, grounded Athanor-shaped screen. The ranking is a prior; real job outcomes update it over time.{{end}}</p>
    <div class="actions">
      <form method="post" action="/ui/welcome"><input type="hidden" name="step" value="models"><button class="secondary" name="action" value="back">Back</button></form>
      <form method="post" action="/ui/welcome"><input type="hidden" name="step" value="models"><button class="primary" name="action" value="next">Continue to qualification</button></form>
    </div>
    {{else if eq .Step "qualify"}}
    <div class="eyebrow">Step {{.StepNumber}} of {{.StepCount}}</div>
    <h1>Rank the pool.</h1>
    <p class="lede">Athanor measures every pool model on a small, grounded screen: deterministic generator cases and fixed-label judgment pairs. It is a prior for selection, not the frozen capability benchmark, and it never leaves this machine. Real job outcomes update the ranking over time.</p>
    {{if .Qualification.Running}}
    <div class="card" data-auto-refresh="2000">
      <div class="row row-middle"><h3>Running qualification</h3><span class="pill run">{{.Qualification.Percent}}%</span></div>
      <progress class="progress" value="{{.Qualification.Percent}}" max="100">{{.Qualification.Percent}}%</progress>
      <p><b>{{.Qualification.Done}} / {{.Qualification.Total}}</b> cases complete{{if .Qualification.Elapsed}} · {{.Qualification.Elapsed}} elapsed{{end}}{{if .Qualification.Remaining}} · about {{.Qualification.Remaining}} remaining{{else if not .Qualification.Done}} · estimating remaining time…{{end}}</p>
      <p class="muted">This page refreshes every 2 seconds. You can leave it; the run is resumable and the result will be here when you return.</p>
    </div>
    {{else}}
    <div class="grid cols-2">
      <div class="card"><h3>Why run it</h3><p class="muted">It ranks the models on this computer for the work you actually do, so Athanor can cast each job instead of guessing from model size or reputation.</p></div>
      <div class="card"><h3>What it costs</h3><p class="muted">Smoke is roughly 14 calls per model; deep is roughly 24 and adds long-context, schema, code repair, injection, and data-integrity cases. Both are bounded and resumable. Deep takes longer and is more accurate.</p></div>
    </div>
    <div class="card">
      {{if .Qualification.Error}}<p class="del">{{.Qualification.Error}}</p>{{end}}
      {{if .Qualification.Stale}}<p class="muted">A previous run looks stale; starting again resets it.</p>{{end}}
      {{if .Qualification.Complete}}<p class="ok"><b>Qualification complete.</b> {{.Qualification.Done}} cases recorded. The ranking below feeds the next cast; remove any model you do not want Athanor to use.</p>{{else}}<p class="muted">Run the screen here, or continue and use the measured roster from a previous run. The run stays inside this setup step.</p>{{end}}
      <form method="post" action="/ui/welcome">
        <input type="hidden" name="step" value="qualify">
        <button class="primary" name="action" value="run_qualify">{{if .Qualification.Complete}}Run smoke again{{else}}Run smoke qualification{{end}}</button>
        <button class="secondary" name="action" value="run_qualify_deep">Run deep qualification</button>
      </form>
    </div>
    {{end}}
    {{if .Pool.QualifiedCount}}
    <h2>Measured ranking</h2>
    <div class="card flush"><div class="table-wrap">
      <table><thead><tr><th>Model</th><th>Overall</th><th>Code</th><th>Data</th><th>Judgment</th><th>Samples</th><th></th></tr></thead><tbody>
      {{range .PoolRanked}}<tr>
        <td class="mono">{{.Name}}</td>
        <td><span class="pill {{if ge .Score 0.75}}ok{{else if ge .Score 0.5}}warn{{else}}bad{{end}}">{{printf "%.0f%%" (mul .Score 100)}}</span></td>
        <td>{{if .Code}}{{printf "%.0f%%" (mul .Code 100)}}{{else}}—{{end}}</td>
        <td>{{if .Data}}{{printf "%.0f%%" (mul .Data 100)}}{{else}}—{{end}}</td>
        <td>{{if .Judgment}}{{printf "%.0f%%" (mul .Judgment 100)}}{{else}}—{{end}}</td>
        <td>{{.Samples}}</td>
        <td>
          <form method="post" action="/ui/welcome" class="inline-form">
            <input type="hidden" name="step" value="qualify">
            <input type="hidden" name="action" value="pool_remove">
            <input type="hidden" name="model" value="{{.Name}}">
            <button class="small secondary">Remove</button>
          </form>
        </td>
      </tr>{{end}}
      </tbody></table>
    </div></div>
    <p class="muted small">The score is a smoothed success rate on this machine; samples are the number of grounded cases behind it. Ties break by name. Removing a model is the only override.</p>
    {{end}}
    <div class="actions">
      <form method="post" action="/ui/welcome"><input type="hidden" name="step" value="qualify"><button class="secondary" name="action" value="back">Back</button></form>
      <form method="post" action="/ui/welcome"><input type="hidden" name="step" value="qualify"><button class="primary" name="action" value="next">{{if .Qualification.Running}}Continue while it runs{{else}}Continue{{end}}</button></form>
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
