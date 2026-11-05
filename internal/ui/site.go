package ui

import (
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
	{"Work", "Home", "/ui", "What needs you and what the machine is doing"},
	{"Work", "Inbox", "/ui/notifications", "Approvals, reviews, alarms, failures"},
	{"Work", "Projects", "/ui/projects", "All projects and their work history"},
	{"Work", "New project", "/ui/projects/new", "Start a project and its onboarding"},
	{"Work", "Review queue", "/ui/reviews", "Blinded human review of accepted work"},
	{"Work", "Library", "/ui/artifacts", "All artifacts, versions, diffs, exports"},
	{"Insights", "Statistics", "/ui/statistics", "Outcomes, tokens, strategy insights"},
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
		rows, err := u.deps.Store.DB().QueryContext(r.Context(),
			`SELECT id, name, goal FROM projects
			  WHERE instr(lower(name), lower(?)) > 0 OR instr(lower(goal), lower(?)) > 0
			  ORDER BY updated_at DESC LIMIT 20`, q, q)
		if err == nil {
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var id, name, goal string
				if rows.Scan(&id, &name, &goal) == nil {
					out = append(out, searchResult{
						Label: name, URL: "/ui/projects/" + url.PathEscape(id),
						Kind: "Project", Detail: goal,
					})
				}
			}
		}

		rows, err = u.deps.Store.DB().QueryContext(r.Context(),
			`SELECT id, project_id, kind, status FROM artifacts
			  WHERE instr(lower(kind), lower(?)) > 0 OR instr(lower(status), lower(?)) > 0
			  ORDER BY created_at DESC LIMIT 20`, q, q)
		if err == nil {
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var id, projectID, kind, status string
				if rows.Scan(&id, &projectID, &kind, &status) == nil {
					out = append(out, searchResult{
						Label: kind + " artifact", URL: "/ui/artifacts/" + url.PathEscape(id),
						Kind: "Artifact", Detail: status + " · " + projectID,
					})
				}
			}
		}

		rows, err = u.deps.Store.DB().QueryContext(r.Context(),
			`SELECT id, project_id, state FROM jobs
			  WHERE instr(lower(id), lower(?)) > 0 OR instr(lower(state), lower(?)) > 0
			  ORDER BY updated_at DESC LIMIT 20`, q, q)
		if err == nil {
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var id, projectID, state string
				if rows.Scan(&id, &projectID, &state) == nil {
					out = append(out, searchResult{
						Label: "Job " + short(id), URL: "/ui/jobs/" + url.PathEscape(id),
						Kind: "Job", Detail: state,
					})
				}
			}
		}

		if events, err := u.deps.Store.QueryEvents(r.Context(), store.EventFilter{
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

func short(s string) string {
	if len(s) > 10 {
		return s[:8] + "…"
	}
	return s
}
