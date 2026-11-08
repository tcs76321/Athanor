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
        <span><span class="t">{{$s.Label}}</span>
          <span class="d">{{$s.Note}}</span></span>
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
    {{if .HealthBlocked}}
    <div class="notice bad">
      <b>One or more checks failed.</b> Athanor can still open, but work that depends on a failed check will not run.
      Fix the red rows above, or continue explicitly and revisit this step from Setup.
    </div>
    {{end}}
    <div class="actions">
      <form method="post" action="/ui/welcome"><input type="hidden" name="step" value="environment"><button class="secondary" name="action" value="back">Back</button></form>
      <form method="post" action="/ui/welcome">
        <input type="hidden" name="step" value="environment">
        {{if .HealthBlocked}}<input type="hidden" name="ack" value="1"><button class="primary" name="action" value="next">Continue anyway</button>
        {{else}}<button class="primary" name="action" value="next">Continue</button>{{end}}
      </form>
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
    {{if .Hardware.Accelerator}}
    <div class="card">
      <div class="row row-middle">
        <div>
          <div class="eyebrow">Inference accelerator</div>
          <h3>{{if .Hardware.Accelerator.Name}}{{.Hardware.Accelerator.Name}}{{else}}No discrete GPU detected{{end}}</h3>
          <p class="muted">
            {{if eq .Hardware.Accelerator.Name ""}}Ollama will use the CPU. {{end}}
            {{if .Hardware.Accelerator.Unified}}
            Unified memory with the host; the host inference budget above applies. Ollama uses {{.Hardware.Accelerator.Backend}}.
            {{else}}
            {{printf "%.1f" (mib .Hardware.Accelerator.MemoryMiB)}} GiB VRAM{{if .Hardware.Accelerator.Devices}}, {{.Hardware.Accelerator.Devices}} device(s){{end}}; full-speed model budget about {{printf "%.1f" .Budget.GPUBudgetGiB}} GiB after display headroom. Models larger than that can offload to system RAM, but slower.
            {{end}}
          </p>
          {{if .Hardware.Accelerator.NPU}}<p class="muted small">NPU present: {{.Hardware.Accelerator.NPU}}. It is inventory only; the current Ollama backend does not schedule work onto it.</p>{{end}}
        </div>
        <span class="pill">{{if .Hardware.Accelerator.Backend}}{{.Hardware.Accelerator.Backend}}{{else}}accelerator{{end}}</span>
      </div>
    </div>
    {{end}}
    {{if .Resources.ReserveClamped}}
    <div class="notice bad">
      <b>Athanor is enforcing a safer host reserve.</b>
      Your config says {{printf "%.1f" (mib .Resources.HostReserveMB)}} GiB, but this machine's Podman VM is {{printf "%.1f" .Resources.TotalMemoryGiB}} GiB. Athanor is using <b>{{.Resources.EffectiveHostReserveMB}} MB</b> for the budget so the VM is never left uncovered.
      The recommended value is <b>{{.Resources.RecommendedReserveMB}} MB</b> (VM + 1 GiB). Save it to make the setting explicit.
      <form method="post" action="/ui/welcome" class="actions">
        <input type="hidden" name="step" value="resources">
        <input type="hidden" name="action" value="apply_resources">
        <input type="hidden" name="host_reserve_mb" value="{{.Resources.RecommendedReserveMB}}">
        <button class="primary" data-confirm="Raise the host reserve to {{.Resources.RecommendedReserveMB}} MB? The Core restarts; completed work and qualification results are kept.">Use {{.Resources.RecommendedReserveMB}} MB and restart</button>
      </form>
    </div>
    {{else if .Resources.ReserveBelowRecommended}}
    <div class="notice">
      <b>Host reserve is below the recommendation.</b>
      You set {{printf "%.1f" (mib .Resources.HostReserveMB)}} GiB; the recommended value is {{.Resources.RecommendedReserveMB}} MB (VM + 1 GiB). This is allowed — it leaves more room for models — but less headroom for macOS and the VM. The minimum Athanor will accept is {{.Resources.MinHostReserveMB}} MB.
    </div>
    {{end}}
    <div class="card">
      <div class="eyebrow">Sandbox limits</div>
      <p class="muted">These are the Job Pod's hard limits. Athanor derives useful defaults from runtime capacity, and you can adjust them. Applying a change restarts the Core automatically.</p>
      <form method="post" action="/ui/welcome">
        <input type="hidden" name="step" value="resources">
        <div class="grid cols-2">
          <label>Memory ceiling (MB)
            <input type="number" name="memory_mb" value="{{.Resources.MemoryMB}}" min="256" max="{{.Resources.MemoryMaxMB}}" step="64">
            <span class="field-help">Runtime memory {{printf "%.0f" .Resources.TotalMemoryGiB}} GiB minus a {{.Resources.ReserveMB}} MB reserve; max {{.Resources.MemoryMaxMB}} MB.</span>
          </label>
          <label>Host reserve (MB)
            <input type="number" name="host_reserve_mb" value="{{.Resources.EffectiveHostReserveMB}}" min="{{.Resources.MinHostReserveMB}}" max="65536" step="1">
            <span class="field-help">Memory left outside any model: OS, apps, the Core, and the Podman VM. This machine's minimum is {{.Resources.MinHostReserveMB}} MB (the VM size); {{.Resources.RecommendedReserveMB}} MB is recommended. Higher is safer; lower leaves more room for models.</span>
          </label>
          <label>CPUs
            <input type="number" name="cpus" value="{{printf "%.2f" .Resources.CPUsAllocated}}" min="0.5" max="{{printf "%.2f" .Resources.CPUsMax}}" step="0.25">
            <span class="field-help">Runtime has {{.Resources.CPUs}} core(s). A fraction is a CPU quota, not a dedicated core.</span>
          </label>
          <label>/tmp size (MB)
            <input type="number" name="tmpfs_mb" value="{{.Resources.TmpfsMB}}" min="64" max="32768" step="32">
            <span class="field-help">Scratch space inside the Job Pod.</span>
          </label>
          <label>PID limit
            <input type="number" name="pids_limit" value="{{.Resources.PidsLimit}}" min="32" max="8192" step="8">
            <span class="field-help">Bounds process fan-out. Default 128; raise it for test suites that fork many workers.</span>
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
        <div><div class="eyebrow">Inference budget</div><p class="muted">{{if .Budget.Model}}Largest configured persona <b>{{.Budget.Model}}</b> uses {{printf "%.1f" .Budget.ModelGiB}} GiB{{if .Budget.ModelMeasured}} measured{{else}} estimated from file size{{end}}. Host {{printf "%.1f" .Budget.HostGiB}} GiB − {{printf "%.1f" .Budget.ReserveGiB}} GiB host reserve (OS, apps, Core{{if .Budget.VmGiB}}, Podman VM {{printf "%.1f" .Budget.VmGiB}} GiB{{end}}) = {{printf "%.1f" .Budget.InferenceGiB}} GiB for inference.{{else}}No configured persona model found.{{end}}</p>
          {{if .Budget.AccelName}}<p class="muted small">{{.Budget.AccelName}}{{if .Budget.AccelBackend}} · {{.Budget.AccelBackend}}{{end}}{{if .Budget.AccelUnified}} · unified host memory; the host budget applies{{else}} · {{printf "%.1f" .Budget.VRAMGiB}} GiB VRAM, full-GPU budget {{printf "%.1f" .Budget.GPUBudgetGiB}} GiB{{end}}.</p>{{end}}
          {{if gt .Budget.SpillGiB 0.0}}<p class="del small">The selected model is {{printf "%.1f" .Budget.SpillGiB}} GiB over the full-GPU budget; it can run by offloading to system RAM, but slower.</p>{{end}}
        </div>
        {{if .Budget.Model}}{{if .Budget.ModelFits}}<span class="pill ok">model fits budget</span>{{else}}<span class="pill bad">model exceeds budget</span>{{end}}{{end}}
      </div>
      <p class="muted small">Installed-but-unused models do not count. The measured audit uses the same host reserve; {{if .Budget.ModelMeasured}}this size comes from the audit{{else}}run <code>athanor models -audit</code> for resident size{{end}}.</p>
      {{if .Budget.ReserveClamped}}
      <div class="notice bad">
        <b>Athanor is enforcing a safer host reserve.</b>
        Your config says {{printf "%.1f" .Budget.ConfiguredReserveGiB}} GiB, but the Podman VM is {{printf "%.1f" .Budget.VmGiB}} GiB. The budget is using <b>{{.Budget.RecommendedReserveMB}} MB</b> (VM + 1 GiB) so models cannot starve the VM.
        Save the recommended value now, or continue and fix it later on Resources.
        <div class="actions">
          <form method="post" action="/ui/welcome">
            <input type="hidden" name="step" value="models">
            <input type="hidden" name="action" value="apply_resources">
            <input type="hidden" name="host_reserve_mb" value="{{.Budget.RecommendedReserveMB}}">
            <button class="primary" data-confirm="Raise the host reserve to {{.Budget.RecommendedReserveMB}} MB? The Core restarts; completed qualification results are kept.">Use {{.Budget.RecommendedReserveMB}} MB and restart</button>
          </form>
          <a class="btn secondary" href="/ui/welcome?step=resources">Open Resources</a>
        </div>
      </div>
      {{else if .Budget.ReserveBelowRecommended}}
      <div class="notice">
        <b>Host reserve is below the recommendation.</b>
        You set {{printf "%.1f" .Budget.ConfiguredReserveGiB}} GiB; the recommended value is {{.Budget.RecommendedReserveMB}} MB (VM + 1 GiB). This is allowed, but it leaves less headroom for macOS and the VM.
        <a href="/ui/welcome?step=resources">Open Resources</a>
      </div>
      {{end}}
      {{if .Budget.Overcommit}}<p class="del">Combined host pressure is {{printf "%.1f" .Budget.OverBy}} GiB over: the model and host reserve together exceed host RAM. Reduce the reserve, lower context, or choose smaller models.</p>{{end}}
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
        <span class="pill" title="RAM/CPU/disk capacity heuristic, not model quality">host capacity {{.Pool.MachineScore}}/100</span>
      </div>
      {{if .PoolWarning}}<p class="del">{{.PoolWarning}}</p>{{end}}
  {{if .PoolRecommendation}}<p class="muted small">{{.PoolRecommendation}}</p>{{end}}
      <p class="muted small">A minimum of {{.PoolMin}} is recommended, with a sanity cap of 100. Models do not need different sizes or families; ties are broken by name, and diversity is measured rather than assumed.</p>
      {{if .Pool.FitLimitGiB}}<p class="muted small">Fit limit: {{printf "%.1f" .Pool.FitLimitGiB}} GiB — host RAM minus the configured host reserve (OS, apps, Core, and Podman VM). Each model's capacity is computed from its weights, measured or metadata-derived KV cost, and native context. A model is excluded only when even 8,192 tokens do not fit.</p>{{end}}
    </div>

    <h2>Installed models</h2>
    <p class="muted small">Context shows working / capacity. Working follows the wide/tall split: smaller models carry more context, larger models carry less. Capacity is the largest window the weights and KV budget allow. A model is excluded only when even 8,192 tokens do not fit.</p>
    {{if .PoolRows}}
    <div class="card flush"><div class="table-wrap">
      <table><thead><tr><th>Model</th><th>Size</th><th>Family</th><th>Context (work/max)</th><th>Status</th><th></th></tr></thead><tbody>
      {{range .PoolRows}}<tr>
        <td class="mono">{{.Name}}</td>
        <td>{{gib .SizeBytes}}{{if .ResidentBytes}}<div class="muted small">{{gib .ResidentBytes}} resident</div>{{end}}</td>
        <td>{{if .Family}}{{.Family}}{{else}}—{{end}}</td>
        <td>{{if .ContextTarget}}{{.ContextTarget}}{{else}}—{{end}}{{if .MaxContext}} / {{.MaxContext}}{{end}}</td>
        <td>{{if .TooLarge}}<span class="pill bad">does not fit at 8,192+ tokens</span>{{else if .InPool}}<span class="pill ok">{{if .Qualified}}qualified {{printf "%.0f%%" (mul .Score 100)}}{{else if .MaxContext}}fits at {{.MaxContext}} tokens{{else}}in pool{{end}}</span>{{else}}<span class="pill">removed</span>{{end}}</td>
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
    <div class="card" data-live-card="pulls" data-live-interval="5000">
      {{range .Pulls}}
      <div class="stack mb-3">
        <div class="row row-middle"><b class="mono">{{.Model}}</b><span class="pill {{if .Done}}ok{{else}}run{{end}}">{{.Status}}</span><span class="muted right">{{.Percent}}%</span>
          {{if not .Done}}<form method="post" action="/ui/welcome" class="inline-form">
            <input type="hidden" name="step" value="models">
            <input type="hidden" name="action" value="cancel_pull">
            <input type="hidden" name="model" value="{{.Model}}">
            <button class="small secondary" data-confirm="Cancel this model pull? A partial download is discarded by Ollama.">Cancel</button>
          </form>{{end}}
        </div>
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
    <div class="tier-picker">
      <div class="tier-picker-head"><b>How deep should the screen go?</b><span class="muted small">Choose now — a running screen switches without waiting for the minimum to finish.</span></div>
      <form method="post" action="/ui/welcome" class="tier-options">
        <input type="hidden" name="step" value="qualify">
        <input type="hidden" name="action" value="set_qual_tier">
        <button type="submit" name="tier" value="short" class="tier-option {{if eq .Qualification.Tier "short"}}selected{{end}}">
          <span class="tier-name">Short</span>
          <span class="tier-desc">Minimum screen: one grounded case per capability on every model.</span>
        </button>
        <button type="submit" name="tier" value="medium" class="tier-option {{if eq .Qualification.Tier "medium"}}selected{{end}}">
          <span class="tier-name">Medium</span>
          <span class="tier-desc">Minimum screen, then about ten minutes of extra cases.</span>
        </button>
        <button type="submit" name="tier" value="full" class="tier-option {{if eq .Qualification.Tier "full"}}selected{{end}}">
          <span class="tier-name">Full</span>
          <span class="tier-desc">Every case in the deep ladder{{if .Qualification.FullEstimate}} · about {{.Qualification.FullEstimate}} remaining{{end}}.</span>
        </button>
      </form>
    </div>
    {{if .Qualification.Running}}
    <div class="card" data-live-card="welcome-qualification" data-live-interval="2000">
      <div class="row row-middle"><h3>{{if eq .Qualification.Phase "core"}}Measuring the pool{{else}}Filling out the screen{{end}}</h3><span class="pill run">{{.Qualification.Percent}}%</span></div>
      <progress class="progress" value="{{.Qualification.Percent}}" max="100">{{.Qualification.Percent}}%</progress>
      <p><b>{{.Qualification.Done}} / {{.Qualification.Total}}</b> cases complete{{if .Qualification.CurrentModel}} · measuring <b class="mono">{{.Qualification.CurrentModel}}</b>{{end}}{{if .Qualification.Elapsed}} · {{.Qualification.Elapsed}} elapsed{{end}}{{if .Qualification.Remaining}} · about {{.Qualification.Remaining}} left{{else if .Qualification.CoreRemaining}} · about {{.Qualification.CoreRemaining}} left{{else if .Qualification.Estimate}} · about {{.Qualification.Estimate}} total{{else}} · estimating time left…{{end}}</p>
      <p class="muted">{{if eq .Qualification.Phase "core"}}The minimum screen runs one grounded case per capability on every model. It keeps running if you leave this page and resumes after a restart.{{else}}Every model runs the same extra cases, so the ranking stays comparable.{{end}}</p>
      <p class="muted small">Depth: {{if eq .Qualification.Tier "short"}}Short — the run stops after the minimum screen.{{else if eq .Qualification.Tier "medium"}}Medium — the minimum screen, then about ten minutes of extra cases.{{else}}Full — the minimum screen, then every remaining case.{{end}} Changing it above applies between cases.</p>
      <form method="post" action="/ui/setup/qualify/cancel">
        <input type="hidden" name="next" value="/ui/welcome?step=qualify">
        <button class="danger small" data-confirm="Stop this qualification run? Completed cases are kept and the run can resume later.">Stop measuring</button>
      </form>
    </div>
    {{else if .Qualification.CoreComplete}}
    <div class="card">
      <div class="grid cols-4">
        <div class="card stat"><div class="n">{{.PoolCount}}</div><div class="l">models in the pool</div></div>
        <div class="card stat"><div class="n">{{.Qualification.Done}}</div><div class="l">cases recorded</div></div>
        <div class="card stat"><div class="n">{{.Qualification.Total}}</div><div class="l">cases planned</div></div>
        <div class="card stat"><div class="n">{{if .Qualification.Estimate}}{{.Qualification.Estimate}}{{else}}—{{end}}</div><div class="l">core run time</div></div>
      </div>
      <p class="ok"><b>{{if eq .Qualification.Tier "short"}}Minimum screen complete.{{else if eq .Qualification.Tier "full"}}{{if .Qualification.AllCases}}All cases complete.{{else}}Full screen paused — choose Full above to continue.{{end}}{{else}}{{if .Qualification.AllCases}}All cases complete.{{else if .Qualification.Complete}}Medium pass complete.{{else}}Medium pass paused — choose Medium above to continue.{{end}}{{end}}</b> The ranking below is usable now.</p>
      {{if .Qualification.Error}}<p class="del">{{.Qualification.Error}}</p>{{end}}
      <p class="muted">Choose a deeper level above to keep going; completed cases are kept. Continue below to use the current ranking.</p>
      {{if .Qualification.FullCases}}<p class="muted small">{{.Qualification.FullCases}} cases remain for the full screen.</p>{{end}}
    </div>
    {{else}}
    <div class="card">
      {{if .Qualification.Error}}<p class="del">{{.Qualification.Error}}</p>{{end}}
      {{if .Qualification.Stale}}<p class="muted">A previous run looks stale; starting again picks up from the completed cases.</p>{{end}}
      <p class="muted">Choose a depth above to start. Completed cases are kept if the run is paused.</p>
    </div>
    {{end}}
    <p class="muted small">The screen covers code, data, documents, text, media, summarization, security, agentic decisions, instruction following, grounding, repair, long-context recall, engine-operation choice, planning, calibration, and judgment. Each family is scored separately.</p>

    {{if .Pool.QualifiedCount}}
    {{if .RolePicks}}
    <h2>Baseline cast</h2>
    <p class="muted">A first signal from this machine's screen: the best small, medium, and big models for the everyday roles. The selector keeps adapting from real job outcomes after this.</p>
    <div class="grid cols-3">
      {{range .RolePicks}}
      <div class="card">
        <div class="eyebrow">{{roleLabel .Role}} · {{.Tier}}</div>
        <h3 class="mono">{{.Model}}</h3>
        <p class="muted">{{printf "%.0f%%" (mul .Score 100)}} role score · {{printf "%.1f" .SizeGiB}} GiB · {{.Context}} ctx</p>
        <p class="muted small">{{.Reason}}</p>
      </div>
      {{end}}
    </div>
    {{end}}
    <h2>Measured ranking</h2>
    <div class="card flush"><div class="table-wrap">
      <table><thead><tr><th>Model</th><th>Family</th><th>Size</th><th>Overall</th><th>Code</th><th>Data</th><th>Judgment</th><th>Summ.</th><th>Sec.</th><th>Agent</th><th>Samples</th><th></th></tr></thead><tbody>
      {{range .PoolRanked}}<tr>
        <td class="mono">{{.Name}}</td>
        <td>{{.Family}}</td>
        <td class="mono">{{gib .SizeBytes}}</td>
        <td><span class="pill {{if ge .Score 0.75}}ok{{else if ge .Score 0.5}}warn{{else}}bad{{end}}">{{printf "%.0f%%" (mul .Score 100)}}</span></td>
        <td>{{if .Code}}{{printf "%.0f%%" (mul .Code 100)}}{{else}}—{{end}}</td>
        <td>{{if .Data}}{{printf "%.0f%%" (mul .Data 100)}}{{else}}—{{end}}</td>
        <td>{{if .Judgment}}{{printf "%.0f%%" (mul .Judgment 100)}}{{else}}—{{end}}</td>
        <td>{{if .Summarize}}{{printf "%.0f%%" (mul .Summarize 100)}}{{else}}—{{end}}</td>
        <td>{{if .Security}}{{printf "%.0f%%" (mul .Security 100)}}{{else}}—{{end}}</td>
        <td>{{if .Agentry}}{{printf "%.0f%%" (mul .Agentry 100)}}{{else}}—{{end}}</td>
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
    <h3>Internal task families</h3>
<div class="card flush"><div class="table-wrap">
<table><thead><tr><th>Model</th><th>Repair</th><th>Long ctx</th><th>Operation</th><th>Planning</th><th>Calibration</th></tr></thead><tbody>
{{range .PoolRanked}}<tr>
  <td class="mono">{{.Name}}</td>
  <td>{{if .Repair}}{{printf "%.0f%%" (mul .Repair 100)}}{{else}}—{{end}}</td>
  <td>{{if .LongContext}}{{printf "%.0f%%" (mul .LongContext 100)}}{{else}}—{{end}}</td>
  <td>{{if .Operation}}{{printf "%.0f%%" (mul .Operation 100)}}{{else}}—{{end}}</td>
  <td>{{if .Planning}}{{printf "%.0f%%" (mul .Planning 100)}}{{else}}—{{end}}</td>
  <td>{{if .Calibration}}{{printf "%.0f%%" (mul .Calibration 100)}}{{else}}—{{end}}</td>
</tr>{{end}}
</tbody></table>
</div></div>
{{if .QualDetails}}
    <h2>Case detail</h2>
    <p class="muted">Expand a model to see every case, the question it was asked, the expected answer, and what it produced.</p>
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
      <div class="card"><h3>Models</h3><p class="muted">{{if .PoolCount}}{{.PoolCount}} model(s) in the pool{{else}}No models in the pool yet — add one in Setup{{end}}</p></div>
    </div>
    <div class="actions">
      <form method="post" action="/ui/welcome"><input type="hidden" name="step" value="ready"><button class="primary" name="action" value="complete">Finish and create a project</button></form>
      <form method="post" action="/ui/welcome"><input type="hidden" name="step" value="ready"><button class="secondary" name="action" value="skip">Finish without a project</button></form>
    </div>
    {{end}}
  </section>
</div>
`
