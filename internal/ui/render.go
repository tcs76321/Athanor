package ui

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
)

// This file owns shared rendering: the page shell and CSP, the template cache,
// the status strip, error pages, template helpers, SSE frames, and the
// human-readable event line.

// listProjects reads every project. project.Repo has no List; the UI queries
// the projects table directly (read-only), which is ordinary for a view.
//
// The store holds a single connection (ADR-0027), so the id rows must be
// drained and closed before the per-id Get queries, or the Get waits forever
// for the connection the open rows hold.
func (u *UI) listProjects(ctx context.Context) ([]project.Project, error) {
	rows, err := u.deps.Store.DB().QueryContext(ctx,
		`SELECT id FROM projects
		 ORDER BY CASE priority WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END,
		          created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := make([]project.Project, 0, len(ids))
	for _, id := range ids {
		p, err := u.deps.Projects.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// --- rendering ---

func (u *UI) render(w http.ResponseWriter, title, body string, data map[string]any) {
	u.renderStatus(w, http.StatusOK, title, body, data)
}

// statusStrip returns the shell-level freeze/active/waiting hints. It is
// cached briefly: the strip is a glance, not a live counter (the dashboard has
// the authoritative, refreshed views).
func (u *UI) statusStrip(ctx context.Context) (frozen bool, active, waiting int) {
	const ttl = 5 * time.Second
	u.statusMu.Lock()
	if !u.statusAt.IsZero() && time.Since(u.statusAt) < ttl {
		frozen, active, waiting = u.statusFroze, u.statusRun, u.statusWait
		u.statusMu.Unlock()
		return
	}
	u.statusMu.Unlock()

	if u.deps.Control != nil {
		frozen = u.deps.Control.Frozen()
	} else if u.deps.Freezer != nil {
		frozen = u.deps.Freezer.Frozen()
	}
	if u.deps.Jobs != nil {
		if jobs, err := u.deps.Jobs.Active(ctx); err == nil {
			active = len(jobs)
		}
	}
	if u.deps.HITL != nil {
		if reqs, err := u.deps.HITL.Pending(ctx); err == nil {
			waiting = len(reqs)
		}
	}

	u.statusMu.Lock()
	u.statusAt, u.statusFroze, u.statusRun, u.statusWait = time.Now(), frozen, active, waiting
	u.statusMu.Unlock()
	return frozen, active, waiting
}

// templateCache memoizes parsed page/partial templates. Re-parsing a constant
// body on every request allocated a new template and made a parse error a
// per-request panic.
var templateCache sync.Map // key string -> *template.Template

func cachedTemplate(kind, body string) *template.Template {
	key := kind + "\x00" + body
	if v, ok := templateCache.Load(key); ok {
		return v.(*template.Template)
	}
	t := template.Must(template.New(kind).Funcs(funcs).Parse(body))
	templateCache.Store(key, t)
	return t
}

// renderStatus renders the shared shell with an explicit status code. The
// header must be written after Content-Type is set and before the body.
func (u *UI) renderStatus(w http.ResponseWriter, status int, title, body string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["Title"] = title
	if _, ok := data["Runtime"]; !ok {
		data["Runtime"] = u.deps.Runtime
	}
	if _, ok := data["Version"]; !ok {
		data["Version"] = u.deps.Version
	}
	if _, ok := data["Query"]; !ok {
		data["Query"] = ""
	}
	if _, ok := data["StatusFrozen"]; !ok {
		frozen, active, waiting := u.statusStrip(context.Background())
		data["StatusFrozen"] = frozen
		data["StatusActive"] = active
		data["StatusWaiting"] = waiting
	}
	theme := u.Theme()
	data["Theme"] = theme
	data["ThemeLabel"] = themeLabel(theme)
	data["Density"] = u.Density()
	if reason := u.restartRequired(context.Background()); reason != "" {
		data["RestartRequired"] = reason
		data["RestartHint"] = u.restartHint()
	}
	if _, ok := data["DevRuntime"]; !ok {
		// The host process is a client/development surface (ADR-0078); show
		// the warning unless this is the supported Core Pod.
		data["DevRuntime"] = u.deps.Runtime != "" && !strings.EqualFold(u.deps.Runtime, "Core Pod")
	}
	t := cachedTemplate("page", document(body))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The shell has no inline script or style: everything is served from
	// /ui/assets. A strict CSP is therefore cheap and closes injection paths
	// that html/template escaping alone does not (e.g. injected <base>).
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.WriteHeader(status)
	if err := t.Execute(w, data); err != nil {
		slog.Error("ui: rendering page", "title", title, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// fail renders a request error with a status that matches what happened. Most
// handler errors are validation failures (400), but a missing artifact or job
// is a 404, and a cancelled request is not a client mistake. The raw error is
// logged for the operator and shown only when it is safe to act on; storage
// and driver errors are replaced with a generic message so schema details do
// not leak into the browser.
func (u *UI) fail(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, sql.ErrNoRows),
		errors.Is(err, artifact.ErrNotFound),
		errors.Is(err, job.ErrNotFound),
		strings.Contains(strings.ToLower(err.Error()), "not found"):
		status = http.StatusNotFound
	case errors.Is(err, context.Canceled):
		status = http.StatusRequestTimeout
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	case looksInternal(err):
		status = http.StatusInternalServerError
	}
	slog.Warn("ui: request failed", "status", status, "err", err)
	msg := err.Error()
	if status == http.StatusInternalServerError {
		msg = "The Core could not complete this request."
	}
	u.renderStatus(w, status, "Error", errorBody, map[string]any{"Error": msg})
}

// looksInternal reports whether an error is a storage/runtime failure rather
// than a validation message safe to show. The check is deliberately narrow: it
// catches driver text and wrapped SQL failures without hiding useful domain
// errors such as "choose a direction first".
func looksInternal(err error) bool {
	s := strings.ToLower(err.Error())
	for _, marker := range []string{
		"sql: ", "sqlite", "database is locked", "constraint failed",
		"no such table", "no such column", "scanning ", "beginning ",
		"committing ", "querying ",
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// renderPartial writes one template fragment (used by HTMX). It sets no page
// shell and no CSP headers: the fragment is swapped into an existing document
// whose policy is already in force.
func (u *UI) renderPartial(w http.ResponseWriter, body string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	t := cachedTemplate("partial", body)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.Execute(w, data); err != nil {
		slog.Error("ui: rendering partial", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// whenLabel renders a timestamp for a human: recent work is relative, today
// keeps the clock time, and older work gets a date. Future timestamps (clock
// skew) are treated as "just now".
func whenLabel(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	y, m, day := t.Date()
	ny, nm, nd := now.Date()
	if y == ny && m == nm && day == nd {
		return t.Format("15:04")
	}
	yy, ym, yd := now.AddDate(0, 0, -1).Date()
	if y == yy && m == ym && day == yd {
		return "Yesterday " + t.Format("15:04")
	}
	if y == ny {
		return t.Format("Jan 2 15:04")
	}
	return t.Format("Jan 2 2006")
}

var funcs = template.FuncMap{
	"short": func(s string) string {
		if len(s) > 12 {
			return s[:8] + "…"
		}
		return s
	},
	"ts": func(v any) string {
		switch t := v.(type) {
		case time.Time:
			return t.Format("15:04:05")
		case *time.Time:
			if t != nil {
				return t.Format("15:04:05")
			}
		}
		return ""
	},
	"join":     func(ss []string) string { return strings.Join(ss, ", ") },
	"mul":      func(a, b float64) float64 { return a * b },
	"mib":      func(n int) float64 { return float64(n) / 1024 },
	"markdown": renderMarkdown,
	"add":      func(a, b int) int { return a + b },
	"gib": func(n int64) string {
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	},
	"plural": func(n int, one, many string) string {
		if n == 1 {
			return one
		}
		return many
	},
	"resultLabel": func(s string) string {
		switch s {
		case "accepted_new":
			return "accepted — new winner"
		case "accepted_previous":
			return "accepted — previous kept"
		default:
			return s
		}
	},
	"when": func(v any) string {
		var t time.Time
		switch x := v.(type) {
		case time.Time:
			t = x
		case *time.Time:
			if x == nil {
				return ""
			}
			t = *x
		case string:
			// Stored timestamps are RFC3339 strings; a non-timestamp string
			// is returned unchanged so the helper cannot blank a label.
			parsed, err := time.Parse(time.RFC3339Nano, x)
			if err != nil {
				return x
			}
			t = parsed
		default:
			return ""
		}
		return whenLabel(t, time.Now())
	},
	"prettyEvent": prettyEvent,
	"hitlLabel":   hitlTypeLabel,
	"hitlFields":  hitlFields,
	"expiresIn":   func(t *time.Time) string { return expiryLabel(t, time.Now()) },
	"tojson": func(v any) string {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return ""
		}
		return string(b)
	},
	"priorityClass": func(v any) string {
		switch project.NormalizePriority(fmt.Sprint(v)) {
		case project.PriorityCritical:
			return "bad"
		case project.PriorityHigh:
			return "warn"
		case project.PriorityLow:
			return ""
		default:
			return "run"
		}
	},
	"roleLabel": func(s string) string {
		if s == "" {
			return ""
		}
		return strings.ToUpper(s[:1]) + s[1:]
	},
	"roleFunction": func(s string) string {
		switch s {
		case "main":
			return "Worker"
		case "tall":
			return "Architect"
		case "wide":
			return "Scout"
		case "security":
			return "Screener"
		case "alternative":
			return "Second opinion"
		default:
			return s
		}
	},
	"priorityLabel": func(v any) string {
		p := project.NormalizePriority(fmt.Sprint(v))
		if p == project.PriorityCritical {
			return "Critical"
		}
		return strings.ToUpper(p[:1]) + p[1:]
	},
	"stateClass": func(v any) string {
		s := fmt.Sprint(v)
		switch s {
		case "completed":
			return "ok"
		case "failed", "cancelled":
			return "bad"
		case "paused", "awaiting_approval":
			return "warn"
		case "queued":
			return ""
		default:
			return "run"
		}
	},
}

func writeSSE(w http.ResponseWriter, event, data string) {
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}

// prettyEvent renders one audit row as a compact, human-readable line. The raw
// JSON remains the source of truth; this is display only.
func prettyEvent(raw string) string {
	var d map[string]any
	if json.Unmarshal([]byte(raw), &d) != nil {
		return raw
	}
	name, _ := d["event"].(string)
	if name == "" {
		name = "event"
	}
	keys := []string{
		"phase", "state", "to", "winner", "result", "passed", "score",
		"reason", "model", "prompt_tokens", "completion_tokens",
		"reasoning_tokens", "decision", "unit", "iter", "candidates",
		"count", "error", "difficulty", "note",
	}
	var parts []string
	for _, k := range keys {
		v, ok := d[k]
		if !ok || v == nil || v == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		if len(parts) == 5 {
			break
		}
	}
	line := name
	if len(parts) > 0 {
		line += " · " + strings.Join(parts, " · ")
	}
	if len(line) > 220 {
		line = line[:220] + "…"
	}
	return line
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `""`
	}
	return string(b)
}
