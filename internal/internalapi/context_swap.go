package internalapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// ContextSwapper is the M5-T3 dispatch surface for the context_swap tool
// (ADR-0021 §10). The Core performs the swap against the MCE's active/dormant
// working set, which no Job Pod can see. internalapi does not import
// internal/mce: the adapter lives in cmd/athanor, the same inversion pattern
// ToolGateway (ADR-0019 §2), TokenStore (ADR-0008) and ToolEnvLookup (M2-T4)
// use. A nil ContextSwapper is valid — the route is registered and responds
// 503 until the daemon wires an adapter.
type ContextSwapper interface {
	SwapContext(ctx context.Context, req toolenvelope.ContextSwapRequest) (*toolenvelope.ContextSwapResponse, error)
}

// Typed errors the context_swap adapter surfaces. The handler maps them to
// statuses; the runner maps the statuses back to errors callers can branch on.
var (
	// ErrChunkNotFound: the requested chunk handle is unknown. → 404.
	ErrChunkNotFound = errors.New("internalapi: context_swap target chunk not found")
	// ErrSwapDisabled: context_engine.enable_lossless_swapping is false, so
	// the MCE refuses to rotate a working set (ADR-0021 §9). → 501.
	ErrSwapDisabled = errors.New("internalapi: lossless swapping is disabled")
)

// handleContextSwap is the M5-T3 /context_swap route. Flow mirrors
// handleExecuteCode: auth (middleware) → parse body → envelope check via
// a.tools.EnvelopeFor (403 + tool_disallowed audit when context_swap is not in
// the per-job envelope) → auditAllow → dispatch to the ContextSwapper → typed
// error mapping. An empty Scope defaults to the authenticated job ID.
func (a *API) handleContextSwap(w http.ResponseWriter, r *http.Request) {
	jobID := jobIDFromContext(r.Context())
	if jobID == "" {
		writeError(w, http.StatusUnauthorized, "missing authenticated job id")
		return
	}
	var req toolenvelope.ContextSwapRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.TargetChunkID == "" {
		writeError(w, http.StatusBadRequest, "context_swap: target_chunk_id is required")
		return
	}
	// Isolation (§3.1): the pod never chooses which working set it rotates.
	// Force the scope to the authenticated job — a client-supplied scope is
	// ignored, so a pod cannot overwrite another job's active pointer.
	req.Scope = jobID
	// Attach the caller's project (server-side) so the store can enforce
	// chunk ownership: a chunk is reachable only if it belongs to this job
	// or to its project.
	if a.projects != nil {
		if pid, err := a.projects.JobProject(r.Context(), jobID); err == nil {
			req.ProjectID = pid
		}
	}

	// The per-job envelope gate. Gate G2's
	// TestGateG2ToolEnvelopeBypassImpossible requires this handler file to
	// reference a.tools.EnvelopeFor; the check is inlined here (rather than
	// delegating to checkEnvelope in gateway_tools.go) for exactly that
	// reason — the gate is a structural text search over this file, so a
	// comment or an indirection would defeat it.
	if a.tools == nil {
		a.auditReject(r.Context(), jobID, toolenvelope.ToolContextSwap, "no envelope lookup configured")
		writeError(w, http.StatusServiceUnavailable, "tool envelope lookup not configured")
		return
	}
	env, err := a.tools.EnvelopeFor(r.Context(), jobID, a.defaultEnvelope)
	if err != nil {
		if errors.Is(err, toolenvelope.ErrUnknownTool) {
			a.auditReject(r.Context(), jobID, toolenvelope.ToolContextSwap, err.Error())
			writeError(w, http.StatusInternalServerError, "task has invalid tool allowlist: "+err.Error())
			return
		}
		a.auditReject(r.Context(), jobID, toolenvelope.ToolContextSwap, err.Error())
		writeError(w, http.StatusInternalServerError, "envelope lookup failed: "+err.Error())
		return
	}
	if !env.Allows(toolenvelope.ToolContextSwap) {
		a.auditReject(r.Context(), jobID, toolenvelope.ToolContextSwap, "tool not in job envelope")
		writeError(w, http.StatusForbidden, ErrToolDisallowed.Error())
		return
	}
	a.auditAllow(r.Context(), jobID, toolenvelope.ToolContextSwap, "chunk="+req.TargetChunkID)

	if a.swapper == nil {
		writeError(w, http.StatusServiceUnavailable, "context swap not configured")
		return
	}
	req.Tool = toolenvelope.ToolContextSwap
	res, err := a.swapper.SwapContext(r.Context(), req)
	if err != nil {
		a.writeSwapError(w, r, jobID, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// writeSwapError maps a ContextSwapper error to the M5-T3 status contract and
// records the outcome in the jobs audit log.
func (a *API) writeSwapError(w http.ResponseWriter, r *http.Request, jobID string, err error) {
	switch {
	case errors.Is(err, ErrChunkNotFound):
		a.auditReject(r.Context(), jobID, toolenvelope.ToolContextSwap, "chunk not found")
		writeError(w, http.StatusNotFound, ErrChunkNotFound.Error())
	case errors.Is(err, ErrSwapDisabled):
		a.auditReject(r.Context(), jobID, toolenvelope.ToolContextSwap, "swapping disabled")
		writeError(w, http.StatusNotImplemented, ErrSwapDisabled.Error())
	default:
		a.auditReject(r.Context(), jobID, toolenvelope.ToolContextSwap, "swap failed: "+err.Error())
		writeError(w, http.StatusInternalServerError, "context swap failed: "+err.Error())
	}
}
