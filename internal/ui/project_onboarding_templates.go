package ui

const projectOnboardingBody = `
<h1>{{.Project.Name}} <span class="pill">{{.Status}}</span></h1>
<p class="sub">{{.Project.Goal}}</p>
<div class="actions">
  <a class="btn secondary" href="/ui/projects/{{.Project.ID}}">Back to project</a>
  <a class="btn secondary" href="/ui/projects/{{.Project.ID}}/interview">Classic interview</a>
</div>
{{if .Requested}}<div class="notice">Access request queued in the Inbox. The host stays unused until the allowlist is updated in config.yaml.</div>{{end}}
{{if .DiscoveryError}}<div class="notice bad">Discovery failed: {{.DiscoveryError}}<br>The brief is still saved; start discovery again when ready.</div>{{end}}

<div class="grid onboard-grid">
  <div class="card">
    <div class="eyebrow">Project onboarding</div>
    <div class="stepper">
      <div class="step {{if eq .Stage "brief"}}current{{end}} {{if or (eq .Stage "generating") (eq .Stage "choose") (eq .Stage "plan") (eq .Stage "approved")}}done{{end}}"><span class="num">1</span><span><span class="t">Brief</span><span class="d">Goal, limits, sources</span></span></div>
      <div class="step {{if eq .Stage "generating"}}current{{end}} {{if or (eq .Stage "choose") (eq .Stage "plan") (eq .Stage "approved")}}done{{end}}"><span class="num">2</span><span><span class="t">Discover</span><span class="d">A portfolio of directions</span></span></div>
      <div class="step {{if eq .Stage "choose"}}current{{end}} {{if or (eq .Stage "plan") (eq .Stage "approved")}}done{{end}}"><span class="num">3</span><span><span class="t">Choose</span><span class="d">Pick one direction</span></span></div>
      <div class="step {{if eq .Stage "plan"}}current{{end}} {{if eq .Stage "approved"}}done{{end}}"><span class="num">4</span><span><span class="t">Plan</span><span class="d">Review the 30-day plan</span></span></div>
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
        <textarea id="goal" name="goal" rows="2" required>{{.Brief.Goal}}</textarea>
        <div class="field-help">Example: “make money online” or “find a small B2B service I can validate in 30 days”.</div>

        <label for="constraints">Constraints / limits (one per line)</label>
        <textarea id="constraints" name="constraints" rows="4">{{join .Brief.Constraints}}</textarea>
        <div class="field-help">Time, money, skills, location, legal or ethical boundaries, things that are out of scope.</div>

        <label for="limitations">Anything Athanor must not do</label>
        <textarea id="limitations" name="limitations" rows="3" placeholder="No ads, no cold calling, no regulated advice, no publishing without review.">{{.Brief.Limitations}}</textarea>

        <label for="sources">Sources or pages Athanor may read (one per line)</label>
        <textarea id="sources" name="sources" rows="3" placeholder="https://example.com/market-report">{{join .Brief.Sources}}</textarea>
        <div class="field-help">Only these URLs are fetched through the gateway. A domain outside the allowlist becomes an access request, not a silent fetch.</div>
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
        <textarea id="success" name="success" rows="3" placeholder="Five customer conversations, one paid pilot, a decision with evidence.">{{.Brief.Success}}</textarea>

        <div class="actions">
          <button name="action" value="save" class="secondary">Save brief</button>
          <button name="action" value="discover" class="primary">Save and explore directions</button>
        </div>
      </form>
    </div>
  {{else if eq .Stage "generating"}}
    <div class="card" data-auto-refresh="4000">
      <div class="eyebrow">Discovery in progress</div>
      <h2>Athanor is thinking across industries.</h2>
      <p class="muted">It is generating a portfolio of concrete directions, not one answer. This usually takes a few minutes on a local model. The page refreshes itself; you can leave and come back.</p>
      <progress class="progress" value="35" max="100">35%</progress>
      <div class="actions"><a class="btn secondary" href="/ui/projects/{{.Project.ID}}/onboarding">Refresh</a></div>
    </div>
  {{else if eq .Stage "choose"}}
    <div class="card">
      <h2>{{len .Session.Options}} directions</h2>
      <p class="muted">Each option is a different customer and wedge. Scores are deterministic from the option's clarity, evidence, speed, capital, and risk — they are a reading aid, not a verdict. Choose the one you want to develop.</p>
    </div>
    {{if .Run}}{{if eq .Run.Status "failed"}}<div class="notice bad">{{.Run.Error}}<div class="actions"><a class="btn secondary" href="/ui/projects/{{$.Project.ID}}/onboarding">Try again</a></div></div>{{end}}{{end}}
    <div class="grid cols-2">
      {{range .Session.Options}}
      <div class="option">
        <div class="top">
          <div>
            <div class="eyebrow">{{.Industry}} · {{.Niche}}</div>
            <div class="title">{{.Title}}</div>
          </div>
          <span class="pill accent">{{index .Scores "total"}}/100</span>
        </div>
        <div class="one-liner">{{.OneLiner}}</div>
        <div class="meta">
          <span class="pill">${{.StartupCostUSD}} to start</span>
          <span class="pill">{{.TimeToFirstRevenueDays}} days to revenue</span>
          <span class="pill">{{len .Evidence}} source{{if ne (len .Evidence) 1}}s{{end}}</span>
          <span class="pill">confidence {{printf "%.2f" .Confidence}}</span>
        </div>
        <div class="stack">
          <div><b>Customer</b> <span class="muted">{{.Customer}}</span></div>
          <div><b>Pain</b> <span class="muted">{{.Pain}}</span></div>
          <div><b>Wedge</b> <span class="muted">{{.Wedge}}</span></div>
          <div><b>First offer</b> <span class="muted">{{.FirstOffer}}</span></div>
          <div><b>First experiment</b> <span class="muted">{{.ValidationExperiment}}</span></div>
        </div>
        <div class="score">
          <div class="score-row"><span>Clarity</span><progress class="progress" value="{{index .Scores "clarity"}}" max="20"></progress><span>{{index .Scores "clarity"}}/20</span></div>
          <div class="score-row"><span>Evidence</span><progress class="progress" value="{{index .Scores "evidence"}}" max="20"></progress><span>{{index .Scores "evidence"}}/20</span></div>
          <div class="score-row"><span>Speed</span><progress class="progress" value="{{index .Scores "speed"}}" max="20"></progress><span>{{index .Scores "speed"}}/20</span></div>
          <div class="score-row"><span>Capital fit</span><progress class="progress" value="{{index .Scores "capital"}}" max="20"></progress><span>{{index .Scores "capital"}}/20</span></div>
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
  {{else if eq .Stage "plan"}}
    <div class="card">
      <div class="eyebrow">Selected direction</div>
      <h2>{{.Selected.Title}}</h2>
      <p class="muted">{{.Selected.OneLiner}}</p>
      <div class="meta">
        <span class="pill">{{.Selected.Industry}} · {{.Selected.Niche}}</span>
        <span class="pill">${{.Selected.StartupCostUSD}} to start</span>
        <span class="pill">{{.Selected.TimeToFirstRevenueDays}} days to revenue</span>
      </div>
      <div class="actions">
        <form method="post" action="/ui/projects/{{.Project.ID}}/onboarding">
          <button class="secondary" name="action" value="reset">Choose a different direction</button>
        </form>
        <form method="post" action="/ui/projects/{{.Project.ID}}/onboarding">
          <button class="primary" name="action" value="approve">Approve plan and start</button>
        </form>
      </div>
    </div>
    <div class="card">
      <h2>Plan</h2>
      <div class="prose">{{markdown .Plan}}</div>
    </div>
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
