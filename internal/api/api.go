// Package api exposes the M1 HTTP surface (ROADMAP M1-T7): project
// creation, goal submission (which starts a job), job progress, artifact
// listing. All routes mount on the daemon's loopback-only server
// (§21.8); there is deliberately no tool-execution route (Gate G1).
package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/control"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
)

// Engine is the execution surface the API drives.
type Engine interface {
	Enqueue(jobID string)
}

// ManualExporter is the surface the M4-T4 `POST /exports/{id}`
// route uses to run a synchronous export. The egress
// package's Exporter satisfies it; the API does not
// import the egress package directly (Gate G1 keeps the
// dependency graph narrow).
type ManualExporter interface {
	// ExportOne runs the egress pipeline on one
	// artifact. Returns the absolute on-disk path
	// on success (clean or no-op) and an error on
	// a hard failure (artifact not found, scanner
	// subprocess hung, etc.).
	ExportOne(ctx context.Context, artifactID string) (path string, exported bool, err error)
}

// API wires the HTTP handlers to the persistence and engine layers.
type API struct {
	projects  *project.Repo
	jobs      *job.Repository
	artifacts *artifact.Store
	engine    Engine
	freezer   *control.KillSwitch
	db        *store.Store
	exporter  ManualExporter
}

// New builds the API.
func New(projects *project.Repo, jobs *job.Repository, artifacts *artifact.Store,
	engine Engine, freezer *control.KillSwitch, db *store.Store) *API {
	return &API{projects: projects, jobs: jobs, artifacts: artifacts, engine: engine, freezer: freezer, db: db}
}

// SetManualExporter wires the egress exporter for the
// `POST /exports/{id}` route. The setter is a separate
// step (rather than a New() argument) so the egress
// package — which depends on artifact + project + store —
// can be initialized after the API without an import
// cycle. Callers that don't wire an exporter get a 503
// on the manual-export route.
func (a *API) SetManualExporter(e ManualExporter) {
	a.exporter = e
}

// Register attaches all routes to mux.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /projects", a.handleProjectCreate)
	mux.HandleFunc("GET /projects/{id}", a.handleProjectGet)
	mux.HandleFunc("POST /projects/{id}/goals", a.handleGoalSubmit)
	mux.HandleFunc("GET /projects/{id}/artifacts", a.handleArtifacts)
	mux.HandleFunc("GET /jobs/{id}", a.handleJobGet)
	mux.HandleFunc("GET /jobs/{id}/events", a.handleJobEvents)
	// M4-T4: synchronous export on operator request.
	mux.HandleFunc("POST /exports/{id}", a.handleExport)
}

// writeJSON is the single response writer: always JSON, always UTF-8.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError reports an error as JSON.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeBody decodes a JSON request body into dst with a size guard.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

type projectRequest struct {
	Name      string   `json:"name"`
	Archetype string   `json:"archetype"`
	Goal      string   `json:"goal"`
	Criteria  []string `json:"acceptance_criteria"`
	// RepositoryPath is the §6.1 repository root the M5-T8 indexer walks
	// (ADR-0028). Optional; empty means no repository is configured.
	RepositoryPath string `json:"repository_path"`
}

type projectResponse struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Archetype      string `json:"archetype"`
	Goal           string `json:"goal"`
	RepositoryPath string `json:"repository_path,omitempty"`
	TaskID         string `json:"task_id,omitempty"`
}

func (a *API) handleProjectCreate(w http.ResponseWriter, r *http.Request) {
	var req projectRequest
	if !decodeBody(w, r, &req) {
		return
	}
	p, task, err := a.projects.Create(r.Context(), req.Name, req.Archetype, req.Goal, req.RepositoryPath, req.Criteria)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, projectResponse{
		ID: p.ID, Name: p.Name, Archetype: p.Archetype, Goal: p.Goal,
		RepositoryPath: p.RepositoryPath, TaskID: task.ID,
	})
}

func (a *API) handleProjectGet(w http.ResponseWriter, r *http.Request) {
	p, err := a.projects.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, projectResponse{
		ID: p.ID, Name: p.Name, Archetype: p.Archetype, Goal: p.Goal,
		RepositoryPath: p.RepositoryPath,
	})
}

// finalKind maps archetype → §9.1 kind, mirroring engine.finalKindFor.
func finalKind(archetype string) artifact.Kind {
	switch archetype {
	case project.ArchetypeCode:
		return artifact.KindCode
	case project.ArchetypeData:
		return artifact.KindDataset
	case project.ArchetypeMedia:
		return artifact.KindMedia
	default:
		return artifact.KindDocument
	}
}
