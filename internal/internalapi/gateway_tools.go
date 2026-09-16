package internalapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// ToolGateway is the M4-T7 dispatch surface for the gateway-backed
// tools fetch_url and search_web (ADR-0019 §2). The Core executes the
// fetch — Job Pods run with --network=none, so the §21.5 gateway lives
// server-side and the routes are envelope-gated. The production
// implementation is the cmd/athanor adapter over
// GatewayParts{Client, Reader}; tests inject a fake. internalapi does
// not import internal/gateway: the adapter holds both types, exactly
// the inversion pattern TokenStore (ADR-0008) and ToolEnvLookup
// (M2-T4) use.
type ToolGateway interface {
	FetchURL(ctx context.Context, req toolenvelope.FetchURLRequest) (*toolenvelope.FetchURLResponse, error)
	SearchWeb(ctx context.Context, req toolenvelope.SearchWebRequest) (*toolenvelope.SearchWebResponse, error)
}

// Typed errors the gateway-tool adapter (and the handlers) surface.
// The handlers map them to HTTP statuses; the runner maps the
// statuses back to errors the engine can distinguish.
var (
	// ErrSearchNotConfigured: the operator has not set
	// network.search_engine_url_template. search_web is registered in
	// the closed set but inert until configured (ADR-0019 §4). → 501.
	ErrSearchNotConfigured = errors.New("internalapi: search_web is not configured (network.search_engine_url_template is empty)")
	// ErrFetchDenied: the gateway Policy refused the URL (off-list,
	// deny-list, IDN, private IP, redirect escape). A containment
	// refusal, not a malfunction. → 403.
	ErrFetchDenied = errors.New("internalapi: fetch denied by gateway policy")
	// ErrContentRejected: the fetched content was refused by the
	// Reader pipeline (not extractable, or it tripped the
	// prompt-injection scan — fail-closed, ADR-0018 §3). → 422.
	ErrContentRejected = errors.New("internalapi: fetched content rejected")
	// ErrFetchFailed: transport-level failure (DNS, connect, timeout,
	// upstream read error). → 502.
	ErrFetchFailed = errors.New("internalapi: gateway fetch failed")
)

// fetchURLRequest is the body of
// POST /internal/v1/jobs/{id}/fetch_url. The wire shape lives in
// toolenvelope (shared with the runner, ADR-0009 pattern); this alias
// keeps the handler signatures readable.
type fetchURLRequest = toolenvelope.FetchURLRequest

// searchWebRequest is the body of
// POST /internal/v1/jobs/{id}/search_web.
type searchWebRequest = toolenvelope.SearchWebRequest

// maxQueryBytes caps the search query length server-side. A query is
// prompt material, not a file upload; anything longer than this is a
// caller bug, and the engine template would render it into a URL no
// search engine accepts anyway.
const maxQueryBytes = 2048

// handleFetchURL is the M4-T7 /fetch_url route (ADR-0019 §1). Flow
// mirrors handleLint: auth (middleware) → parse body → envelope check
// via a.tools.EnvelopeFor (403 + tool_disallowed audit when absent) →
// auditAllow → dispatch to the ToolGateway → typed error mapping.
func (a *API) handleFetchURL(w http.ResponseWriter, r *http.Request) {
	jobID := jobIDFromContext(r.Context())
	if jobID == "" {
		writeError(w, http.StatusUnauthorized, "missing authenticated job id")
		return
	}
	var req fetchURLRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.URL == "" {
		writeError(w, http.StatusBadRequest, "fetch_url: url is required")
		return
	}
	// Defense-in-depth sanity check. The gateway Policy re-validates
	// everything (allowlist, deny-list, rebinding guard); this only
	// rejects obviously malformed input with a 400 instead of a 403
	// so the caller can distinguish "typo" from "denied".
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		writeError(w, http.StatusBadRequest, "fetch_url: url must be an absolute http(s) URL")
		return
	}

	if _, _, _, ok := a.checkEnvelope(w, r, jobID, toolenvelope.ToolFetchURL, "url_len="+itoaLen(req.URL)); !ok {
		return
	}

	if a.gateway == nil {
		a.auditReject(r.Context(), jobID, toolenvelope.ToolFetchURL, "no gateway configured")
		writeError(w, http.StatusServiceUnavailable, "gateway tools not configured on this daemon")
		return
	}

	// Defense-in-depth: stamp the tool identity before dispatch, the
	// same contract ExecuteRequest.Tool documents for the pod tools.
	req.Tool = toolenvelope.ToolFetchURL
	ctx, cancel := toolTimeout(r.Context(), req.TimeoutSeconds)
	defer cancel()
	res, err := a.gateway.FetchURL(ctx, req)
	if err != nil {
		a.writeGatewayError(w, r, jobID, toolenvelope.ToolFetchURL, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleSearchWeb is the M4-T7 /search_web route (ADR-0019 §4). Same
// shape as handleFetchURL with the query body and the
// ErrSearchNotConfigured → 501 mapping.
func (a *API) handleSearchWeb(w http.ResponseWriter, r *http.Request) {
	jobID := jobIDFromContext(r.Context())
	if jobID == "" {
		writeError(w, http.StatusUnauthorized, "missing authenticated job id")
		return
	}
	var req searchWebRequest
	if !decodeBody(w, r, &req) {
		return
	}
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		writeError(w, http.StatusBadRequest, "search_web: query is required")
		return
	}
	if len(req.Query) > maxQueryBytes {
		writeError(w, http.StatusBadRequest, "search_web: query exceeds the maximum length")
		return
	}

	if _, _, _, ok := a.checkEnvelope(w, r, jobID, toolenvelope.ToolSearchWeb, "query_len="+itoaLen(req.Query)); !ok {
		return
	}

	if a.gateway == nil {
		a.auditReject(r.Context(), jobID, toolenvelope.ToolSearchWeb, "no gateway configured")
		writeError(w, http.StatusServiceUnavailable, "gateway tools not configured on this daemon")
		return
	}

	// Defense-in-depth: stamp the tool identity before dispatch, the
	// same contract ExecuteRequest.Tool documents for the pod tools.
	req.Tool = toolenvelope.ToolSearchWeb
	ctx, cancel := toolTimeout(r.Context(), req.TimeoutSeconds)
	defer cancel()
	res, err := a.gateway.SearchWeb(ctx, req)
	if err != nil {
		a.writeGatewayError(w, r, jobID, toolenvelope.ToolSearchWeb, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// checkEnvelope is the shared envelope gate for the gateway-tool
// handlers. It mirrors the handleExecuteCode/handleRunTests/handleLint
// flow exactly: nil-lookup 503, ErrUnknownTool 500, lookup failure
// 500, disallowed 403 — each with a tool_disallowed audit row — and
// auditAllow on the way through. The bool is "continue to dispatch";
// the envelope and placeholder returns exist so the signature can grow
// later without a call-site churn.
func (a *API) checkEnvelope(w http.ResponseWriter, r *http.Request, jobID string, tool toolenvelope.Tool, detail string) (toolenvelope.Envelope, int, string, bool) {
	if a.tools == nil {
		a.auditReject(r.Context(), jobID, tool, "no envelope lookup configured")
		writeError(w, http.StatusServiceUnavailable, "tool envelope lookup not configured")
		return toolenvelope.Envelope{}, 0, "", false
	}
	env, err := a.tools.EnvelopeFor(r.Context(), jobID, a.defaultEnvelope)
	if err != nil {
		if errors.Is(err, toolenvelope.ErrUnknownTool) {
			a.auditReject(r.Context(), jobID, tool, err.Error())
			writeError(w, http.StatusInternalServerError, "task has invalid tool allowlist: "+err.Error())
			return toolenvelope.Envelope{}, 0, "", false
		}
		a.auditReject(r.Context(), jobID, tool, err.Error())
		writeError(w, http.StatusInternalServerError, "envelope lookup failed: "+err.Error())
		return toolenvelope.Envelope{}, 0, "", false
	}
	if !env.Allows(tool) {
		a.auditReject(r.Context(), jobID, tool, "tool not in job envelope")
		writeError(w, http.StatusForbidden, ErrToolDisallowed.Error())
		return toolenvelope.Envelope{}, 0, "", false
	}
	a.auditAllow(r.Context(), jobID, tool, detail)
	return env, 0, "", true
}

// writeGatewayError maps a ToolGateway error to the ADR-0019 §1
// status contract and records the outcome in the jobs audit log (the
// gateway itself logs the network-side detail; this row is the
// job-side trace).
func (a *API) writeGatewayError(w http.ResponseWriter, r *http.Request, jobID string, tool toolenvelope.Tool, err error) {
	switch {
	case errors.Is(err, ErrSearchNotConfigured):
		a.auditReject(r.Context(), jobID, tool, "search engine not configured")
		writeError(w, http.StatusNotImplemented, ErrSearchNotConfigured.Error())
	case errors.Is(err, ErrFetchDenied):
		a.auditReject(r.Context(), jobID, tool, "denied by gateway policy")
		writeError(w, http.StatusForbidden, ErrFetchDenied.Error())
	case errors.Is(err, ErrContentRejected):
		a.auditReject(r.Context(), jobID, tool, "content rejected by reader pipeline")
		writeError(w, http.StatusUnprocessableEntity, ErrContentRejected.Error())
	default:
		a.auditReject(r.Context(), jobID, tool, "fetch failed: "+err.Error())
		writeError(w, http.StatusBadGateway, ErrFetchFailed.Error()+": "+err.Error())
	}
}

// toolTimeout applies the request's timeout_seconds to the dispatch
// context. Zero means "no additional cap" — the caller's context (and
// the gateway's own per-request timeout) govern.
func toolTimeout(parent context.Context, seconds int) (context.Context, context.CancelFunc) {
	if seconds <= 0 {
		return parent, func() {}
	}
	return context.WithTimeout(parent, time.Duration(seconds)*time.Second)
}