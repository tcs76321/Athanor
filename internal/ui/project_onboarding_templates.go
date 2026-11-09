package ui

const projectOnboardingBody = `
<h1>{{.Project.Name}} <span class="pill">{{.Project.OnRamp}}</span></h1>
<p class="sub">{{.Project.Goal}}</p>
<div class="actions">
  <a class="btn secondary" href="/ui/projects/{{.Project.ID}}">Back to project</a>
</div>
{{if .Requested}}<div class="notice">Access request queued in the Inbox. The host stays unused until the allowlist is updated in config.yaml.</div>{{end}}

{{if eq .Stage "intent"}}
  <div class="grid onboard-grid">
    <div class="card">
      <div class="eyebrow">Project intake</div>
      <div class="stepper">
        <div class="step current"><span class="num">1</span><span><span class="t">Goal</span><span class="d">One sentence, plus optional limits</span></span></div>
        {{if eq .Project.OnRamp "explore"}}
        <div class="step"><span class="num">2</span><span><span class="t">Directions</span><span class="d">Choose one approach</span></span></div>
        {{end}}
        <div class="step"><span class="num">{{if eq .Project.OnRamp "explore"}}3{{else}}2{{end}}</span><span><span class="t">Plan</span><span class="d">Approve and start</span></span></div>
      </div>
      <p class="muted">{{if eq .Project.OnRamp "explore"}}This goal is ambiguous enough to explore. Athanor will propose a small portfolio of directions; you choose one.{{else}}This goal is precise enough to plan directly. Athanor will draft one plan for you to approve.{{end}}</p>
      <form method="post" action="/ui/projects/{{.Project.ID}}/onboarding">
        <label for="goal">Goal</label>
        <textarea id="goal" name="goal" rows="3" required data-refine-field="project.goal">{{.Project.Goal}}</textarea>
        <div class="field-help">One sentence is enough. Refine it if you want a sharper outcome.</div>

        <details {{if or .Project.Boundaries .Project.Sources .Project.RepositoryPath}}open{{end}}>
          <summary>Add limits, sources, or a repository (optional)</summary>
          <label for="name">Name <input id="name" name="name" value="{{.Project.Name}}" placeholder="derived from the goal"></label>
          <label for="repository_path">Repository path (optional) <input id="repository_path" name="repository_path" value="{{.Project.RepositoryPath}}" placeholder="/absolute/path/to/repo"></label>
          <div class="field-help">Core Pod: the repository must be visible inside the Core. The installer mounts <code>~/athanor/repos</code> at the same host path.</div>
          <label for="boundaries">Limits (optional, one per line) <textarea id="boundaries" name="boundaries" rows="3" data-refine-field="project.boundaries" placeholder="No external publishing without review, no outbound messages, no new services.">{{.Project.Boundaries}}</textarea></label>
          <label for="sources">Sources Athanor may read (optional, one URL per line) <textarea id="sources" name="sources" rows="2" placeholder="https://example.com/feed">{{join .Project.Sources}}</textarea></label>
          {{if .Sources}}
          <div class="stack">
            {{range .Sources}}
            <div class="row row-middle">
              <span class="mono wrap">{{.URL}}</span>
              {{if .Allowed}}<span class="pill ok">allowed</span>{{else}}<span class="pill warn">needs access</span>{{end}}
              {{if not .Allowed}}
              <form method="post" action="/ui/projects/{{$.Project.ID}}/onboarding" class="inline-form">
                <input type="hidden" name="action" value="request_access">
                <input type="hidden" name="source" value="{{.URL}}">
                <button class="small secondary">Request access</button>
              </form>
              {{end}}
            </div>
            {{end}}
          </div>
          {{end}}
        </details>

        <label for="onramp">Path</label>
        <select id="onramp" name="onramp">
          <option value="" {{if eq .Project.OnRamp ""}}selected{{end}}>Infer from the goal ({{.Project.OnRamp}})</option>
          <option value="direct" {{if eq .Project.OnRamp "direct"}}selected{{end}}>Direct plan — I know what to build</option>
          <option value="explore" {{if eq .Project.OnRamp "explore"}}selected{{end}}>Explore directions — help me choose</option>
        </select>

        <div class="actions">
          <button name="action" value="save_intent" class="secondary">Save</button>
          {{if eq .Project.OnRamp "explore"}}
          <button name="action" value="discover" class="primary">Generate directions</button>
          {{else}}
          <button name="action" value="save_and_plan" class="primary">Draft the plan</button>
          {{end}}
        </div>
      </form>
      {{if .State.DirectionError}}<div class="notice bad">{{.State.DirectionError}}</div>{{end}}
      {{if .State.PlanError}}<div class="notice bad">{{.State.PlanError}}</div>{{end}}
    </div>
  </div>
{{else if eq .Stage "directions"}}
  <div class="card" data-live-card="intake" data-live-interval="2000">
    <div class="eyebrow">Directions in progress</div>
    <h2>Athanor is exploring directions inside your goal.</h2>
    <p class="muted">One bounded model pass. The result is saved when it finishes.</p>
    {{with .State.DirectionRun}}
    <dl class="kv">
      <dt>stage</dt><dd><code>{{.Stage}}</code> — {{.Detail}}</dd>
      <dt>model</dt><dd><code>{{.Model}}</code></dd>
      <dt>response</dt><dd>{{.ResponseBytes}} chars received{{if .Options}} · {{.Options}} options parsed{{end}}</dd>
    </dl>
    {{if .Tail}}<details><summary>Raw model output (last 1200 chars, display only)</summary><pre class="wrap">{{.Tail}}</pre></details>{{end}}
    {{if .Steps}}<details open><summary>What Athanor is doing</summary><ol class="stack">{{range .Steps}}<li><span class="mono">{{ts .At}}</span> <b>{{.Stage}}</b> — {{.Detail}}</li>{{end}}</ol></details>{{end}}
    {{else}}
    <p class="muted">This pass was started before a Core restart, so live progress is unavailable. Its durable status is <b>{{.State.Directions.Status}}</b>.</p>
    {{end}}
    <div class="actions">
      <a class="btn secondary" href="/ui/projects/{{.Project.ID}}/onboarding">Refresh</a>
      <form method="post" action="/ui/projects/{{.Project.ID}}/onboarding">
        <button class="danger" name="action" value="cancel_discovery">Cancel</button>
      </form>
    </div>
  </div>
{{else if eq .Stage "choose"}}
  <div class="card">
    <h2>{{len .State.Directions.Options}} directions</h2>
    <p class="muted">Each direction is a different outcome, method, and check. Read the three lines and choose the one you want to develop.</p>
  </div>
  {{if .State.DirectionError}}<div class="notice bad">{{.State.DirectionError}}</div>{{end}}
  <div class="grid cols-2">
    {{range .State.Directions.Options}}
    <div class="option">
      <div class="top"><div><div class="title">{{.Title}}</div></div></div>
      <div class="stack">
        <div><b>Outcome</b> <span class="muted">{{.Outcome}}</span></div>
        <div><b>Method</b> <span class="muted">{{.Method}}</span></div>
        <div><b>Check</b> <span class="muted">{{.Check}}</span></div>
      </div>
      {{if or .Scope .Risks .Assumptions}}
      <details><summary>Details</summary>
        {{if .Scope}}<div class="ev">Scope: {{.Scope}}</div>{{end}}
        {{range .Risks}}<div class="ev">{{.}}</div>{{end}}
        {{range .Assumptions}}<div class="ev">Assumption: {{.}}</div>{{end}}
      </details>
      {{end}}
      <form method="post" action="/ui/projects/{{$.Project.ID}}/onboarding" class="actions">
        <input type="hidden" name="option" value="{{.ID}}">
        <button class="primary" name="action" value="select">Develop this direction</button>
      </form>
    </div>
    {{end}}
  </div>
  <div class="actions">
    <form method="post" action="/ui/projects/{{.Project.ID}}/onboarding">
      <button class="secondary" name="action" value="discover">Regenerate directions</button>
    </form>
    <form method="post" action="/ui/projects/{{.Project.ID}}/onboarding">
      <button class="secondary" name="action" value="save_intent">Back to the goal</button>
    </form>
  </div>
{{else if eq .Stage "plan"}}
  {{if .Selected}}
  <div class="card">
    <div class="eyebrow">Selected direction</div>
    <h2>{{.Selected.Title}}</h2>
    <div class="stack">
      <div><b>Outcome</b> <span class="muted">{{.Selected.Outcome}}</span></div>
      <div><b>Method</b> <span class="muted">{{.Selected.Method}}</span></div>
      <div><b>Check</b> <span class="muted">{{.Selected.Check}}</span></div>
    </div>
  </div>
  {{end}}

  {{if .State.PlanRun}}
  {{if eq .State.PlanRun.Status "running"}}
  <div class="card" data-live-card="intake" data-live-interval="3000">
    <div class="eyebrow">Plan in progress</div>
    <h2>{{if eq .Project.OnRamp "direct"}}Drafting one plan.{{else}}Drafting plan candidates.{{end}}</h2>
    <dl class="kv">
      <dt>stage</dt><dd><code>{{.State.PlanRun.Stage}}</code>{{if .State.PlanRun.Detail}} — {{.State.PlanRun.Detail}}{{end}}</dd>
      <dt>model</dt><dd><code>{{if .State.PlanRun.Model}}{{.State.PlanRun.Model}}{{else}}selecting…{{end}}</code></dd>
      <dt>response</dt><dd>{{.State.PlanRun.ResponseBytes}} chars received</dd>
      {{if .State.PlanRun.PlanIndex}}<dt>plan</dt><dd>{{.State.PlanRun.PlanIndex}} of 3 · {{.State.PlanRun.Expanded}} ready</dd>{{end}}
    </dl>
    {{if .State.PlanRun.Steps}}<details open><summary>What Athanor is doing</summary><ol class="stack">{{range .State.PlanRun.Steps}}<li><span class="mono">{{ts .At}}</span> <b>{{.Stage}}</b> — {{.Detail}}</li>{{end}}</ol></details>{{end}}
    <div class="actions">
      <a class="btn secondary" href="/ui/projects/{{.Project.ID}}/onboarding">Refresh</a>
      <form method="post" action="/ui/projects/{{.Project.ID}}/onboarding">
        <button class="secondary" name="action" value="cancel_planning">Cancel</button>
      </form>
    </div>
  </div>
  {{else if eq .State.PlanRun.Status "failed"}}
  <div class="notice bad">Plan generation failed: {{.State.PlanRun.Error}}</div>
  {{end}}
  {{end}}
  {{if .State.PlanError}}
  <div class="notice bad">Plan generation failed: {{.State.PlanError}}</div>
  {{end}}

  {{if .SelectedPlan}}
  <div class="card">
    <div class="eyebrow">Chosen plan</div>
    <h2>{{.SelectedPlan.Title}}</h2>
    <p class="muted">{{.SelectedPlan.Summary}}</p>
    <div class="meta"><span class="pill">{{len .SelectedPlan.Tasks}} tasks</span></div>
    <div class="table-wrap">
      <table><thead><tr><th>Task</th><th>Capability</th><th>Criteria</th></tr></thead><tbody>
      {{range .SelectedPlan.Tasks}}<tr>
        <td><b>{{.Title}}</b><div class="muted small">{{.Description}}</div></td>
        <td><span class="pill">{{.Capability}}</span></td>
        <td class="muted">{{join .Criteria}}</td>
      </tr>{{end}}
      </tbody></table>
    </div>
    <div class="actions">
      <form method="post" action="/ui/projects/{{.Project.ID}}/onboarding">
        <input type="hidden" name="candidate" value="{{.SelectedPlan.ID}}">
        <button class="primary" name="action" value="approve">Approve plan and start</button>
      </form>
      {{if ne .Project.OnRamp "direct"}}
      <form method="post" action="/ui/projects/{{.Project.ID}}/onboarding">
        <button class="secondary" name="action" value="generate_plans">Generate three again</button>
      </form>
      {{end}}
    </div>
  </div>
  {{else if .State.Plan.Candidates}}
  <h2>{{if eq .Project.OnRamp "direct"}}The plan{{else}}Plans{{end}}</h2>
  <p class="muted">Choose the plan you want to execute. A plan is usable only after its expansion finishes.</p>
  <div class="grid {{if eq .Project.OnRamp "direct"}}cols-2{{else}}cols-3{{end}}">
    {{range .State.Plan.Candidates}}
    <div class="option">
      <div class="top">
        <div><div class="eyebrow">{{if .Stance}}{{.Stance}}{{else}}Plan{{end}}</div><div class="title">{{.Title}}</div></div>
        <span class="pill {{if eq .Status "ready"}}ok{{else if eq .Status "failed"}}bad{{else}}run{{end}}">{{if .Status}}{{.Status}}{{else}}seed{{end}}</span>
      </div>
      <p class="muted">{{.Summary}}</p>
      <div class="meta"><span class="pill">{{len .Tasks}} tasks</span></div>
      <div class="stack">{{range .Tasks}}<div class="ev"><b>{{.Title}}</b>{{if .Capability}} <span class="muted">· {{.Capability}}</span>{{end}}</div>{{end}}</div>
      {{if .Error}}<div class="del small">{{.Error}}</div>{{end}}
      {{if eq .Status "ready"}}
      <form method="post" action="/ui/projects/{{$.Project.ID}}/onboarding" class="actions">
        <input type="hidden" name="candidate" value="{{.ID}}">
        <button class="primary" name="action" value="select_plan">Review this plan</button>
      </form>
      {{end}}
    </div>
    {{end}}
  </div>
  <div class="actions">
    <form method="post" action="/ui/projects/{{.Project.ID}}/onboarding">
      <button class="secondary" name="action" value="generate_plans">{{if eq .Project.OnRamp "direct"}}Generate again{{else}}Generate three again{{end}}</button>
    </form>
  </div>
  {{else}}
  <div class="card">
    <div class="eyebrow">Plan</div>
    <h2>{{if eq .Project.OnRamp "direct"}}Draft one plan.{{else}}Draft the plans.{{end}}</h2>
    <p class="muted">{{if eq .Project.OnRamp "direct"}}One concrete plan, one approval. No work starts until you approve it.{{else}}Three plans across the stances; you choose before work starts.{{end}}</p>
    <div class="actions">
      <form method="post" action="/ui/projects/{{.Project.ID}}/onboarding">
        {{if eq .Project.OnRamp "direct"}}<input type="hidden" name="mode" value="single">{{end}}
        <button class="primary" name="action" value="generate_plans">{{if eq .Project.OnRamp "direct"}}Draft the plan{{else}}Generate three plans{{end}}</button>
      </form>
      {{if .PlanPreview}}
      <form method="post" action="/ui/projects/{{.Project.ID}}/onboarding">
        <input type="hidden" name="candidate" value="deterministic">
        <button class="secondary" name="action" value="approve">Approve the simple plan</button>
      </form>
      {{end}}
    </div>
    {{if .PlanPreview}}<details><summary>Simple plan preview</summary><div class="prose">{{markdown .PlanPreview}}</div></details>{{end}}
  </div>
  {{end}}
{{else}}
  <div class="card">
    <div class="eyebrow">Approved</div>
    <h2>Work has started.</h2>
    <p class="muted">The plan created a dependency-ordered set of tasks. Watch the first job, or return to the project to see the graph.</p>
    <div class="actions">
      <a class="btn primary" href="/ui/projects/{{.Project.ID}}">Open project</a>
    </div>
  </div>
{{end}}
`
