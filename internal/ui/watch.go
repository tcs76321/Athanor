package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/firstuse"
	"github.com/tcs76321/athanor/internal/interruptions"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/store"
)

// This file owns the job watch view: the timeline, failure explanation, live
// token/event stream, operator note, and pause/resume/cancel/retry controls.

func (u *UI) handleWatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	j, err := u.deps.Jobs.Get(ctx, id)
	if err != nil {
		u.fail(w, err)
		return
	}
	arts, err := u.deps.Artifacts.ListByJob(ctx, id)
	if err != nil {
		u.fail(w, err)
		return
	}
	var notes []interruptions.Note
	if u.deps.Interruptions != nil {
		notes, _ = u.deps.Interruptions.List(ctx, id)
	}
	var evals []evaluation.Record
	if u.deps.Eval != nil {
		evals, _ = u.deps.Eval.ListByJob(ctx, id)
	}
	// Newest-first with a bound: the watch page does not scan a long job's
	// whole event history just to display the last hundred rows.
	events, err := u.deps.Store.QueryEvents(ctx, store.EventFilter{JobID: id, Desc: true, Limit: 100})
	if err != nil {
		u.fail(w, err)
		return
	}
	projectName := j.ProjectID
	if p, err := u.deps.Projects.Get(ctx, j.ProjectID); err == nil {
		projectName = p.Name
	}
	phase := j.State
	if phase == job.StatePaused && j.PausedFrom != "" {
		phase = j.PausedFrom
	}
	if phase == job.StateAwaitingApproval && j.AwaitingFrom != "" {
		phase = j.AwaitingFrom
	}
	failureEvent, failure := failureReason(events)
	lastID := int64(0)
	if len(events) > 0 {
		lastID = events[0].ID
	}
	u.render(w, "Watch "+id, watchBody, map[string]any{
		"Job": j, "Artifacts": arts, "Notes": notes, "Events": events,
		"ProjectName": projectName, "Phases": watchPhases, "Phase": string(phase),
		"PhaseIndex": phaseIndex(phase), "LastEvent": lastEventName(events),
		"Evaluations": evals, "FailureEvent": failureEvent, "Failure": failure,
		"CanPause": job.CanTransition(j.State, job.StatePaused), "LastEventID": lastID,
		"Timeline": u.phaseTimeline(ctx, id),
	})
}

// failureReason finds the newest terminal failure event and returns its name
// and human reason. The job row does not carry an error column, so the event
// log is the source of truth; the watch page promotes the latest failure so a
// failed run explains itself without a trip to the raw trace.
func failureReason(events []store.EventRecord) (string, string) {
	for _, ev := range events {
		var d map[string]any
		if json.Unmarshal([]byte(ev.DataJSON), &d) != nil {
			continue
		}
		name, _ := d["event"].(string)
		switch name {
		case "job_failed":
			if s, _ := d["error"].(string); s != "" {
				return name, s
			}
			return name, "the phase failed"
		case "job_panicked":
			if s, _ := d["panic"].(string); s != "" {
				return name, s
			}
			return name, "the phase panicked"
		case "job_cancelled":
			return name, "the operator cancelled this run"
		}
	}
	return "", ""
}

// watchPhases is the §8.1 working sequence shown as the watch stepper.
var watchPhases = []job.State{
	job.StateContextBuilding, job.StatePlanning, job.StateDiverging,
	job.StateEvaluating, job.StateReflecting, job.StateSynthesizing, job.StateComparing,
}

func phaseIndex(s job.State) int {
	for i, p := range watchPhases {
		if p == s {
			return i
		}
	}
	if s == job.StateCompleted {
		return len(watchPhases)
	}
	return -1
}

func lastEventName(events []store.EventRecord) string {
	if len(events) == 0 {
		return ""
	}
	// Events arrive newest-first from the query in handleWatch's caller.
	return prettyEvent(events[0].DataJSON)
}

// handleJobStream is the watch view's SSE stream: token chunks plus new job
// events.
func (u *UI) handleJobStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	jobID := r.PathValue("id")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	tokens, cancel := u.hub.Subscribe(jobID)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var lastID int64
	// Prefer the page's own last event so anything written between the page
	// render and this connection is delivered; fall back to the current tail
	// so a bare stream does not replay the job's history. LatestEventID is an
	// index lookup, not a scan (M9).
	if after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64); after > 0 {
		lastID = after
	} else {
		lastID, _ = u.deps.Store.LatestEventID(r.Context(), store.EventFilter{JobID: jobID})
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-u.done:
			return
		case tok := <-tokens:
			writeSSE(w, "token", mustJSON(tok))
			flusher.Flush()
		case <-ticker.C:
			for _, ev := range u.newEvents(r.Context(), jobID, &lastID) {
				writeSSE(w, "event", mustJSON(map[string]any{
					"id": ev.ID, "ts": ev.TS.Format(time.RFC3339), "category": ev.Category, "data": ev.DataJSON,
				}))
			}
			flusher.Flush()
		}
	}
}

func (u *UI) handleNote(w http.ResponseWriter, r *http.Request) {
	if u.deps.Interruptions == nil {
		u.fail(w, fmt.Errorf("interruption queue is not configured"))
		return
	}
	_ = r.ParseForm()
	note := strings.TrimSpace(r.FormValue("note"))
	if note == "" {
		u.fail(w, fmt.Errorf("note must not be empty"))
		return
	}
	if _, err := u.deps.Interruptions.Add(r.Context(), r.PathValue("id"), note); err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui/jobs/"+r.PathValue("id"), http.StatusSeeOther)
}

// handleControl pauses, resumes, cancels, or retries a job.
func (u *UI) handleControl(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	j, err := u.deps.Jobs.Get(ctx, id)
	if err != nil {
		u.fail(w, err)
		return
	}
	_ = r.ParseForm()
	switch r.FormValue("action") {
	case "pause":
		if !job.CanTransition(j.State, job.StatePaused) {
			u.fail(w, fmt.Errorf("job %s cannot pause from %s", id, j.State))
			return
		}
		if _, err := u.deps.Jobs.Transition(ctx, id, job.StatePaused); err != nil {
			u.fail(w, err)
			return
		}
	case "resume":
		if j.State != job.StatePaused {
			u.fail(w, fmt.Errorf("job %s is not paused", id))
			return
		}
		if _, err := u.deps.Jobs.Transition(ctx, id, j.PausedFrom); err != nil {
			u.fail(w, err)
			return
		}
		if u.deps.Engine != nil {
			u.deps.Engine.Enqueue(id)
		}
	case "cancel":
		if j.State.Terminal() {
			return
		}
		// Prefer the engine's cancellation path: it cancels the phase context
		// and commits `cancelled` from the job goroutine, so a long LLM or pod
		// call stops instead of continuing against an already-cancelled row.
		if c, ok := u.deps.Engine.(interface{ Cancel(string) bool }); ok && c.Cancel(id) {
			break
		}
		if _, err := u.deps.Jobs.Transition(ctx, id, job.StateCancelled); err != nil {
			u.fail(w, err)
			return
		}
		if f, ok := u.deps.Engine.(interface {
			FinalizeTerminal(context.Context, string)
		}); ok {
			f.FinalizeTerminal(ctx, id)
		}
	case "retry":
		if !j.State.Terminal() {
			u.fail(w, fmt.Errorf("job %s is not terminal", id))
			return
		}
		next, err := firstuse.StartTaskJob(ctx, u.deps.Store, u.deps.Projects, u.deps.Jobs, u.deps.Engine, j.TaskID, j.ProjectID)
		if err != nil {
			u.fail(w, err)
			return
		}
		http.Redirect(w, r, "/ui/jobs/"+next, http.StatusSeeOther)
		return
	default:
		u.fail(w, fmt.Errorf("unknown control action"))
		return
	}
	http.Redirect(w, r, "/ui/jobs/"+id, http.StatusSeeOther)
}

// handleDiff renders a line diff between an artifact and the version it
// superseded.
// handleArtifact renders one artifact's full content. The project page links
// here; without it the human could see statuses and diffs but not the accepted
// bytes.
