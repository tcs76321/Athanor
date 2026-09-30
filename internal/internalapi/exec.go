package internalapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// allowedLanguage is the closed set of languages the Job Pod's
// execute_code route accepts (M2-T4 §25). The set is intentionally
// narrow — one entry — and grows when the M3 Skills runtime lands.
const allowedLanguage = "python"

// executeCodeRequest is the body of
// POST /internal/v1/jobs/{id}/execute_code.
type executeCodeRequest struct {
	Language string `json:"language"`
	Code     string `json:"code"`
	Timeout  int    `json:"timeout_seconds"`
}

// runTestsRequest is the body of POST /internal/v1/jobs/{id}/run_tests.
type runTestsRequest struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout_seconds"`
}

// lintRequest is the body of POST /internal/v1/jobs/{id}/lint.
// M3-T2 commit 2.3: the per-task linter. Default command is
// `ruff check .` for Python projects; a future per-task
// override arrives with the Skills runtime (post-M7).
type lintRequest struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout_seconds"`
}

// allowedLintCommand is the closed-set default for the lint
// route when the request body omits a `command`. Future
// overrides (per-task linter config) will be merged in the
// same way the per-task `allowed_tools_json` override works
// for the envelope.
const allowedLintCommand = "ruff check ."

// handleExecuteCode is the M2-T4 /execute_code route. Steps:
//  1. Parse body (400 on bad JSON or missing Code).
//  2. Validate Language against the closed set (400).
//  3. Look up the per-job envelope via a.tools.EnvelopeFor; 403
//     if execute_code is not in the envelope. The structural
//     proof that this check cannot be bypassed lives in Gate G2
//     (TestGateG2ToolEnvelopeBypassImpossible).
//  4. Append an EventLog entry recording the call.
//
// In commit 4 the runner dispatch lands. The handler returns 501
// in this commit because the runner package does not yet exist;
// the body validation, the envelope check, and the audit log are
// real and tested.
func (a *API) handleExecuteCode(w http.ResponseWriter, r *http.Request) {
	jobID := jobIDFromContext(r.Context())
	if jobID == "" {
		writeError(w, http.StatusUnauthorized, "missing authenticated job id")
		return
	}
	var req executeCodeRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Code == "" {
		writeError(w, http.StatusBadRequest, "execute_code: code is required")
		return
	}
	if req.Language == "" {
		req.Language = allowedLanguage
	}
	if req.Language != allowedLanguage {
		writeError(w, http.StatusBadRequest,
			"execute_code: language must be \"python\" (M2-T4 closed set)")
		return
	}

	if a.tools == nil {
		a.auditReject(r.Context(), jobID, toolenvelope.ToolExecuteCode, "no envelope lookup configured")
		writeError(w, http.StatusServiceUnavailable, "tool envelope lookup not configured")
		return
	}
	env, err := a.tools.EnvelopeFor(r.Context(), jobID, a.defaultEnvelope)
	if err != nil {
		if errors.Is(err, toolenvelope.ErrUnknownTool) {
			a.auditReject(r.Context(), jobID, toolenvelope.ToolExecuteCode, err.Error())
			writeError(w, http.StatusInternalServerError, "task has invalid tool allowlist: "+err.Error())
			return
		}
		a.auditReject(r.Context(), jobID, toolenvelope.ToolExecuteCode, err.Error())
		writeError(w, http.StatusInternalServerError, "envelope lookup failed: "+err.Error())
		return
	}
	if !env.Allows(toolenvelope.ToolExecuteCode) {
		a.auditReject(r.Context(), jobID, toolenvelope.ToolExecuteCode, "tool not in job envelope")
		writeError(w, http.StatusForbidden, ErrToolDisallowed.Error())
		return
	}

	a.auditAllow(r.Context(), jobID, toolenvelope.ToolExecuteCode, "code_len="+itoaLen(req.Code))
	writeError(w, http.StatusNotImplemented, "execute_code dispatch lands in M2-T4 commit 4 (runner package)")
}

// handleRunTests is the M2-T4 /run_tests route. Mirrors
// handleExecuteCode with a different body shape and a different
// envelope tool. As with execute_code, dispatch lands in commit 4.
func (a *API) handleRunTests(w http.ResponseWriter, r *http.Request) {
	jobID := jobIDFromContext(r.Context())
	if jobID == "" {
		writeError(w, http.StatusUnauthorized, "missing authenticated job id")
		return
	}
	var req runTestsRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Command == "" {
		writeError(w, http.StatusBadRequest, "run_tests: command is required")
		return
	}

	if a.tools == nil {
		a.auditReject(r.Context(), jobID, toolenvelope.ToolRunTests, "no envelope lookup configured")
		writeError(w, http.StatusServiceUnavailable, "tool envelope lookup not configured")
		return
	}
	env, err := a.tools.EnvelopeFor(r.Context(), jobID, a.defaultEnvelope)
	if err != nil {
		if errors.Is(err, toolenvelope.ErrUnknownTool) {
			a.auditReject(r.Context(), jobID, toolenvelope.ToolRunTests, err.Error())
			writeError(w, http.StatusInternalServerError, "task has invalid tool allowlist: "+err.Error())
			return
		}
		a.auditReject(r.Context(), jobID, toolenvelope.ToolRunTests, err.Error())
		writeError(w, http.StatusInternalServerError, "envelope lookup failed: "+err.Error())
		return
	}
	if !env.Allows(toolenvelope.ToolRunTests) {
		a.auditReject(r.Context(), jobID, toolenvelope.ToolRunTests, "tool not in job envelope")
		writeError(w, http.StatusForbidden, ErrToolDisallowed.Error())
		return
	}

	a.auditAllow(r.Context(), jobID, toolenvelope.ToolRunTests, "command="+req.Command)
	writeError(w, http.StatusNotImplemented, "run_tests dispatch lands in M2-T4 commit 4 (runner package)")
}

// handleLint is the M3-T2 commit 2.3 /lint route. Mirrors
// handleExecuteCode and handleRunTests with the lint body
// shape (command + timeout_seconds). The envelope check
// uses the same `a.tools.EnvelopeFor` path; Gate G2's
// TestGateG2ToolEnvelopeBypassImpossible already iterates
// over the closed set and will pick up `lint` once it's
// added to the slice below.
//
// As with the M2-T4 handlers, the actual linter dispatch
// (running `ruff` in a Job Pod) lands in a follow-up
// commit; this commit ships the envelope + audit + 501.
func (a *API) handleLint(w http.ResponseWriter, r *http.Request) {
	jobID := jobIDFromContext(r.Context())
	if jobID == "" {
		writeError(w, http.StatusUnauthorized, "missing authenticated job id")
		return
	}
	var req lintRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Command == "" {
		req.Command = allowedLintCommand
	}

	if a.tools == nil {
		a.auditReject(r.Context(), jobID, toolenvelope.ToolLint, "no envelope lookup configured")
		writeError(w, http.StatusServiceUnavailable, "tool envelope lookup not configured")
		return
	}
	env, err := a.tools.EnvelopeFor(r.Context(), jobID, a.defaultEnvelope)
	if err != nil {
		if errors.Is(err, toolenvelope.ErrUnknownTool) {
			a.auditReject(r.Context(), jobID, toolenvelope.ToolLint, err.Error())
			writeError(w, http.StatusInternalServerError, "task has invalid tool allowlist: "+err.Error())
			return
		}
		a.auditReject(r.Context(), jobID, toolenvelope.ToolLint, err.Error())
		writeError(w, http.StatusInternalServerError, "envelope lookup failed: "+err.Error())
		return
	}
	if !env.Allows(toolenvelope.ToolLint) {
		a.auditReject(r.Context(), jobID, toolenvelope.ToolLint, "tool not in job envelope")
		writeError(w, http.StatusForbidden, ErrToolDisallowed.Error())
		return
	}

	a.auditAllow(r.Context(), jobID, toolenvelope.ToolLint, "command="+req.Command)
	writeError(w, http.StatusNotImplemented, "lint dispatch lands in a follow-up commit (M3-T2 commit 2.3 ships the envelope + audit)")
}

// auditReject records a rejected tool call in the EventLog. The
// log is the source of truth for "what was attempted and why it
// was denied"; the rejection HTTP response is the user-visible
// surface.
func (a *API) auditReject(ctx context.Context, jobID string, tool toolenvelope.Tool, reason string) {
	if a.events == nil {
		return
	}
	_, _ = a.events.AppendEvent(ctx, store.Event{
		Category: "jobs",
		JobID:    jobID,
		Data: map[string]any{
			"event":  "tool_disallowed",
			"tool":   string(tool),
			"reason": reason,
		},
	})
}

// auditAllow records a successful envelope check, just before the
// runner dispatch. M3 will use this to drive the EvaluationRecord.
func (a *API) auditAllow(ctx context.Context, jobID string, tool toolenvelope.Tool, detail string) {
	if a.events == nil {
		return
	}
	_, _ = a.events.AppendEvent(ctx, store.Event{
		Category: "jobs",
		JobID:    jobID,
		Data: map[string]any{
			"event":  "tool_call",
			"tool":   string(tool),
			"detail": detail,
			"at":     time.Now().UTC().Format(time.RFC3339Nano),
		},
	})
}

// itoaLen is a tiny non-allocating length-to-string for audit
// fields.
func itoaLen(s string) string {
	n := len(s)
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
