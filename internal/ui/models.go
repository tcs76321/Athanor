package ui

import (
	"fmt"
	"net/http"
)

// modelsBody is the post-onboarding model pool and roster. First use owns the
// guided flow; this page is the everyday surface for adding, removing, and
// re-ranking models after setup.
const modelsBody = `
<div class="hero card">
  <div>
    <div class="eyebrow">Model repertoire</div>
    <h1>What Athanor may choose from.</h1>
    <p class="lede">You own membership. Athanor owns the measured ranking and the cast. Removing a model is the only override; the next job casts from what remains.</p>
  </div>
  <div class="actions">
    <form method="post" action="/ui/models/qualify"><button class="primary">Run smoke</button></form>
    <form method="post" action="/ui/models/qualify/deep"><button class="secondary">Run deep</button></form>
  </div>
</div>

<div class="card {{if not .PoolReady}}card-accent{{end}}">
  <div class="row row-middle">
    <div>
      <div class="eyebrow">{{.PoolCount}} eligible model(s) · minimum {{.PoolMin}}</div>
      <p class="muted">{{if .PoolReady}}Full repertoire: multiple candidates, an independent judge when a different family exists, and second opinions within budget.{{else}}Degraded mode: fewer independent perspectives. Four or more eligible models enable the full repertoire.{{end}} Machine: {{printf "%.0f" .Pool.MachineHostGiB}} GiB host, {{.Pool.MachineCPUs}} CPU, score {{.Pool.MachineScore}}/100.</p>
    </div>
    <span class="pill {{if .PoolReady}}ok{{else}}warn{{end}}">{{if .PoolReady}}full{{else}}degraded{{end}}</span>
    <span class="pill">machine {{.Pool.MachineScore}}/100</span>
  </div>
  {{if .PoolWarning}}<p class="del">{{.PoolWarning}}</p>{{end}}
</div>

{{if .Qualification.Running}}
<div class="card" data-auto-refresh="2500">
  <div class="row row-middle"><h3>Qualification running</h3><span class="pill run">{{.Qualification.Percent}}%</span></div>
  <progress class="progress" value="{{.Qualification.Percent}}" max="100">{{.Qualification.Percent}}%</progress>
  <p><b>{{.Qualification.Done}} / {{.Qualification.Total}}</b> cases{{if .Qualification.Elapsed}} · {{.Qualification.Elapsed}} elapsed{{end}}{{if .Qualification.Remaining}} · about {{.Qualification.Remaining}} remaining{{end}}</p>
  <p class="muted">The run is resumable. It uses the same grounded screen as first use.</p>
</div>
{{else if .Qualification.Complete}}
<div class="card"><p class="ok"><b>Qualification complete.</b> {{.Qualification.Done}} cases recorded. The ranking below is the current prior.</p></div>
{{else if .Qualification.Error}}
<div class="card"><p class="del">{{.Qualification.Error}}</p></div>
{{end}}

<h2>Pool</h2>
<div class="card flush"><div class="table-wrap">
<table><thead><tr><th>Model</th><th>Family</th><th>Size</th><th>Context</th><th>Measured</th><th>Status</th><th></th></tr></thead><tbody>
{{range .Pool.Rows}}<tr>
  <td class="mono">{{.Name}}</td>
  <td>{{if .Family}}{{.Family}}{{else}}—{{end}}</td>
  <td>{{gib .SizeBytes}}</td>
  <td>{{if .ContextTarget}}{{.ContextTarget}}{{else}}—{{end}}</td>
  <td>{{if .Qualified}}{{printf "%.0f%%" (mul .Score 100)}} <span class="muted">({{.Samples}})</span>{{else}}—{{end}}</td>
  <td>{{if .TooLarge}}<span class="pill bad">too large</span>{{else if .InPool}}<span class="pill ok">in pool</span>{{else}}<span class="pill">removed</span>{{end}}</td>
  <td>
    {{if .InPool}}
    <form method="post" action="/ui/models/pool" class="inline-form">
      <input type="hidden" name="action" value="pool_remove"><input type="hidden" name="model" value="{{.Name}}">
      <button class="small secondary">Remove</button>
    </form>
    {{else}}
    <form method="post" action="/ui/models/pool" class="inline-form">
      <input type="hidden" name="action" value="pool_add"><input type="hidden" name="model" value="{{.Name}}">
      <button class="small secondary">Add</button>
    </form>
    {{end}}
  </td>
</tr>{{end}}
</tbody></table>
</div></div>

{{if .Pool.QualifiedCount}}
<h2>Measured ranking</h2>
<div class="card flush"><div class="table-wrap">
<table><thead><tr><th>Model</th><th>Overall</th><th>Code</th><th>Data</th><th>Judgment</th><th>Samples</th></tr></thead><tbody>
{{range .PoolRanked}}<tr>
  <td class="mono">{{.Name}}</td>
  <td><span class="pill {{if ge .Score 0.75}}ok{{else if ge .Score 0.5}}warn{{else}}bad{{end}}">{{printf "%.0f%%" (mul .Score 100)}}</span></td>
  <td>{{if .Code}}{{printf "%.0f%%" (mul .Code 100)}}{{else}}—{{end}}</td>
  <td>{{if .Data}}{{printf "%.0f%%" (mul .Data 100)}}{{else}}—{{end}}</td>
  <td>{{if .Judgment}}{{printf "%.0f%%" (mul .Judgment 100)}}{{else}}—{{end}}</td>
  <td>{{.Samples}}</td>
</tr>{{end}}
</tbody></table>
</div></div>
<p class="muted small">Scores are smoothed success rates on this machine; samples are grounded cases. Ties break by name. Real job outcomes continue to update the ranking.</p>
{{end}}
`

func (u *UI) handleModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if u.needsOnboarding(ctx) {
		http.Redirect(w, r, "/ui/welcome", http.StatusSeeOther)
		return
	}
	if u.deps.Config == nil {
		u.fail(w, fmt.Errorf("configuration is not available"))
		return
	}
	data := u.welcomeData(ctx, onboardingState{Step: "models"})
	u.render(w, "Models", modelsBody, data)
}

func (u *UI) handleModelsPool(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		u.fail(w, err)
		return
	}
	var err error
	switch r.FormValue("action") {
	case "pool_add":
		err = u.includeModel(r.Context(), r.FormValue("model"))
	case "pool_remove":
		err = u.excludeModel(r.Context(), r.FormValue("model"))
	default:
		err = fmt.Errorf("unknown action %q", r.FormValue("action"))
	}
	if err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui/models", http.StatusSeeOther)
}

func (u *UI) handleModelsQualify(w http.ResponseWriter, r *http.Request) {
	if _, _, err := u.startPoolQualification(r.Context(), false, false); err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui/models", http.StatusSeeOther)
}

func (u *UI) handleModelsQualifyDeep(w http.ResponseWriter, r *http.Request) {
	if _, _, err := u.startPoolQualification(r.Context(), false, true); err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui/models", http.StatusSeeOther)
}
