package api

import (
	"errors"
	"net/http"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
)

type goalRequest struct {
	Goal     string   `json:"goal"`
	Criteria []string `json:"acceptance_criteria"`
}

type goalResponse struct {
	TaskID string `json:"task_id"`
	JobID  string `json:"job_id"`
}

// handleGoalSubmit submits a goal to a project: it creates the goal, its
// M1 task, and a queued job, then hands the job to the engine. Frozen
// daemons reject new work (§22.1).
func (a *API) handleGoalSubmit(w http.ResponseWriter, r *http.Request) {
	if a.freezer.Frozen() {
		writeError(w, http.StatusConflict,
			"daemon is frozen: no new work is accepted (unfreeze with a reason first, §22.2)")
		return
	}
	var req goalRequest
	if !decodeBody(w, r, &req) {
		return
	}
	task, err := a.projects.SubmitGoal(r.Context(), r.PathValue("id"), req.Goal, req.Criteria)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, project.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	j, err := a.jobs.Create(r.Context(), task.ID, task.ProjectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.engine.Enqueue(j.ID)
	writeJSON(w, http.StatusCreated, goalResponse{TaskID: task.ID, JobID: j.ID})
}

func (a *API) handleArtifacts(w http.ResponseWriter, r *http.Request) {
	list, err := a.artifacts.ListByProject(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, art := range list {
		out = append(out, map[string]any{
			"id": art.ID, "kind": string(art.Kind), "version": art.Version,
			"status": string(art.Status), "task_id": art.TaskID, "job_id": art.JobID,
			"content_hash": art.ContentHash, "created_at": art.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"artifacts": out})
}

// handleExport is the M4-T4 manual-export route. The
// `athanor export -artifact <id>` CLI calls this; the
// egress exporter's asynchronous poll also drives the
// same code path, so a manual export is identical in
// behavior to an automatic one. Returns 200 with the
// on-disk path on a clean export, 200 with `exported=false`
// on a no-op (already exported, or non-accepted artifact),
// 404 if the artifact does not exist, 422 if the scanners
// rejected the export, 503 if no exporter is wired.
func (a *API) handleExport(w http.ResponseWriter, r *http.Request) {
	if a.exporter == nil {
		writeError(w, http.StatusServiceUnavailable,
			"egress exporter not wired (airlock may be disabled)")
		return
	}
	if a.freezer.Frozen() {
		writeError(w, http.StatusConflict,
			"daemon is frozen: export is paused (unfreeze with a reason first, §22.2)")
		return
	}
	artifactID := r.PathValue("id")
	if artifactID == "" {
		writeError(w, http.StatusBadRequest, "artifact id is required")
		return
	}
	path, exported, err := a.exporter.ExportOne(r.Context(), artifactID)
	if err != nil {
		// artifact-not-found surfaces as a 404; the
		// other errors (subprocess hung, FS error) are
		// 500s with the error string for the operator.
		if isNotFoundErr(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":     path,
		"exported": exported,
	})
}

// isNotFoundErr maps a missing artifact or project to a 404 in
// handleExport. It matches the store sentinels with errors.Is rather than
// comparing error strings: the previous string comparison used the
// literal "artifact: not found", which never matched the real sentinel
// ("artifact not found", further wrapped by the exporter), so a missing
// artifact returned 500 instead of 404.
func isNotFoundErr(err error) bool {
	return errors.Is(err, artifact.ErrNotFound) || errors.Is(err, project.ErrNotFound)
}

func (a *API) handleJobGet(w http.ResponseWriter, r *http.Request) {
	j, err := a.jobs.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	resp := map[string]any{
		"id": j.ID, "state": string(j.State), "task_id": j.TaskID, "project_id": j.ProjectID,
		"recovery_flag": j.RecoveryFlag,
	}
	if j.PausedFrom != "" {
		resp["paused_from"] = string(j.PausedFrom)
	}
	if j.StartedAt != nil {
		resp["started_at"] = j.StartedAt
	}
	if j.FinishedAt != nil {
		resp["finished_at"] = j.FinishedAt
	}
	// The final artifact is the useful tail of a watch — link it directly.
	if j.State == job.StateCompleted {
		if p, perr := a.projects.Get(r.Context(), j.ProjectID); perr == nil {
			if art, aerr := a.artifacts.LatestForJob(r.Context(), j.ID, finalKind(p.Archetype)); aerr == nil {
				resp["artifact_id"] = art.ID
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) handleJobEvents(w http.ResponseWriter, r *http.Request) {
	events, err := a.db.QueryEvents(r.Context(), store.EventFilter{JobID: r.PathValue("id")})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(events))
	for _, e := range events {
		out = append(out, map[string]any{
			"id": e.ID, "ts": e.TS, "category": e.Category, "level": e.Level, "data": e.DataJSON,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}
