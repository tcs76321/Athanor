package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/decompose"
	"github.com/tcs76321/athanor/internal/hitl"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
)

type goalRequest struct {
	Goal     string   `json:"goal"`
	Criteria []string `json:"acceptance_criteria"`
}

type goalResponse struct {
	TaskID string `json:"task_id,omitempty"`
	JobID  string `json:"job_id,omitempty"`
	// M6-T2 (ADR-0033): present only on the decompose-then-schedule path.
	GoalID  string   `json:"goal_id,omitempty"`
	Persona string   `json:"persona,omitempty"`
	TaskIDs []string `json:"task_ids,omitempty"`
	JobIDs  []string `json:"job_ids,omitempty"`
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
	// M6-T2 (ADR-0033 §5): with dag_decomposition enabled, submission
	// decomposes the goal and schedules its ready tasks instead of
	// creating the M1 single task.
	if a.dagScheduling && a.decomposer != nil && a.scheduler != nil {
		a.submitDecomposedGoal(w, r, req)
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

// submitDecomposedGoal is the M6-T2 gated submit path: decompose, then
// schedule the ready leaves. A decomposition rejection persists nothing
// (CreateDAG is atomic); a scheduler failure surfaces as 500.
func (a *API) submitDecomposedGoal(w http.ResponseWriter, r *http.Request, req goalRequest) {
	res, err := a.decomposer.Decompose(r.Context(), r.PathValue("id"), req.Goal, req.Criteria)
	if err != nil {
		writeError(w, decomposeStatus(err), err.Error())
		return
	}
	jobs, err := a.scheduler.Start(r.Context(), res.GoalID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := goalResponse{GoalID: res.GoalID, Persona: res.Persona, JobIDs: jobs}
	for _, t := range res.Tasks {
		resp.TaskIDs = append(resp.TaskIDs, t.ID)
	}
	if len(res.Tasks) > 0 {
		resp.TaskID = res.Tasks[0].ID
	}
	if len(jobs) > 0 {
		resp.JobID = jobs[0]
	}
	writeJSON(w, http.StatusCreated, resp)
}

// decompositionTask is the wire shape of one task in a decomposition
// response. It is deliberately narrower than project.Task: the API exposes
// the graph structure, not persistence internals.
type decompositionTask struct {
	ID        string   `json:"id"`
	ParentID  string   `json:"parent_id,omitempty"`
	Title     string   `json:"title"`
	Status    string   `json:"status"`
	DependsOn []string `json:"depends_on"`
	Criteria  []string `json:"acceptance_criteria"`
}

type decompositionResponse struct {
	GoalID  string              `json:"goal_id"`
	Persona string              `json:"persona"`
	Tasks   []decompositionTask `json:"tasks"`
}

func toDecompositionTasks(tasks []project.Task) []decompositionTask {
	out := make([]decompositionTask, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, decompositionTask{
			ID: t.ID, ParentID: t.ParentID, Title: t.Title, Status: t.Status,
			DependsOn: t.DependsOn, Criteria: t.Criteria,
		})
	}
	return out
}

// handleDecompose is the M6-T1 explicit decomposition entry point
// (ADR-0032). It runs the decomposer synchronously and returns the
// persisted graph. 422 carries a machine-readable rejection reason; 409 is
// reserved for the frozen kill switch; 503 means no decomposer is wired.
func (a *API) handleDecompose(w http.ResponseWriter, r *http.Request) {
	if a.freezer.Frozen() {
		writeError(w, http.StatusConflict,
			"daemon is frozen: no new work is accepted (unfreeze with a reason first, §22.2)")
		return
	}
	if a.decomposer == nil {
		writeError(w, http.StatusServiceUnavailable, "DAG decomposition is not configured")
		return
	}
	var req goalRequest
	if !decodeBody(w, r, &req) {
		return
	}
	res, err := a.decomposer.Decompose(r.Context(), r.PathValue("id"), req.Goal, req.Criteria)
	if err != nil {
		writeError(w, decomposeStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, decompositionResponse{
		GoalID: res.GoalID, Persona: res.Persona, Tasks: toDecompositionTasks(res.Tasks),
	})
}

// handleProjectTasks lists a project's tasks (M6-T1 DAG inspection).
func (a *API) handleProjectTasks(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := a.projects.Get(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	tasks, err := a.projects.TasksByProject(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": toDecompositionTasks(tasks)})
}

// decomposeStatus maps a decomposer error to an HTTP status.
func decomposeStatus(err error) int {
	switch {
	case errors.Is(err, project.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, decompose.ErrRejected), errors.Is(err, decompose.ErrInfeasible):
		return http.StatusUnprocessableEntity
	default:
		return http.StatusInternalServerError
	}
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

// hitlResponse is the JSON shape of a HITL request. It is deliberately
// explicit (rather than serializing hitl.Request) so the field names are
// stable snake_case and payload is passed through verbatim.
type hitlResponse struct {
	ID           string          `json:"id"`
	ProjectID    string          `json:"project_id,omitempty"`
	JobID        string          `json:"job_id,omitempty"`
	Type         string          `json:"type"`
	Severity     string          `json:"severity"`
	Status       string          `json:"status"`
	Payload      json.RawMessage `json:"payload,omitempty"`
	DecisionNote string          `json:"decision_note,omitempty"`
	ExpiresAt    *time.Time      `json:"expires_at,omitempty"`
	DecidedAt    *time.Time      `json:"decided_at,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

func toHITLResponse(r hitl.Request) hitlResponse {
	payload := json.RawMessage(r.PayloadJSON)
	if len(payload) == 0 || !json.Valid(payload) {
		payload = json.RawMessage(`{}`)
	}
	return hitlResponse{
		ID: r.ID, ProjectID: r.ProjectID, JobID: r.JobID, Type: r.Type,
		Severity: r.Severity, Status: r.Status, Payload: payload,
		DecisionNote: r.DecisionNote, ExpiresAt: r.ExpiresAt, DecidedAt: r.DecidedAt,
		CreatedAt: r.CreatedAt,
	}
}

// handleHITLList lists pending HITL requests (M6-T4, ADR-0035).
func (a *API) handleHITLList(w http.ResponseWriter, r *http.Request) {
	if a.hitl == nil {
		writeError(w, http.StatusServiceUnavailable, "HITL queue is not configured")
		return
	}
	reqs, err := a.hitl.Pending(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]hitlResponse, 0, len(reqs))
	for _, req := range reqs {
		out = append(out, toHITLResponse(req))
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": out})
}

type hitlDecisionRequest struct {
	Action string `json:"action"`
	Note   string `json:"note"`
	// DeferFor is a Go duration string ("1h"); required for a defer.
	DeferFor string `json:"defer_for"`
}

// handleHITLDecision applies approve/reject/defer to a pending request.
func (a *API) handleHITLDecision(w http.ResponseWriter, r *http.Request) {
	if a.hitl == nil {
		writeError(w, http.StatusServiceUnavailable, "HITL queue is not configured")
		return
	}
	var req hitlDecisionRequest
	if !decodeBody(w, r, &req) {
		return
	}
	var deferFor time.Duration
	if req.DeferFor != "" {
		d, err := time.ParseDuration(req.DeferFor)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid defer_for: "+err.Error())
			return
		}
		deferFor = d
	}
	res, err := a.hitl.Decide(r.Context(), r.PathValue("id"), req.Action, req.Note, deferFor)
	if err != nil {
		writeError(w, hitlStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toHITLResponse(res))
}

func hitlStatus(err error) int {
	switch {
	case errors.Is(err, hitl.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, hitl.ErrNotPending):
		return http.StatusConflict
	case errors.Is(err, hitl.ErrUnknownAction):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
