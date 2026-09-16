package internalapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// EventLogger is the surface the heartbeat and log handlers
// need. *store.Store satisfies it. Kept as an interface so the
// handlers can be tested with a fake.
type EventLogger interface {
	AppendEvent(ctx context.Context, e store.Event) (int64, error)
}

// ToolEnvLookup is the surface the new execute_code and run_tests
// handlers read to enforce the per-job allowlist (ARCHITECTURE
// §25, ROADMAP M2-T4). *project.Repo satisfies it via
// EnvelopeFor. The handler is the only place this check runs;
// Gate G2's TestGateG2ToolEnvelopeBypassImpossible structurally
// proves every execute_code/run_tests route references the
// envelope lookup.
type ToolEnvLookup interface {
	// EnvelopeFor returns the per-job tool envelope: a
	// non-empty task override (tasks.allowed_tools_json) or,
	// if the task has no override, the passed-in daemon
	// default (config.job_pod.default_tools). The default is
	// passed by the caller because internalapi intentionally
	// does not import internal/config (Gate G2 has no rule
	// against that today, but the seam keeps the package
	// dependency-free of the daemon's config schema).
	EnvelopeFor(ctx context.Context, jobID string, defaultEnv toolenvelope.Envelope) (toolenvelope.Envelope, error)
}

// API wires the internal API to its dependencies. All routes are
// mounted behind the auth middleware at registration time.
type API struct {
	tokens  TokenStore
	projects *project.Repo
	events  EventLogger
	tools   ToolEnvLookup
	// gateway is the M4-T7 ToolGateway dispatch surface for
	// fetch_url / search_web (ADR-0019 §2). Nil is valid: the routes
	// are registered but respond 503 "not configured" — the daemon
	// wires the cmd/athanor adapter over GatewayParts in M4-T7.4.
	gateway ToolGateway
	// defaultEnvelope is the daemon-wide tool envelope from
	// config.job_pod.default_tools. Stored on the API so the
	// envelope check has a baseline when a task has no
	// override. Computed once at construction; if the config
	// ever changes at runtime, the daemon must be restarted
	// (the same constraint that applies to every other
	// config-derived field).
	defaultEnvelope toolenvelope.Envelope
}

// New returns an API bound to the given TokenStore, project
// repository, event logger, tool envelope lookup, and gateway-tool
// dispatch surface. The constructor signature widened in M2-T4 commit
// 3 and again in M4-T7.3; call sites are updated in the same commit
// (cmd/athanor/serve.go) and in the test fixtures (handlers_test.go).
//
// defaultEnvelope is the daemon-wide fallback when a task
// declares no override. The API does not validate it here;
// the caller is expected to have already passed it through
// config.JobPodEnvelope so a closed-set violation would have
// failed at config-load time.
//
// gateway may be nil (the routes then respond 503 until an adapter
// is wired); passing a non-nil ToolGateway activates fetch_url and
// search_web for jobs whose envelope admits them.
func New(tokens TokenStore, projects *project.Repo, events EventLogger, tools ToolEnvLookup, defaultEnvelope toolenvelope.Envelope, gateway ToolGateway) *API {
	return &API{tokens: tokens, projects: projects, events: events, tools: tools, defaultEnvelope: defaultEnvelope, gateway: gateway}
}

// jobResponse is the body of GET /internal/v1/jobs/{id}. The pod
// uses this to discover what it is supposed to do.
type jobResponse struct {
	ID          string   `json:"id"`
	State       string   `json:"state"`
	ProjectID   string   `json:"project_id"`
	TaskTitle   string   `json:"task_title"`
	TaskDescription string `json:"task_description"`
	Criteria    []string `json:"acceptance_criteria"`
}

// handleJobGet returns the authenticated job's task context. The
// pod reads this once at startup to learn the goal, the acceptance
// criteria, and the description. M2-T4 will use this; M2-T3
// commits only prove the round-trip works.
func (a *API) handleJobGet(w http.ResponseWriter, r *http.Request) {
	jobID := jobIDFromContext(r.Context())
	if jobID == "" {
		// Unreachable: the middleware sets it. Defensive 401
		// in case a future refactor routes around auth.
		writeError(w, http.StatusUnauthorized, "missing authenticated job id")
		return
	}
	task, err := a.projects.Task(r.Context(), jobID)
	if err != nil {
		if errors.Is(err, project.ErrNotFound) {
			writeError(w, http.StatusNotFound, "task not found for job "+jobID)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, jobResponse{
		ID:              jobID,
		State:           "running", // M1: jobs at this point are running
		ProjectID:       task.ProjectID,
		TaskTitle:       task.Title,
		TaskDescription: task.Description,
		Criteria:        task.Criteria,
	})
}

// heartbeatRequest is the body of POST /internal/v1/jobs/{id}/heartbeat.
// The pod sends a periodic liveness signal; future M2-T5 will use
// the timestamp to detect stuck pods.
type heartbeatRequest struct {
	// Note: empty body is allowed. Fields are reserved for future use.
}

// handleHeartbeat records a heartbeat event in the EventLog. The
// body is currently ignored; future versions may carry a status
// or progress flag.
func (a *API) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	jobID := jobIDFromContext(r.Context())
	if jobID == "" {
		writeError(w, http.StatusUnauthorized, "missing authenticated job id")
		return
	}
	var req heartbeatRequest
	// Body is optional: ignore decode errors and treat as empty.
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req)
	if _, err := a.events.AppendEvent(r.Context(), store.Event{
		Category: "jobs",
		JobID:    jobID,
		Data:     map[string]any{"event": "heartbeat"},
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// logRequest is the body of POST /internal/v1/jobs/{id}/log. The
// pod streams a single log line per request; bulk streaming
// arrives with a future task.
type logRequest struct {
	Stream string `json:"stream"` // "stdout" | "stderr" — informational only
	Line   string `json:"line"`
}

// handleLog records a single log line from the pod. The stream
// field is preserved in the event for human readers; the line
// is the full text of one log output line.
func (a *API) handleLog(w http.ResponseWriter, r *http.Request) {
	jobID := jobIDFromContext(r.Context())
	if jobID == "" {
		writeError(w, http.StatusUnauthorized, "missing authenticated job id")
		return
	}
	var req logRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Stream == "" {
		req.Stream = "stdout"
	}
	if _, err := a.events.AppendEvent(r.Context(), store.Event{
		Category: "jobs",
		JobID:    jobID,
		Data: map[string]any{
			"event":  "pod_log",
			"stream": req.Stream,
			"line":   req.Line,
		},
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// decodeBody decodes a JSON request body into dst with a 1 MiB
// size guard. The handler-specific size limit is encoded in the
// http.MaxBytesReader call inside each handler; this is the
// shared decoder.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// Register attaches every internal route to mux. Every route is
// wrapped in authMiddleware; the structural proof of "every route
// is wrapped" lives in Gate G2 (internal/gate/gate_test.go) and
// runs in make test-race.
//
// M2-T4 adds two tool routes — execute_code and run_tests — that
// share the auth middleware. Their envelope enforcement is a
// separate structural check (TestGateG2ToolEnvelopeBypassImpossible).
func (a *API) Register(mux *http.ServeMux) {
	mux.Handle("GET /internal/v1/jobs/{id}",
		authMiddleware(a.tokens, http.HandlerFunc(a.handleJobGet)))
	mux.Handle("POST /internal/v1/jobs/{id}/heartbeat",
		authMiddleware(a.tokens, http.HandlerFunc(a.handleHeartbeat)))
	mux.Handle("POST /internal/v1/jobs/{id}/log",
		authMiddleware(a.tokens, http.HandlerFunc(a.handleLog)))
	mux.Handle("POST /internal/v1/jobs/{id}/execute_code",
		authMiddleware(a.tokens, http.HandlerFunc(a.handleExecuteCode)))
	mux.Handle("POST /internal/v1/jobs/{id}/run_tests",
		authMiddleware(a.tokens, http.HandlerFunc(a.handleRunTests)))
	mux.Handle("POST /internal/v1/jobs/{id}/lint",
		authMiddleware(a.tokens, http.HandlerFunc(a.handleLint)))
	mux.Handle("POST /internal/v1/jobs/{id}/fetch_url",
		authMiddleware(a.tokens, http.HandlerFunc(a.handleFetchURL)))
	mux.Handle("POST /internal/v1/jobs/{id}/search_web",
		authMiddleware(a.tokens, http.HandlerFunc(a.handleSearchWeb)))
}
