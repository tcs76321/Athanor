package ui

const settingsBody = `
<h1>Settings</h1>
<p class="sub">Appearance is local to this Core. Configuration is read-only here; the effective values and their source are shown in Runtime.</p>

<h2 id="appearance">Appearance</h2>
<div class="card">
  <form method="post" action="/ui/theme" data-theme-form>
    <label for="theme">Theme</label>
    <select id="theme" name="theme">
      {{range .Themes}}<option value="{{.ID}}" {{if eq .ID $.Theme}}selected{{end}}>{{.Label}}</option>{{end}}
    </select>
    <div class="field-help">The theme is stored in the Core and applies to every window. Monokai is the default; Ember Paper is the warm light theme.</div>
    <div class="actions"><button class="primary">Apply theme</button></div>
  </form>
</div>

<div class="grid cols-3">
  {{range .Themes}}
  <div class="card card-hover" data-theme-preview="{{.ID}}">
    <div class="row row-middle">
      <div>
        <div class="eyebrow">Theme</div>
        <h3>{{.Label}}</h3>
      </div>
      {{if eq .ID $.Theme}}<span class="pill ok right">active</span>{{end}}
    </div>
    <div class="mini-bar theme-swatch" aria-hidden="true">
      <span></span><span></span><span></span><span></span>
    </div>
  </div>
  {{end}}
</div>

<h2>Configuration</h2>
<div class="card">
  <dl class="kv">
    <dt>config file</dt><dd><code>{{.ConfigPath}}</code></dd>
    <dt>serving runtime</dt><dd>{{.Runtime}}</dd>
    <dt>version</dt><dd>{{.Version}}</dd>
  </dl>
  <p class="muted">The GUI does not rewrite config.yaml. Use the Runtime and Setup pages to inspect effective values, and edit the file for changes. Network allowlist and persona routing changes are HITL-gated project decisions.</p>
  <div class="actions"><a class="btn" href="/ui/runtime">Open runtime</a><a class="btn secondary" href="/ui/setup">Model setup</a></div>
</div>

<h2>Other preferences</h2>
<div class="card">
  <p class="muted">Desktop notifications are enabled from the <a href="/ui/notifications">Inbox</a>. Density and font size follow the theme tokens; a compact mode lands with the work-surface pass.</p>
</div>
`

const sitemapBody = `
<h1>Sitemap</h1>
<p class="sub">Every surface in Athanor. Primary navigation stays small; this page is the complete index.</p>
<div class="card flush">
<div class="table-wrap">
<table>
<thead><tr><th>Group</th><th>Surface</th><th>What it shows</th></tr></thead>
<tbody>
{{range .Surfaces}}<tr>
  <td><span class="pill">{{.Group}}</span></td>
  <td><a href="{{.URL}}">{{.Label}}</a></td>
  <td class="muted">{{.Note}}</td>
</tr>{{end}}
</tbody>
</table>
</div>
</div>
`

const searchBody = `
<h1>Search</h1>
<p class="sub">{{if .Query}}{{.Count}} result{{if ne .Count 1}}s{{end}} for “{{.Query}}”{{else}}Search projects, artifacts, jobs, events, and pages.{{end}}</p>
<div class="card">
<form method="get" action="/ui/search" role="search" class="row">
  <input type="search" name="q" value="{{.Query}}" placeholder="Search everything…" aria-label="Search">
  <button class="primary">Search</button>
</form>
</div>
{{if .Results}}
<div class="card flush">
<div class="table-wrap">
<table>
<thead><tr><th>Kind</th><th>Result</th><th>Detail</th></tr></thead>
<tbody>
{{range .Results}}<tr>
  <td><span class="pill">{{.Kind}}</span></td>
  <td><a href="{{.URL}}">{{.Label}}</a></td>
  <td class="muted">{{.Detail}}</td>
</tr>{{end}}
</tbody>
</table>
</div>
</div>
{{else if .Query}}
<div class="empty"><strong>No matches.</strong><br>Try a project name, an artifact kind, a job id, or a page name.</div>
{{else}}
<div class="empty"><strong>Start typing.</strong><br>The command palette (⌘K / Ctrl-K) opens the same index from anywhere.</div>
{{end}}
`
