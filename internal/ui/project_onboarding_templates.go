package ui

const projectOnboardingBody = `
<h1>{{.Project.Name}} <span class="pill">{{.Status}}</span></h1>
<p class="sub">{{.Project.Goal}}</p>
<div class="actions">
  <a class="btn secondary" href="/ui/projects/{{.Project.ID}}">Back to project</a>
  <a class="btn secondary" href="/ui/projects/{{.Project.ID}}/interview">Classic interview</a>
</div>
{{if .Requested}}<div class="notice">Access request queued in the Inbox. The host stays unused until the allowlist is updated in config.yaml.</div>{{end}}
{{if .DiscoveryError}}<div class="notice bad">Discovery failed: {{.DiscoveryError}}<br>The brief is still saved; start discovery again when ready.{{if .Run}}{{if .Run.Tail}}<details><summary>Raw model output (last 1200 chars)</summary><pre class="wrap">{{.Run.Tail}}</pre></details>{{end}}{{end}}</div>{{end}}

<div class="grid onboard-grid">
  <div class="card">
    <div class="eyebrow">Project onboarding</div>
    <div class="stepper">
      <div class="step {{if eq .Stage "brief"}}current{{end}} {{if or (eq .Stage "generating") (eq .Stage "choose") (eq .Stage "generating_plan") (eq .Stage "plan") (eq .Stage "approved")}}done{{end}}"><span class="num">1</span><span><span class="t">Brief</span><span class="d">Goal, limits, sources</span></span></div>
      <div class="step {{if eq .Stage "generating"}}current{{end}} {{if or (eq .Stage "choose") (eq .Stage "generating_plan") (eq .Stage "plan") (eq .Stage "approved")}}done{{end}}"><span class="num">2</span><span><span class="t">Discover</span><span class="d">A portfolio of directions</span></span></div>
      <div class="step {{if eq .Stage "choose"}}current{{end}} {{if or (eq .Stage "generating_plan") (eq .Stage "plan") (eq .Stage "approved")}}done{{end}}"><span class="num">3</span><span><span class="t">Choose</span><span class="d">Pick one direction</span></span></div>
      <div class="step {{if or (eq .Stage "plan") (eq .Stage "generating_plan")}}current{{end}} {{if eq .Stage "approved"}}done{{end}}"><span class="num">4</span><span><span class="t">Plan</span><span class="d">Three AI plans</span></span></div>
      <div class="step {{if eq .Stage "approved"}}current{{end}}"><span class="num">5</span><span><span class="t">Start</span><span class="d">Approve and run</span></span></div>
    </div>
  </div>

  <div>
  {{if eq .Stage "brief"}}
    <div class="card">
      <h2>What are we actually trying to do?</h2>
      <p class="muted">Short is fine. Athanor will ask for the limits and sources before it explores; the goal can stay one sentence.</p>
      <form method="post" action="/ui/projects/{{.Project.ID}}/onboarding">
        <label for="goal">Goal</label>
        <textarea id="goal" name="goal" rows="2" required data-refine-field="brief.goal">{{.Brief.Goal}}</textarea>
        <div class="field-help">Examples: “compile a daily news brief from approved sources” or “build a small internal tool with tests”.</div>

        <label for="constraints">Constraints / limits (one per line)</label>
        <textarea id="constraints" name="constraints" rows="4" data-refine-field="brief.constraints">{{join .Brief.Constraints}}</textarea>
        <div class="field-help">Time, money, skills, location, legal or ethical boundaries, things that are out of scope.</div>

        <label for="limitations">Anything Athanor must not do</label>
        <textarea id="limitations" name="limitations" rows="3" data-refine-field="brief.limitations" placeholder="No external publishing without review, no outbound messages, no new external services, no data outside the approved sources.">{{.Brief.Limitations}}</textarea>

        <label for="sources">Sources or pages Athanor may read (one per line)</label>
        <textarea id="sources" name="sources" rows="3" data-refine-field="brief.sources" placeholder="https://example.com/feed">{{join .Brief.Sources}}</textarea>
        <div class="field-help">These URLs are recorded in the approved plan. Research tasks fetch only allowlisted sources through the gateway; a domain outside the allowlist becomes an access request, not a silent fetch.</div>
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
          <p class="muted">A source marked “needs access” stays unused until its domain is added to <code>network.allow_list</code> in config.yaml. Athanor will not fetch it silently.</p>
        </div>
        {{end}}

        <label for="success">What would make this a success?</label>
        <textarea id="success" name="success" rows="3" data-refine-field="brief.success" placeholder="A daily report a reader can finish in ten minutes, with every claim traceable to a source.">{{.Brief.Success}}</textarea>

        <div class="actions">
          <button name="action" value="save" class="secondary">Save brief</button>
          <button name="action" value="discover" class="primary">Save and explore directions</button>
        </div>
      </form>
    </div>
  {{else if eq .Stage "generating"}}
    <div class="card" data-live-card="discovery" data-live-interval="2000">
      <div class="eyebrow">Discovery in progress</div>
      <h2>Athanor is thinking across industries.</h2>
      <p class="muted">This is one bounded model pass. The status updates in place; open panels stay open, and the result is saved when it finishes.</p>
      <progress class="progress" value="{{.DiscoveryPercent}}" max="100">{{.DiscoveryPercent}}%</progress>
      {{if .Run}}
      <div class="row row-middle"><span class="pill run">{{.DiscoveryPercent}}%</span><span class="muted">{{.DiscoveryElapsed}} elapsed</span></div>
      <dl class="kv">
        <dt>stage</dt><dd><code>{{.Run.Stage}}</code> — {{.Run.Detail}}</dd>
        <dt>model</dt><dd><code>{{.Run.Model}}</code></dd>
        <dt>prompt</dt><dd>{{.Run.PromptBytes}} chars assembled</dd>
        <dt>response</dt><dd>{{.Run.ResponseBytes}} chars received{{if .Run.Options}} · {{.Run.Options}} options parsed{{end}}</dd>
      </dl>
      <p class="muted">Request: {{.Brief.Goal}}{{if .Brief.Constraints}} · {{len .Brief.Constraints}} constraint(s){{end}}{{if .Brief.Sources}} · {{len .Brief.Sources}} source(s){{end}} · target 10 options.</p>
      {{if .Run.Steps}}
      <details open><summary>What Athanor is doing</summary>
        <ol class="stack">
        {{range .Run.Steps}}<li><span class="mono">{{ts .At}}</span> <b>{{.Stage}}</b> — {{.Detail}}</li>{{end}}
        </ol>
      </details>
      {{end}}
      {{if .Run.Tail}}
      <details><summary>Raw model output (last 1200 chars, display only)</summary>
        <pre class="wrap">{{.Run.Tail}}</pre>
      </details>
      {{end}}
      {{else}}
      <p class="muted">This session was started before a Core restart, so live progress is unavailable. Its status is <b>{{.Session.Status}}</b>; start discovery again if it has been running longer than 20 minutes.</p>
      {{end}}
      <div class="actions"><a class="btn secondary" href="/ui/projects/{{.Project.ID}}/onboarding">Refresh now</a></div>
    </div>
  {{else if eq .Stage "choose"}}
    <div class="card">
      <h2>{{len .Session.Options}} directions</h2>
      <p class="muted">Each option is a different audience, need, and approach. Scores are deterministic from the option's clarity, evidence, speed, cost fit, and risk — they are a reading aid, not a verdict. Choose the one you want to develop.</p>
    </div>
    {{if .Run}}{{if eq .Run.Status "failed"}}<div class="notice bad">{{.Run.Error}}<div class="actions"><a class="btn secondary" href="/ui/projects/{{$.Project.ID}}/onboarding">Try again</a></div></div>{{end}}{{end}}
    <div class="grid cols-2">
      {{range .Session.Options}}
      <div class="option">
        <div class="top">
          <div>
            <div class="eyebrow">{{.Domain}} · {{.Focus}}</div>
            <div class="title">{{.Title}}</div>
          </div>
          <span class="pill accent">{{index .Scores "total"}}/100</span>
        </div>
        <div class="one-liner">{{.OneLiner}}</div>
        <div class="meta">
          <span class="pill">~${{.SetupCostUSD}} setup cost</span>
          <span class="pill">{{.TimeToFirstDeliverableDays}} days to first deliverable</span>
          <span class="pill">{{len .Evidence}} source{{if ne (len .Evidence) 1}}s{{end}}</span>
          <span class="pill">confidence {{printf "%.2f" .Confidence}}</span>
        </div>
        <div class="stack">
          <div><b>Audience</b> <span class="muted">{{.Audience}}</span></div>
          <div><b>Need</b> <span class="muted">{{.Need}}</span></div>
          <div><b>Approach</b> <span class="muted">{{.Approach}}</span></div>
          <div><b>First deliverable</b> <span class="muted">{{.FirstDeliverable}}</span></div>
          <div><b>How to test it</b> <span class="muted">{{.FirstCheck}}</span></div>
        </div>
        <div class="score">
          <div class="score-row"><span>Clarity</span><progress class="progress" value="{{index .Scores "clarity"}}" max="20"></progress><span>{{index .Scores "clarity"}}/20</span></div>
          <div class="score-row"><span>Evidence</span><progress class="progress" value="{{index .Scores "evidence"}}" max="20"></progress><span>{{index .Scores "evidence"}}/20</span></div>
          <div class="score-row"><span>Speed</span><progress class="progress" value="{{index .Scores "speed"}}" max="20"></progress><span>{{index .Scores "speed"}}/20</span></div>
          <div class="score-row"><span>Cost fit</span><progress class="progress" value="{{index .Scores "cost"}}" max="20"></progress><span>{{index .Scores "cost"}}/20</span></div>
          <div class="score-row"><span>Risk</span><progress class="progress" value="{{index .Scores "risk"}}" max="20"></progress><span>{{index .Scores "risk"}}/20</span></div>
        </div>
        {{if .Risks}}<details><summary>Risks and assumptions</summary>{{range .Risks}}<div class="ev">{{.}}</div>{{end}}{{range .Assumptions}}<div class="ev">Assumption: {{.}}</div>{{end}}</details>{{end}}
        <form method="post" action="/ui/projects/{{$.Project.ID}}/onboarding" class="actions">
          <input type="hidden" name="option" value="{{.ID}}">
          <button class="primary" name="action" value="select">Develop this direction</button>
        </form>
      </div>
      {{end}}
    </div>
  {{else if eq .Stage "generating_plan"}}
    <div class="card" data-live-card="planning" data-live-interval="4000">
      <div class="eyebrow">Planning in progress</div>
      <h2>Athanor is drafting three plans.</h2>
      <p class="muted">The plans differ by speed, depth, and risk. This panel updates in place; you can leave and come back.</p>
      <progress class="progress" value="45" max="100">45%</progress>
      <div class="actions"><a class="btn secondary" href="/ui/projects/{{.Project.ID}}/onboarding">Refresh</a></div>
    </div>
  {{else if eq .Stage "plan"}}
    <div class="card">
      <div class="eyebrow">Selected direction</div>
      <h2>{{.Selected.Title}}</h2>
      <p class="muted">{{.Selected.OneLiner}}</p>
      <div class="meta">
        <span class="pill">{{.Selected.Domain}} · {{.Selected.Focus}}</span>
        <span class="pill">~${{.Selected.SetupCostUSD}} setup cost</span>
        <span class="pill">{{.Selected.TimeToFirstDeliverableDays}} days to first deliverable</span>
      </div>
      <div class="actions">
        <form method="post" action="/ui/projects/{{.Project.ID}}/onboarding">
          <button class="secondary" name="action" value="reset">Choose a different direction</button>
        </form>
      </div>
    </div>
    {{if .PlanError}}<div class="notice bad">Plan generation failed: {{.PlanError}}</div>{{end}}

    {{if .SelectedPlan}}
    <div class="card">
      <div class="eyebrow">Chosen plan</div>
      <h2>{{.SelectedPlan.Title}}</h2>
      <p class="muted">{{.SelectedPlan.Summary}}</p>
      <div class="meta">
        <span class="pill accent">score {{.SelectedPlan.Score}}/100</span>
        <span class="pill">confidence {{printf "%.2f" .SelectedPlan.Confidence}}</span>
      </div>
      <div class="table-wrap">
        <table><thead><tr><th>Task</th><th>Capability</th><th>Criteria</th></tr></thead><tbody>
        {{range .SelectedPlan.Tasks}}<tr>
          <td><b>{{.Title}}</b><div class="muted small">{{.Description}}</div></td>
          <td><span class="pill">{{.Capability}}</span></td>
          <td class="muted">{{join .Criteria}}</td>
        </tr>{{end}}
        </tbody></table>
      </div>
      {{if .SelectedPlan.Risks}}<div class="actions"><span class="muted">Risks: {{join .SelectedPlan.Risks}}</span></div>{{end}}
      <div class="actions">
        <form method="post" action="/ui/projects/{{.Project.ID}}/onboarding">
          <button class="primary" name="action" value="approve">Approve plan and start</button>
        </form>
        <form method="post" action="/ui/projects/{{.Project.ID}}/onboarding">
          <button class="secondary" name="action" value="generate_plans">Generate three again</button>
        </form>
      </div>
    </div>
    {{else if .PlanCandidates}}
    <h2>Three plans</h2>
    <p class="muted">Choose the plan you want to execute. The score is deterministic from task coverage, criteria, risks, and specificity; it is a reading aid, not a verdict.</p>
    <div class="grid cols-3">
      {{range $i, $c := .PlanCandidates}}
      <div class="option">
        <div class="top">
          <div><div class="eyebrow">Option {{add $i 1}}</div><div class="title">{{$c.Title}}</div></div>
          <span class="pill accent">{{$c.Score}}</span>
        </div>
        <p class="muted">{{$c.Summary}}</p>
        <div class="meta"><span class="pill">{{len $c.Tasks}} tasks</span><span class="pill">confidence {{printf "%.2f" $c.Confidence}}</span></div>
        <div class="stack">
          {{range $c.Tasks}}<div class="ev"><b>{{.Title}}</b> <span class="muted">· {{.Capability}}</span></div>{{end}}
        </div>
        {{if $c.Risks}}<details><summary>Risks</summary>{{range $c.Risks}}<div class="ev">{{.}}</div>{{end}}</details>{{end}}
        <form method="post" action="/ui/projects/{{$.Project.ID}}/onboarding" class="actions">
          <input type="hidden" name="plan" value="{{$i}}">
          <button class="primary" name="action" value="select_plan">Use this plan</button>
        </form>
      </div>
      {{end}}
    </div>
    <div class="actions">
      <form method="post" action="/ui/projects/{{.Project.ID}}/onboarding">
        <button class="secondary" name="action" value="generate_plans">Generate three again</button>
      </form>
    </div>
    {{else}}
    <div class="card">
      <div class="eyebrow">AI plans</div>
      {{if .PlanningAvailable}}
      <h2>Draft three plans.</h2>
      <p class="muted">Athanor will turn the selected direction and your brief into three distinct plans: fastest usable first version, deepest coverage, and leanest path. You choose before work starts.</p>
      <form method="post" action="/ui/projects/{{.Project.ID}}/onboarding">
        <button class="primary" name="action" value="generate_plans">Generate three plans</button>
      </form>
      <p class="muted small">Or approve the deterministic plan below without an extra model call.</p>
      {{else}}
      <h2>Simple plan.</h2>
      <p class="muted">AI planning is not configured. Review and approve the deterministic plan below.</p>
      {{end}}
      <div class="actions">
        <form method="post" action="/ui/projects/{{.Project.ID}}/onboarding">
          <button class="{{if .PlanningAvailable}}secondary{{else}}primary{{end}}" name="action" value="approve">Approve plan and start</button>
        </form>
      </div>
    </div>
    {{if .Plan}}<details><summary>Simple plan preview</summary><div class="prose">{{markdown .Plan}}</div></details>{{end}}
    {{end}}
  {{else}}
    <div class="card">
      <div class="eyebrow">Approved</div>
      <h2>Work has started.</h2>
      <p class="muted">The plan created a dependency-ordered set of tasks. Watch the first job, or return to the project to see the graph.</p>
      <div class="actions">
        <a class="btn primary" href="/ui/projects/{{.Project.ID}}">Open project</a>
        <a class="btn secondary" href="/ui/projects/{{.Project.ID}}/onboarding">Review onboarding</a>
      </div>
    </div>
  {{end}}
  </div>
</div>
`
