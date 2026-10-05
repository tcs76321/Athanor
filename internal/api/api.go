// Package api exposes the M1 HTTP surface (ROADMAP M1-T7): project
// creation, goal submission (which starts a job), job progress, artifact
// listing. All routes mount on the daemon's loopback-only server
// (§21.8); there is deliberately no tool-execution route (Gate G1).
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/control"
	"github.com/tcs76321/athanor/internal/decompose"
	"github.com/tcs76321/athanor/internal/hitl"
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

// IndexRunner is the surface the M5-T8 `POST /projects/{id}/index` route
// uses to index a repository (ADR-0028 §6). The MCE adapter lives in cmd/,
// so the API does not import internal/mce.
type IndexRunner interface {
	IndexProject(ctx context.Context, projectID, path string) (IndexSummary, error)
}

// Decomposer is the surface the M6-T1 `POST /projects/{id}/decompose`
// route uses (ADR-0032). The concrete orchestrator is
// *decompose.Decomposer; the pure graph model and validator live in
// internal/dag.
type Decomposer interface {
	Decompose(ctx context.Context, projectID, goal string, criteria []string) (decompose.Result, error)
}

// Scheduler is the surface the gated `POST /projects/{id}/goals` path uses
// when dag_decomposition is enabled: it starts a decomposed goal's ready
// tasks (M6-T2, ADR-0033). The concrete implementation is
// *scheduler.Scheduler.
type Scheduler interface {
	Start(ctx context.Context, goalID string) ([]string, error)
}

// HITL is the surface the M6-T4 queue routes use (ADR-0035). The concrete
// implementation is *hitl.Service.
type HITL interface {
	Pending(ctx context.Context) ([]hitl.Request, error)
	Decide(ctx context.Context, id, action, note string, deferFor time.Duration) (hitl.Request, error)
}

// IndexSummary is an indexing pass's counters, also the route's JSON body.
type IndexSummary struct {
	Discovered int `json:"discovered"`
	Indexed    int `json:"indexed"`
	Skipped    int `json:"skipped"`
	Pruned     int `json:"pruned"`
	Chunks     int `json:"chunks"`
	Summarized int `json:"summarized"`
	Embedded   int `json:"embedded"`
	Failed     int `json:"failed"`
}

// API wires the HTTP handlers to the persistence and engine layers.
type API struct {
	projects   *project.Repo
	jobs       *job.Repository
	artifacts  *artifact.Store
	engine     Engine
	freezer    *control.KillSwitch
	db         *store.Store
	exporter   ManualExporter
	indexer    IndexRunner
	decomposer Decomposer
	scheduler  Scheduler
	hitl       HITL
	// dagScheduling mirrors execution.dag_decomposition: when true, goal
	// submission decomposes and schedules instead of creating one task.
	dagScheduling bool
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

// SetIndexRunner wires the M5-T8 repository indexer for `POST
// /projects/{id}/index`. A daemon that does not wire one answers 503.
func (a *API) SetIndexRunner(r IndexRunner) {
	a.indexer = r
}

// SetDecomposer wires the M6-T1 DAG decomposer for `POST
// /projects/{id}/decompose` (ADR-0032). A daemon that does not wire one
// answers 503.
func (a *API) SetDecomposer(d Decomposer) {
	a.decomposer = d
}

// SetScheduler wires the M6-T2 dependency scheduler used by the gated
// decompose-then-schedule path (ADR-0033). A daemon that does not wire one
// keeps the single-task submit path even when dag_decomposition is set.
func (a *API) SetScheduler(s Scheduler) {
	a.scheduler = s
}

// SetDAGScheduling enables decompose-then-schedule on goal submission. It
// mirrors execution.dag_decomposition; the default false preserves M1.
func (a *API) SetDAGScheduling(enabled bool) {
	a.dagScheduling = enabled
}

// SetHITL wires the M6-T4 HITL queue (ADR-0035). A daemon that does not
// wire one answers 503 on the queue routes.
func (a *API) SetHITL(h HITL) { a.hitl = h }

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
	// M5-T8: synchronous repository indexing on operator request.
	mux.HandleFunc("POST /projects/{id}/index", a.handleProjectIndex)
	// M6-T1: explicit DAG decomposition (ADR-0032).
	mux.HandleFunc("POST /projects/{id}/decompose", a.handleDecompose)
	mux.HandleFunc("GET /projects/{id}/tasks", a.handleProjectTasks)
	// M6-T4: the §20 HITL queue (ADR-0035).
	mux.HandleFunc("GET /hitl", a.handleHITLList)
	mux.HandleFunc("POST /hitl/{id}/decision", a.handleHITLDecision)
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
	// Execution is the optional §6.2 execution override (F3-T4,
	// ADR-0031). Absent means "use the built-in archetype defaults".
	Execution *projectExecution `json:"execution"`
}

// projectExecution is the wire shape for the per-project execution override.
type projectExecution struct {
	TestCommand  string   `json:"test_command"`
	BuildCommand string   `json:"build_command"`
	Linters      []string `json:"linters"`
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
	// F3-T4: apply the optional §6.2 execution override after the create
	// transaction. Setting it is a separate write so the common path
	// (no override) stays one transaction.
	if req.Execution != nil {
		ex := project.Execution{
			TestCommand:  req.Execution.TestCommand,
			BuildCommand: req.Execution.BuildCommand,
			Linters:      req.Execution.Linters,
		}
		if err := a.projects.SetExecution(r.Context(), p.ID, ex); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		p.Execution = ex
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

type indexRequest struct {
	Path string `json:"path"`
}

// handleProjectIndex runs the M5-T8 repository indexer to completion for a
// project (ADR-0028 §6). The repository root is the request's path, falling
// back to the project's repository_path; neither present is a 409.
func (a *API) handleProjectIndex(w http.ResponseWriter, r *http.Request) {
	if a.indexer == nil {
		writeError(w, http.StatusServiceUnavailable, "repository indexing is not configured")
		return
	}
	id := r.PathValue("id")
	p, err := a.projects.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var req indexRequest
	if r.ContentLength > 0 {
		if !decodeBody(w, r, &req) {
			return
		}
	}
	path := req.Path
	if path == "" {
		path = p.RepositoryPath
	}
	if path == "" {
		writeError(w, http.StatusConflict, "project has no repository_path; set one or pass a path")
		return
	}
	sum, err := a.indexer.IndexProject(r.Context(), id, path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sum)
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
