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
    <form method="post" action="/ui/models/qualify">
      <input type="hidden" name="mode" value="core">
      <button class="primary">Run minimal core</button>
    </form>
    <form method="post" action="/ui/models/qualify">
      <input type="hidden" name="mode" value="extra">
      <label class="muted small">Minimum run time
        <select name="minutes">
          <option value="10">10 min</option>
          <option value="20" selected>20 min</option>
          <option value="30">30 min</option>
          <option value="45">45 min</option>
          <option value="60">60 min</option>
        </select>
      </label>
      <label class="muted small"><input type="checkbox" name="deep" value="1"> deep cases</label>
      <button class="secondary">Run core + fill minimum</button>
    </form>
  </div>
</div>

<div class="card {{if not .PoolReady}}card-accent{{end}}">
  <div class="row row-middle">
    <div>
      <div class="eyebrow">{{.PoolCount}} eligible model(s) · minimum {{.PoolMin}}</div>
      <p class="muted">{{if .PoolReady}}Full repertoire: multiple candidates, an independent judge when a different family exists, and second opinions within budget.{{else}}Degraded mode: fewer independent perspectives. Four or more eligible models enable the full repertoire.{{end}} Host capacity: {{printf "%.0f" .Pool.MachineHostGiB}} GiB RAM, {{.Pool.MachineCPUs}} CPU, capacity {{.Pool.MachineScore}}/100 — a RAM/CPU/disk heuristic, not a model-quality score. The measured ranking below is the model score.</p>
    </div>
    <span class="pill {{if .PoolReady}}ok{{else}}warn{{end}}">{{if .PoolReady}}full{{else}}degraded{{end}}</span>
    <span class="pill" title="RAM/CPU/disk capacity heuristic, not model quality">host capacity {{.Pool.MachineScore}}/100</span>
  </div>
  {{if .PoolWarning}}<p class="del">{{.PoolWarning}}</p>{{end}}
  {{if .PoolRecommendation}}<p class="muted small">{{.PoolRecommendation}}</p>{{end}}
</div>

{{if .Qualification.Running}}
<div class="card" data-live-card="models-qualification" data-live-interval="2500">
  <div class="row row-middle"><h3>Qualification running</h3><span class="pill run">{{.Qualification.Percent}}%</span></div>
  <progress class="progress" value="{{.Qualification.Percent}}" max="100">{{.Qualification.Percent}}%</progress>
  <p><b>{{.Qualification.Done}} / {{.Qualification.Total}}</b> cases{{if .Qualification.Elapsed}} · {{.Qualification.Elapsed}} elapsed{{end}}{{if .Qualification.Estimate}} · estimated core {{.Qualification.Estimate}}{{end}}{{if .Qualification.MinimumRemaining}} · minimum remaining {{.Qualification.MinimumRemaining}}{{end}}{{if .Qualification.Remaining}} · about {{.Qualification.Remaining}} remaining{{end}}</p>
  <p class="muted">The run is resumable. It uses the same grounded screen as first use.</p>
  <form method="post" action="/ui/setup/qualify/cancel">
    <input type="hidden" name="next" value="/ui/models">
    <button class="danger small" data-confirm="Stop this qualification run? Completed cases are kept and the run can resume later.">Stop qualification</button>
  </form>
</div>
{{else if .Qualification.Complete}}
<div class="card"><p class="ok"><b>Qualification complete.</b> {{.Qualification.Done}} cases recorded. The ranking below is the current prior.</p></div>
{{else if .Qualification.Error}}
<div class="card"><p class="del">{{.Qualification.Error}}</p></div>
{{end}}

<h2>Pool</h2>
<div class="card flush"><div class="table-wrap">
<table><thead><tr><th>Model</th><th>Family</th><th>Size</th><th>Context (work/max)</th><th>Measured</th><th>Status</th><th></th></tr></thead><tbody>
{{range .Pool.Rows}}<tr>
  <td class="mono">{{.Name}}</td>
  <td>{{if .Family}}{{.Family}}{{else}}—{{end}}</td>
  <td>{{gib .SizeBytes}}{{if .ResidentBytes}}<div class="muted small">{{gib .ResidentBytes}} resident</div>{{end}}</td>
  <td>{{if .ContextTarget}}{{.ContextTarget}}{{else}}—{{end}}{{if .MaxContext}} / {{.MaxContext}}{{end}}</td>
  <td>{{if .Qualified}}{{printf "%.0f%%" (mul .Score 100)}} <span class="muted">({{.Samples}})</span>{{else}}—{{end}}</td>
  <td>{{if .TooLarge}}<span class="pill bad">does not fit at 8,192+</span>{{else if .InPool}}<span class="pill ok">{{if .MaxContext}}fits at {{.MaxContext}}{{else}}in pool{{end}}</span>{{else}}<span class="pill">removed</span>{{end}}</td>
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
{{if .RolePicks}}
<h2>Baseline cast</h2>
<p class="muted">The best small, medium, and big models on this machine for the everyday roles. This is a first signal; job outcomes keep updating selection.</p>
<div class="grid cols-3">
  {{range .RolePicks}}
  <div class="card">
    <div class="eyebrow">{{roleLabel .Role}} · {{.Tier}}</div>
    <h3 class="mono">{{.Model}}</h3>
    <p class="muted">{{printf "%.0f%%" (mul .Score 100)}} role score · {{printf "%.1f" .SizeGiB}} GiB · {{.Context}} ctx</p>
  </div>
  {{end}}
</div>
{{end}}
{{if .Qualification.CanExtend}}
<div class="card">
  <h3>Core complete</h3>
  <p class="muted">Every model has the minimal capability core. Choose a minimum run time; extra cases fill it, and the first model's core pass sets the estimate.</p>
  <form method="post" action="/ui/models/qualify">
    <input type="hidden" name="mode" value="extra">
    <label class="muted small">Minimum run time
      <select name="minutes">
        <option value="10">10 min</option>
        <option value="20" selected>20 min</option>
        <option value="30">30 min</option>
        <option value="45">45 min</option>
        <option value="60">60 min</option>
      </select>
    </label>
    <label class="muted small"><input type="checkbox" name="deep" value="1"> deep cases</label>
    <button class="primary">Run for at least this long</button>
  </form>
</div>
{{end}}
<h2>Measured ranking</h2>
<div class="card flush"><div class="table-wrap">
<table><thead><tr><th>Model</th><th>Family</th><th>Size</th><th>Overall</th><th>Code</th><th>Data</th><th>Judgment</th><th>Summ.</th><th>Sec.</th><th>Agent</th><th>Samples</th></tr></thead><tbody>
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
</tr>{{end}}
</tbody></table>
</div></div>
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
<p class="muted small">Scores are smoothed success rates on this machine; samples are grounded cases. Ties break by name. Real job outcomes continue to update the ranking.</p>
{{if .QualDetails}}
<h2>Case detail</h2>
<p class="muted">Expand a model to see every case, the question, the expected answer, and what it produced.</p>
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
	if err := r.ParseForm(); err != nil {
		u.fail(w, err)
		return
	}
	mode := r.FormValue("mode")
	minutes := parseFormInt(r, "minutes", 20)
	deep := r.FormValue("deep") != ""
	if _, _, err := u.startPoolQualification(r.Context(), false, deep, mode, minutes); err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui/models", http.StatusSeeOther)
}

func (u *UI) handleModelsQualifyDeep(w http.ResponseWriter, r *http.Request) {
	if _, _, err := u.startPoolQualification(r.Context(), false, true, "core", 0); err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui/models", http.StatusSeeOther)
}
