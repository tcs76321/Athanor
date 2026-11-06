package ui

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/tcs76321/athanor/internal/store"
)

// searchResult is one item in the global search and command palette.
type searchResult struct {
	Label  string `json:"label"`
	URL    string `json:"url"`
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
}

// surface is one entry in the sitemap: everything the UI can show, reachable
// even when it is not in the primary navigation.
type surface struct {
	Group string
	Label string
	URL   string
	Note  string
}

var surfaces = []surface{
	{"Work", "Dashboard", "/ui", "What needs you and what the machine is doing"},
	{"Work", "Inbox", "/ui/notifications", "Approvals, reviews, alarms, failures"},
	{"Work", "Projects", "/ui/projects", "All projects and their work history"},
	{"Work", "New project", "/ui/projects/new", "Start a project and its onboarding"},
	{"Work", "Review queue", "/ui/reviews", "Blinded human review of accepted work"},
	{"Work", "Library", "/ui/artifacts", "All artifacts, versions, diffs, exports"},
	{"Insights", "Statistics", "/ui/statistics", "Outcomes, tokens, strategy insights"},
	{"Insights", "Models", "/ui/models", "Pool membership, measured ranking, and casts"},
	{"Insights", "Memory", "/ui/memory", "Chunks, compacted memory, embeddings, sources"},
	{"Insights", "Network", "/ui/events?category=network", "Allowed, denied, and truncated fetches"},
	{"Insights", "Security", "/ui/events?category=security", "Scanner and containment decisions"},
	{"Insights", "Daydream", "/ui/daydream", "Idle-cycle drafts and memory consolidation"},
	{"System", "Setup", "/ui/setup", "Models, personas, qualification, routing"},
	{"System", "Settings", "/ui/settings", "Appearance, density, effective configuration"},
	{"System", "Runtime", "/ui/runtime", "Effective runtime, personas, budget, alarms"},
	{"System", "Events", "/ui/events", "The full append-only audit log"},
	{"System", "Corrections", "/ui/corrections", "Lessons captured from rejections and failures"},
	{"System", "Sitemap", "/ui/sitemap", "Every UI surface in one place"},
}

func (u *UI) handleSettings(w http.ResponseWriter, r *http.Request) {
	frozen := u.deps.Freezer != nil && u.deps.Freezer.Frozen()
	if u.deps.Control != nil {
		frozen = u.deps.Control.Frozen()
	}
	u.render(w, "Settings", settingsBody, map[string]any{
		"Themes": themeNames, "Theme": u.Theme(), "ThemeLabel": themeLabel(u.Theme()),
		"ConfigPath": configPath(u.deps.Config), "Frozen": frozen,
		"CanControl": u.deps.Control != nil,
	})
}

// handleFreeze drives the §22 kill switch from Settings. A freeze is
// immediate; unfreeze requires a reason and is audited by the control store.
func (u *UI) handleFreeze(w http.ResponseWriter, r *http.Request) {
	if u.deps.Control == nil {
		u.fail(w, fmt.Errorf("the kill switch is not configured for the UI"))
		return
	}
	if err := r.ParseForm(); err != nil {
		u.fail(w, err)
		return
	}
	switch r.FormValue("action") {
	case "freeze":
		if err := u.deps.Control.Freeze(r.Context()); err != nil {
			u.fail(w, err)
			return
		}
	case "unfreeze":
		reason := strings.TrimSpace(r.FormValue("reason"))
		if reason == "" {
			u.fail(w, fmt.Errorf("an unfreeze reason is required"))
			return
		}
		if err := u.deps.Control.Unfreeze(r.Context(), reason); err != nil {
			u.fail(w, err)
			return
		}
	default:
		u.fail(w, fmt.Errorf("unknown freeze action"))
		return
	}
	http.Redirect(w, r, "/ui/settings#system", http.StatusSeeOther)
}

func (u *UI) handleTheme(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		u.fail(w, err)
		return
	}
	if err := u.setTheme(r.FormValue("theme")); err != nil {
		u.fail(w, err)
		return
	}
	next := r.FormValue("next")
	if !strings.HasPrefix(next, "/ui/") {
		next = "/ui/settings#appearance"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (u *UI) handleDensity(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		u.fail(w, err)
		return
	}
	if err := u.setDensity(r.FormValue("density")); err != nil {
		u.fail(w, err)
		return
	}
	next := r.FormValue("next")
	if !strings.HasPrefix(next, "/ui/") {
		next = "/ui/settings#appearance"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (u *UI) handleSitemap(w http.ResponseWriter, r *http.Request) {
	u.render(w, "Sitemap", sitemapBody, map[string]any{"Surfaces": surfaces})
}

// handleSearch searches projects, artifacts, jobs, events, and UI surfaces.
// The palette calls it with format=json; the full page is a server-rendered
// result list that works without JavaScript.
func (u *UI) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	results := u.search(r, q)
	if r.URL.Query().Get("format") == "json" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"query": q, "results": results})
		return
	}
	data := map[string]any{"Query": q, "Results": results, "Count": len(results)}
	if r.Header.Get("HX-Request") == "true" {
		u.renderPartial(w, searchResultsBody, data)
		return
	}
	u.render(w, "Search", searchBody, data)
}

func (u *UI) search(r *http.Request, q string) []searchResult {
	var out []searchResult
	if q == "" {
		for _, s := range surfaces {
			out = append(out, searchResult{Label: s.Label, URL: s.URL, Kind: s.Group, Detail: s.Note})
		}
		return out
	}
	// Pages first: a search for "settings" should jump to the page even when
	// no entity matches.
	for _, s := range surfaces {
		if strings.Contains(strings.ToLower(s.Label+" "+s.Note), strings.ToLower(q)) {
			out = append(out, searchResult{Label: s.Label, URL: s.URL, Kind: s.Group, Detail: s.Note})
		}
	}

	if u.deps.Store != nil {
		ctx := r.Context()
		for _, row := range queryRows(ctx, u.deps.Store.DB(),
			`SELECT id, name, goal FROM projects
			  WHERE instr(lower(name), lower(?)) > 0 OR instr(lower(goal), lower(?)) > 0
			  ORDER BY updated_at DESC LIMIT 20`, scanSearchRow, q, q) {
			out = append(out, searchResult{
				Label: row[1], URL: "/ui/projects/" + url.PathEscape(row[0]),
				Kind: "Project", Detail: row[2],
			})
		}
		for _, row := range queryRows(ctx, u.deps.Store.DB(),
			`SELECT t.id, t.project_id, t.title, COALESCE(p.name, '')
			  FROM tasks t LEFT JOIN projects p ON p.id = t.project_id
			  WHERE instr(lower(t.title), lower(?)) > 0
			     OR instr(lower(t.description), lower(?)) > 0
			     OR instr(lower(p.name), lower(?)) > 0
			  ORDER BY t.updated_at DESC LIMIT 20`, scanSearchRow, q, q, q) {
			out = append(out, searchResult{
				Label: row[2], URL: "/ui/projects/" + url.PathEscape(row[1]) + "#work",
				Kind: "Task", Detail: row[3],
			})
		}
		for _, row := range queryRows(ctx, u.deps.Store.DB(),
			`SELECT a.id, a.kind, a.status, COALESCE(p.name, '')
			  FROM artifacts a LEFT JOIN projects p ON p.id = a.project_id
			  WHERE instr(lower(a.kind), lower(?)) > 0
			     OR instr(lower(a.status), lower(?)) > 0
			     OR instr(lower(p.name), lower(?)) > 0
			     OR instr(lower(a.id), lower(?)) > 0
			  ORDER BY a.created_at DESC LIMIT 20`, scanSearchRow, q, q, q, q) {
			out = append(out, searchResult{
				Label: row[1] + " artifact", URL: "/ui/artifacts/" + url.PathEscape(row[0]),
				Kind: "Artifact", Detail: row[2] + " · " + row[3],
			})
		}
		for _, row := range queryRows(ctx, u.deps.Store.DB(),
			`SELECT j.id, j.state, COALESCE(t.title, ''), COALESCE(p.name, '')
			  FROM jobs j
			  LEFT JOIN tasks t ON t.id = j.task_id
			  LEFT JOIN projects p ON p.id = j.project_id
			  WHERE instr(lower(j.id), lower(?)) > 0
			     OR instr(lower(j.state), lower(?)) > 0
			     OR instr(lower(t.title), lower(?)) > 0
			     OR instr(lower(p.name), lower(?)) > 0
			  ORDER BY j.updated_at DESC LIMIT 20`, scanSearchRow, q, q, q, q) {
			detail := row[1]
			if row[2] != "" {
				detail += " · " + row[2]
			}
			if row[3] != "" {
				detail += " · " + row[3]
			}
			out = append(out, searchResult{
				Label: "Run " + short(row[0]), URL: "/ui/jobs/" + url.PathEscape(row[0]),
				Kind: "Run", Detail: detail,
			})
		}
		// Artifact bytes are files on disk, not an FTS table. Scan a bounded
		// recent set so a phrase search finds content, not only kind/status.
		if u.deps.Artifacts != nil && len(q) >= 3 {
			seen := map[string]bool{}
			for _, r := range out {
				seen[r.URL] = true
			}
			arts, _ := u.deps.Artifacts.ListRecent(ctx, 100)
			matches := 0
			for _, a := range arts {
				if matches >= 20 {
					break
				}
				url := "/ui/artifacts/" + url.PathEscape(a.ID)
				if seen[url] {
					continue
				}
				blob, err := u.deps.Artifacts.ReadContent(ctx, a.ID)
				if err != nil || len(blob) > 2<<20 {
					continue
				}
				if snippet, ok := contentSnippet(string(blob), q); ok {
					seen[url] = true
					out = append(out, searchResult{
						Label: string(a.Kind) + " v" + itoa(a.Version), URL: url,
						Kind: "Artifact content", Detail: snippet,
					})
					matches++
				}
			}
		}
		if events, err := u.deps.Store.QueryEvents(ctx, store.EventFilter{
			Query: q, Desc: true, Limit: 20,
		}); err == nil {
			for _, e := range events {
				out = append(out, searchResult{
					Label: eventName(e.DataJSON), URL: "/ui/events?q=" + url.QueryEscape(q),
					Kind: "Event", Detail: prettyEvent(e.DataJSON),
				})
			}
		}
	}
	if len(out) > 100 {
		out = out[:100]
	}
	return out
}

// contentSnippet returns a short, single-line window around a content match.
// The match is case-insensitive; multi-megabyte files are skipped by the
// caller rather than copied into a search result.
func contentSnippet(text, query string) (string, bool) {
	lower := strings.ToLower(text)
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return "", false
	}
	i := strings.Index(lower, needle)
	if i < 0 {
		return "", false
	}
	start, end := i-60, i+len(needle)+60
	if start < 0 {
		start = 0
	}
	if end > len(text) {
		end = len(text)
	}
	s := strings.ReplaceAll(text[start:end], "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ToValidUTF8(s, "…")
	if start > 0 {
		s = "… " + s
	}
	if end < len(text) {
		s += " …"
	}
	return s, true
}

// scanSearchRow reads every column as a nullable string so the search queries
// can share one scan helper and tolerate LEFT JOIN nulls.
func scanSearchRow(rows *sql.Rows) ([]string, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	holders := make([]sql.NullString, len(cols))
	ptrs := make([]any, len(cols))
	for i := range holders {
		ptrs[i] = &holders[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	out := make([]string, len(cols))
	for i := range holders {
		if holders[i].Valid {
			out[i] = holders[i].String
		}
	}
	return out, nil
}

func short(s string) string {
	if len(s) > 10 {
		return s[:8] + "…"
	}
	return s
}
