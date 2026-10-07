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
	// Files is a multi-file candidate tree (ADR-0065). Non-empty supersedes
	// Code: the pod stages the tree and the task's run_tests command exercises
	// it.
	Files   []toolenvelope.File `json:"files"`
	Timeout int                 `json:"timeout_seconds"`
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

// PodExecutor is the M2-T4b dispatch surface for the pod-executed tools
// (execute_code, run_tests, lint). The Core runs the command inside the
// job's Job Pod and returns its result (ADR-0024 §2/§4). The production
// implementation lives in cmd/athanor over jobpod.Manager — the same
// inversion ToolGateway (ADR-0019 §2), ContextSwapper (ADR-0021 §10),
// TokenStore (ADR-0008), and ToolEnvLookup (M2-T4) use, so internalapi
// imports neither internal/jobpod nor internal/llm. A nil PodExecutor is
// valid: the routes are registered and respond 503 "not configured"
// until the daemon wires an adapter.
type PodExecutor interface {
	RunCode(ctx context.Context, jobID string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error)
	RunTests(ctx context.Context, jobID string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error)
	Lint(ctx context.Context, jobID string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error)
}

// Typed errors the PodExecutor adapter surfaces. The handler maps them to
// statuses; the runner maps statuses back to errors callers can branch on
// (the ContextSwapper / ToolGateway pattern).
var (
	// ErrNoPod: the job has no live Job Pod to exec into (jobpod.ErrNotFound
	// upstream); the engine can start one and retry. → 404.
	ErrNoPod = errors.New("internalapi: no job pod for this job")
	// ErrPodNotRunning: the pod exists but cannot accept commands. → 409.
	ErrPodNotRunning = errors.New("internalapi: job pod is not running")
	// ErrExecNotConfigured: the daemon has no job_pod.image, so it cannot
	// start a Job Pod to exec into. A configuration gap, not a job
	// failure. → 503. The daemon logs a warning at boot rather than
	// refusing to start, because a daemon that runs only LLM phases is
	// still useful (M2-T4b.4).
	ErrExecNotConfigured = errors.New("internalapi: pod executor not configured (job_pod.image is empty)")
)

// handleExecuteCode is the M2-T4 /execute_code route. Steps:
//
//  1. Parse body (400 on bad JSON or missing Code).
//
//  2. Validate Language against the closed set (400).
//
//  3. Look up the per-job envelope via a.tools.EnvelopeFor; 403
//     if execute_code is not in the envelope. The structural
//     proof that this check cannot be bypassed lives in Gate G2
//     (TestGateG2ToolEnvelopeBypassImpossible).
//
//  4. Append an EventLog entry recording the call.
//
//  5. Dispatch to the PodExecutor (M2-T4b, ADR-0024). A nil executor
//     responds 503 "not configured"; a typed executor error maps via
//     writeExecError. The old 501 placeholder is gone.
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
	if req.Code == "" && len(req.Files) == 0 {
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
	if a.exec == nil {
		writeError(w, http.StatusServiceUnavailable, "pod executor not configured")
		return
	}
	res, err := a.exec.RunCode(r.Context(), jobID, toolenvelope.ExecuteRequest{
		Tool:           toolenvelope.ToolExecuteCode,
		Language:       req.Language,
		Code:           req.Code,
		Files:          req.Files,
		TimeoutSeconds: req.Timeout,
	})
	if err != nil {
		a.writeExecError(w, r, jobID, toolenvelope.ToolExecuteCode, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleRunTests is the M2-T4 /run_tests route. Mirrors
// handleExecuteCode with a different body shape and a different
// envelope tool; dispatch goes to PodExecutor.RunTests (M2-T4b).
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
	if a.exec == nil {
		writeError(w, http.StatusServiceUnavailable, "pod executor not configured")
		return
	}
	res, err := a.exec.RunTests(r.Context(), jobID, toolenvelope.ExecuteRequest{
		Tool:           toolenvelope.ToolRunTests,
		Command:        req.Command,
		TimeoutSeconds: req.Timeout,
	})
	if err != nil {
		a.writeExecError(w, r, jobID, toolenvelope.ToolRunTests, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleLint is the M3-T2 commit 2.3 /lint route. Mirrors
// handleExecuteCode and handleRunTests with the lint body
// shape (command + timeout_seconds). The envelope check
// uses the same `a.tools.EnvelopeFor` path; Gate G2's
// TestGateG2ToolEnvelopeBypassImpossible already iterates
// over the closed set and will pick up `lint` once it's
// added to the slice below.
//
// The linter itself runs in the Job Pod via PodExecutor.Lint (M2-T4b,
// ADR-0024).
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
	if a.exec == nil {
		writeError(w, http.StatusServiceUnavailable, "pod executor not configured")
		return
	}
	res, err := a.exec.Lint(r.Context(), jobID, toolenvelope.ExecuteRequest{
		Tool:           toolenvelope.ToolLint,
		Command:        req.Command,
		TimeoutSeconds: req.Timeout,
	})
	if err != nil {
		a.writeExecError(w, r, jobID, toolenvelope.ToolLint, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// writeExecError maps a PodExecutor error to the M2-T4b status contract
// and records the outcome in the jobs audit log. The typed errors travel
// from the cmd/ adapter (which translates jobpod.ErrNotFound /
// jobpod.ErrNotRunning), so this file stays free of internal/jobpod.
func (a *API) writeExecError(w http.ResponseWriter, r *http.Request, jobID string, tool toolenvelope.Tool, err error) {
	switch {
	case errors.Is(err, ErrNoPod):
		a.auditReject(r.Context(), jobID, tool, "no job pod")
		writeError(w, http.StatusNotFound, ErrNoPod.Error())
	case errors.Is(err, ErrPodNotRunning):
		a.auditReject(r.Context(), jobID, tool, "pod not running")
		writeError(w, http.StatusConflict, ErrPodNotRunning.Error())
	case errors.Is(err, ErrExecNotConfigured):
		a.auditReject(r.Context(), jobID, tool, "exec not configured")
		writeError(w, http.StatusServiceUnavailable, ErrExecNotConfigured.Error())
	default:
		a.auditReject(r.Context(), jobID, tool, "exec failed: "+err.Error())
		writeError(w, http.StatusInternalServerError, "pod exec failed: "+err.Error())
	}
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
