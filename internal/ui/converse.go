package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/tcs76321/athanor/internal/architect"
	"github.com/tcs76321/athanor/internal/firstuse"
	"github.com/tcs76321/athanor/internal/ids"
)

// converseSession is a durable onboarding conversation. It lives in
// system_state while the proposal is still a draft; approval moves the frame
// and graph into a project cycle.
type converseSession struct {
	ID       string              `json:"id"`
	Messages []architect.Message `json:"messages"`
	Proposal *architect.Proposal `json:"proposal,omitempty"`
	Error    string              `json:"error,omitempty"`
}

func (u *UI) loadConversation(id string) converseSession {
	if id == "" {
		return converseSession{ID: ids.New()}
	}
	raw, ok := u.loadSystemState(context.Background(), "architect:session:"+id)
	if !ok {
		return converseSession{ID: id}
	}
	var s converseSession
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return converseSession{ID: id}
	}
	s.ID = id
	return s
}

func (u *UI) saveConversation(s converseSession) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return u.saveSystemState(context.Background(), "architect:session:"+s.ID, string(raw))
}

func (u *UI) handleNewConversation(w http.ResponseWriter, r *http.Request) {
	s := u.loadConversation(r.URL.Query().Get("session"))
	u.render(w, "New project", converseBody, map[string]any{"Session": s})
}

func (u *UI) handleNewMessage(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		u.fail(w, err)
		return
	}
	s := u.loadConversation(strings.TrimSpace(r.FormValue("session")))
	text := strings.TrimSpace(r.FormValue("message"))
	if text == "" {
		http.Redirect(w, r, "/ui/new?session="+s.ID, http.StatusSeeOther)
		return
	}
	if len(s.Messages) > 40 {
		s.Messages = s.Messages[len(s.Messages)-40:]
	}
	s.Messages = append(s.Messages, architect.Message{Role: "user", Content: text})
	svc := u.deps.Architect
	if svc == nil {
		svc = architect.New(nil)
	}
	p, err := svc.Propose(r.Context(), s.Messages)
	if err != nil {
		s.Error = err.Error()
	} else {
		s.Proposal = &p
		s.Error = ""
		reply := "I have a draft frame and first-cycle graph. Review it below."
		if len(p.Questions) > 0 {
			reply = "Before I can approve a frame, I need: " + strings.Join(p.Questions, "; ")
		}
		s.Messages = append(s.Messages, architect.Message{Role: "assistant", Content: reply})
	}
	if err := u.saveConversation(s); err != nil {
		s.Error = err.Error()
	}
	http.Redirect(w, r, "/ui/new?session="+s.ID, http.StatusSeeOther)
}

func (u *UI) handleNewApprove(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		u.fail(w, err)
		return
	}
	s := u.loadConversation(strings.TrimSpace(r.FormValue("session")))
	if s.Proposal == nil {
		s.Error = "There is no proposal to approve yet."
		u.render(w, "New project", converseBody, map[string]any{"Session": s})
		return
	}
	if u.deps.Cycles == nil {
		s.Error = "The cycle service is not configured."
		u.render(w, "New project", converseBody, map[string]any{"Session": s})
		return
	}
	deps := firstuse.Deps{
		Projects: u.deps.Projects, Events: u.deps.Store, Freezer: u.deps.Freezer,
	}
	if u.deps.Indexer != nil {
		deps.Indexer = uiPreindexer{inner: u.deps.Indexer}
	}
	draft, err := firstuse.CreateDraft(r.Context(), deps, u.deps.Cycles, firstuse.ApproveInput{
		Frame: s.Proposal.Frame, Graph: s.Proposal.Graph,
	})
	if err != nil {
		s.Error = "Could not create the draft: " + err.Error()
		u.render(w, "New project", converseBody, map[string]any{"Session": s})
		return
	}
	http.Redirect(w, r, "/ui/projects/"+draft.Project.ID+"/canvas?cycle="+draft.Cycle.ID, http.StatusSeeOther)
}

const converseBody = `
<h1>New project</h1>
<p class="sub">Tell Athanor what you want to make. It asks only what it needs, proposes a frame and a first-cycle graph, and opens the canvas after you approve.</p>

<div class="card">
  <div class="eyebrow">Conversation</div>
  {{range .Session.Messages}}
  <div class="converse-msg">
    <div class="eyebrow">{{.Role}}</div>
    <p>{{.Content}}</p>
  </div>
  {{end}}
  {{if .Session.Error}}<div class="notice bad">{{.Session.Error}}</div>{{end}}
  <form method="post" action="/ui/new/message">
    <input type="hidden" name="session" value="{{.Session.ID}}">
    <label class="field"><span>Your message</span>
      <textarea name="message" rows="4" required placeholder="e.g. I want to make a game but I don't know what yet. Research the space and give me three directions."></textarea>
    </label>
    <div class="actions">
      <button class="btn primary">Send</button>
      <a class="btn secondary" href="/ui/projects/new">Use the advanced form</a>
    </div>
  </form>
</div>

{{with .Session.Proposal}}
<div class="card">
  <div class="eyebrow">Proposed frame</div>
  <h2>{{.Frame.Name}}</h2>
  <p>{{.Frame.Goal}}</p>
  <div class="grid">
    <div><div class="l muted">First cycle</div><div><span class="pill">{{.Frame.FirstCycle}}</span></div></div>
    <div><div class="l muted">Capabilities</div><div>{{join .Frame.Capabilities}}</div></div>
    <div><div class="l muted">Repository</div><div class="mono wrap">{{if .Frame.Repository}}{{.Frame.Repository}}{{else}}—{{end}}</div></div>
    <div><div class="l muted">Review policy</div><div>{{.Frame.ReviewPolicy}}</div></div>
  </div>
  {{if .Frame.Acceptance}}<h3>Acceptance</h3><ul>{{range .Frame.Acceptance}}<li>{{.}}</li>{{end}}</ul>{{end}}
  {{if .Frame.Inferred}}<p class="muted small">Inferred and open to correction: {{join .Frame.Inferred}}</p>{{end}}

  <h3>First cycle graph</h3>
  <ul>
    {{range .Graph.Blocks}}<li><strong>{{if .Title}}{{.Title}}{{else}}{{.Kind}}{{end}}</strong> <span class="mono muted">{{.Kind}}</span></li>{{end}}
  </ul>
  <p class="muted small">{{len .Graph.Edges}} connection(s). The canvas opens for editing after approval.</p>

  {{if .Questions}}
  <div class="notice"><strong>Before I can approve:</strong><ul>{{range .Questions}}<li>{{.}}</li>{{end}}</ul></div>
  {{end}}
  {{if .Degraded}}<div class="notice bad">{{.Notes}}</div>{{end}}

  {{if not .Questions}}
  <form method="post" action="/ui/new/approve">
    <input type="hidden" name="session" value="{{$.Session.ID}}">
    <div class="actions"><button class="btn primary">Approve and open canvas</button></div>
  </form>
  {{end}}
</div>
{{end}}
`
