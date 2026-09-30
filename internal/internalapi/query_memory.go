package internalapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// MemoryQuerier is the M5-T7 dispatch surface for the query_memory tool
// (ADR-0026 §6). The Core performs the search against the MCE's memory
// (compacted memos + dormant chunks), which lives in the Core's SQLite
// database and is invisible to Job Pods — so, like ContextSwapper, the
// adapter lives in cmd/athanor and internalapi does not import internal/mce.
// A nil MemoryQuerier is valid: the route is registered and responds 503
// until the daemon wires an adapter.
type MemoryQuerier interface {
	QueryMemory(ctx context.Context, req toolenvelope.QueryMemoryRequest) (*toolenvelope.QueryMemoryResponse, error)
}

// Typed errors the query_memory adapter surfaces. The handler maps them to
// statuses; the runner maps the statuses back to errors callers can branch on.
var (
	// ErrMemoryDisabled: memory retrieval is unavailable on this daemon
	// (no MCE). → 501.
	ErrMemoryDisabled = errors.New("internalapi: memory retrieval is not configured")
	// ErrMemoryScopeRequired: the request carried no project or job scope,
	// so the search would span every project (ADR-0026 §5). → 400.
	ErrMemoryScopeRequired = errors.New("internalapi: query_memory requires a project or job scope")
)

// handleQueryMemory is the M5-T7 /query_memory route. Flow mirrors
// handleContextSwap: auth (middleware) → parse body → envelope check via
// a.tools.EnvelopeFor (403 + tool_disallowed audit when query_memory is not
// in the per-job envelope) → auditAllow → dispatch to the MemoryQuerier →
// typed error mapping. An empty Scope defaults to the authenticated job ID.
func (a *API) handleQueryMemory(w http.ResponseWriter, r *http.Request) {
	jobID := jobIDFromContext(r.Context())
	if jobID == "" {
		writeError(w, http.StatusUnauthorized, "missing authenticated job id")
		return
	}
	var req toolenvelope.QueryMemoryRequest
	if !decodeBody(w, r, &req) {
		return
	}
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		writeError(w, http.StatusBadRequest, "query_memory: query is required")
		return
	}
	if req.Scope == "" {
		req.Scope = jobID
	}

	// The per-job envelope gate. Gate G2's
	// TestGateG2ToolEnvelopeBypassImpossible requires this handler file to
	// reference a.tools.EnvelopeFor; the check is inlined here (rather than
	// delegating to checkEnvelope in gateway_tools.go) for exactly that
	// reason — the gate is a structural text search over this file, so a
	// comment or an indirection would defeat it.
	if a.tools == nil {
		a.auditReject(r.Context(), jobID, toolenvelope.ToolQueryMemory, "no envelope lookup configured")
		writeError(w, http.StatusServiceUnavailable, "tool envelope lookup not configured")
		return
	}
	env, err := a.tools.EnvelopeFor(r.Context(), jobID, a.defaultEnvelope)
	if err != nil {
		if errors.Is(err, toolenvelope.ErrUnknownTool) {
			a.auditReject(r.Context(), jobID, toolenvelope.ToolQueryMemory, err.Error())
			writeError(w, http.StatusInternalServerError, "task has invalid tool allowlist: "+err.Error())
			return
		}
		a.auditReject(r.Context(), jobID, toolenvelope.ToolQueryMemory, err.Error())
		writeError(w, http.StatusInternalServerError, "envelope lookup failed: "+err.Error())
		return
	}
	if !env.Allows(toolenvelope.ToolQueryMemory) {
		a.auditReject(r.Context(), jobID, toolenvelope.ToolQueryMemory, "tool not in job envelope")
		writeError(w, http.StatusForbidden, ErrToolDisallowed.Error())
		return
	}
	a.auditAllow(r.Context(), jobID, toolenvelope.ToolQueryMemory, "query_len="+itoaLen(req.Query))

	if a.querier == nil {
		writeError(w, http.StatusServiceUnavailable, ErrMemoryDisabled.Error())
		return
	}
	req.Tool = toolenvelope.ToolQueryMemory
	res, err := a.querier.QueryMemory(r.Context(), req)
	if err != nil {
		a.writeMemoryError(w, r, jobID, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// writeMemoryError maps a MemoryQuerier error to the M5-T7 status contract
// and records the outcome in the jobs audit log.
func (a *API) writeMemoryError(w http.ResponseWriter, r *http.Request, jobID string, err error) {
	switch {
	case errors.Is(err, ErrMemoryScopeRequired):
		a.auditReject(r.Context(), jobID, toolenvelope.ToolQueryMemory, "no scope")
		writeError(w, http.StatusBadRequest, ErrMemoryScopeRequired.Error())
	case errors.Is(err, ErrMemoryDisabled):
		a.auditReject(r.Context(), jobID, toolenvelope.ToolQueryMemory, "memory retrieval disabled")
		writeError(w, http.StatusNotImplemented, ErrMemoryDisabled.Error())
	default:
		a.auditReject(r.Context(), jobID, toolenvelope.ToolQueryMemory, "query failed: "+err.Error())
		writeError(w, http.StatusInternalServerError, "memory query failed: "+err.Error())
	}
}
