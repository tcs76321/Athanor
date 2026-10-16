package api

import (
	"errors"
	"net/http"

	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/scheduler"
)

// handleTaskReset is the known-issues H8 operator recovery route
// (POST /tasks/{id}/reset): it returns a blocked task and its transitively
// blocked dependents to `pending` and grants the task a fresh retry window,
// so a DAG stalled by an exhausted leaf is recoverable without manual
// database edits. A task that is not `blocked` is a 409 (ErrNotBlocked); an
// unknown task is 404.
func (a *API) handleTaskReset(w http.ResponseWriter, r *http.Request) {
	if a.resetter == nil {
		writeError(w, http.StatusServiceUnavailable, "task reset is not configured")
		return
	}
	id := r.PathValue("id")
	started, err := a.resetter.Reset(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, scheduler.ErrNotBlocked):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, project.ErrNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if started == nil {
		started = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_ids": started})
}
