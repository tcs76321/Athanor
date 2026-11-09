package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/tcs76321/athanor/internal/assist"
)

// contextRefiner is the optional richer assist surface. The production
// *assist.Service implements it; a test fake that only implements Refine keeps
// working through the fallback.
type contextRefiner interface {
	RefineContext(ctx context.Context, field, text, mode string, ac assist.Context) (assist.Result, error)
}

// handleAssist refines one field and returns a proposal. It never writes the
// field; the browser applies it and the user can revert.
func (u *UI) handleAssist(w http.ResponseWriter, r *http.Request) {
	if u.deps.Assist == nil {
		writeAssistError(w, http.StatusServiceUnavailable, "refinement is not configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req struct {
		Field            string            `json:"field"`
		Text             string            `json:"text"`
		Mode             string            `json:"mode"`
		ProjectID        string            `json:"project_id"`
		Context          map[string]string `json:"context"`
		Pass             int               `json:"pass"`
		Origin           string            `json:"origin"`
		PriorAssumptions []string          `json:"prior_assumptions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAssistError(w, http.StatusBadRequest, "invalid refinement request")
		return
	}
	ac := assist.Context{
		Runtime: u.deps.Runtime, Version: u.deps.Version, Fields: map[string]string{},
		Pass: req.Pass, Origin: req.Origin, PriorAssumptions: req.PriorAssumptions,
	}
	for k, v := range req.Context {
		k = strings.TrimSpace(k)
		if k == "" || strings.TrimSpace(v) == "" {
			continue
		}
		if len(v) > 2000 {
			v = v[:2000]
		}
		ac.Fields[k] = v
	}
	if req.ProjectID != "" && u.deps.Projects != nil {
		if p, err := u.deps.Projects.Get(r.Context(), req.ProjectID); err == nil {
			ac.ProjectName = p.Name
			ac.ProjectGoal = p.Goal
			ac.ProjectCapabilities = strings.Join(p.Capabilities, ", ")
			ac.RepositoryPath = p.RepositoryPath
			ac.Priority = p.Priority
			if tasks, terr := u.deps.Projects.TasksByProject(r.Context(), p.ID); terr == nil && len(tasks) > 0 {
				ac.Criteria = tasks[0].Criteria
			}
		}
	}
	var (
		res assist.Result
		err error
	)
	if cr, ok := u.deps.Assist.(contextRefiner); ok {
		res, err = cr.RefineContext(r.Context(), req.Field, req.Text, req.Mode, ac)
	} else {
		res, err = u.deps.Assist.Refine(r.Context(), req.Field, req.Text, req.Mode)
	}
	if err != nil {
		writeAssistError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(res)
}

func writeAssistError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
