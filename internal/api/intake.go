package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/tcs76321/athanor/internal/intake"
	"github.com/tcs76321/athanor/internal/project"
)

// intakeIntentRequest is the wire shape for PATCH /projects/{id}/intent.
type intakeIntentRequest struct {
	Name           string   `json:"name"`
	Goal           string   `json:"goal"`
	RepositoryPath string   `json:"repository_path"`
	Boundaries     string   `json:"boundaries"`
	Sources        []string `json:"sources"`
	OnRamp         string   `json:"onramp"`
}

func (a *API) requireIntake(w http.ResponseWriter) bool {
	if a.intake == nil {
		writeError(w, http.StatusServiceUnavailable, "guided intake is not configured")
		return false
	}
	return true
}

func (a *API) handleIntakeState(w http.ResponseWriter, r *http.Request) {
	if !a.requireIntake(w) {
		return
	}
	st, err := a.intake.State(r.Context(), r.PathValue("id"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, project.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (a *API) handleIntakeIntent(w http.ResponseWriter, r *http.Request) {
	if !a.requireIntake(w) {
		return
	}
	var req intakeIntentRequest
	if !decodeBody(w, r, &req) {
		return
	}
	p, err := a.intake.SaveIntent(r.Context(), r.PathValue("id"), project.Intent{
		Name: req.Name, Goal: req.Goal, RepositoryPath: req.RepositoryPath,
		Boundaries: req.Boundaries, Sources: req.Sources, OnRamp: req.OnRamp,
	})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, project.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) handleIntakeOnRamp(w http.ResponseWriter, r *http.Request) {
	if !a.requireIntake(w) {
		return
	}
	var req struct {
		OnRamp string `json:"onramp"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.OnRamp) == "" {
		writeError(w, http.StatusBadRequest, "onramp must be direct or explore")
		return
	}
	if err := a.intake.SetOnRamp(r.Context(), r.PathValue("id"), req.OnRamp); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"onramp": req.OnRamp})
}

func (a *API) handleIntakeDirections(w http.ResponseWriter, r *http.Request) {
	if !a.requireIntake(w) {
		return
	}
	if err := a.intake.StartDirections(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "generating"})
}

func (a *API) handleIntakeSelectDirection(w http.ResponseWriter, r *http.Request) {
	if !a.requireIntake(w) {
		return
	}
	if err := a.intake.SelectDirection(r.Context(), r.PathValue("id"), r.PathValue("option")); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"selected": r.PathValue("option")})
}

func (a *API) handleIntakePlans(w http.ResponseWriter, r *http.Request) {
	if !a.requireIntake(w) {
		return
	}
	var req struct {
		Mode string `json:"mode"`
	}
	if r.ContentLength != 0 {
		if !decodeBody(w, r, &req) {
			return
		}
	}
	if err := a.intake.StartPlans(r.Context(), r.PathValue("id"), req.Mode); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "generating"})
}

func (a *API) handleIntakeSelectPlan(w http.ResponseWriter, r *http.Request) {
	if !a.requireIntake(w) {
		return
	}
	if err := a.intake.SelectPlan(r.Context(), r.PathValue("id"), r.PathValue("candidate")); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"selected": r.PathValue("candidate")})
}

func (a *API) handleIntakeApprove(w http.ResponseWriter, r *http.Request) {
	if !a.requireIntake(w) {
		return
	}
	var req struct {
		Plan string `json:"plan"`
	}
	if r.ContentLength != 0 {
		if !decodeBody(w, r, &req) {
			return
		}
	}
	res, err := a.intake.Approve(r.Context(), r.PathValue("id"), req.Plan)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, intake.ErrAlreadyApproved) {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	tasks := make([]decompositionTask, 0, len(res.Tasks))
	for _, t := range res.Tasks {
		tasks = append(tasks, decompositionTask{
			ID: t.ID, ParentID: t.ParentID, Title: t.Title, Status: t.Status,
			DependsOn: t.DependsOn, Criteria: t.Criteria,
		})
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"goal_id": res.GoalID, "plan": res.PlanID,
		"task_ids": taskIDs(res.Tasks), "job_ids": res.JobIDs,
		"tasks": tasks,
	})
}

func taskIDs(tasks []project.Task) []string {
	out := make([]string, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}
