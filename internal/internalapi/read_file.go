package internalapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/tcs76321/athanor/internal/airlock/paths"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// readFileMaxBytes bounds one observation. The engine asks for less; this is
// the server's hard ceiling.
const readFileMaxBytes = 16 << 10

// handleReadFile is the ADR-0113 read-only observation route. It is
// envelope-gated like every other tool and confined to the staged repository
// snapshot; it cannot read host files, write, execute, or follow an absolute
// path.
func (a *API) handleReadFile(w http.ResponseWriter, r *http.Request) {
	jobID := jobIDFromContext(r.Context())
	if jobID == "" {
		writeError(w, http.StatusUnauthorized, "missing authenticated job id")
		return
	}
	var req toolenvelope.ReadFileRequest
	if !decodeBody(w, r, &req) {
		return
	}
	req.Path = strings.TrimSpace(req.Path)
	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "read_file: path is required")
		return
	}
	if _, err := paths.Resolve("/workspace/repo", req.Path); err != nil {
		writeError(w, http.StatusBadRequest, "read_file: path must be repository-relative")
		return
	}
	if req.MaxBytes <= 0 || req.MaxBytes > readFileMaxBytes {
		req.MaxBytes = readFileMaxBytes
	}
	if a.tools == nil {
		a.auditReject(r.Context(), jobID, toolenvelope.ToolReadFile, "no envelope lookup configured")
		writeError(w, http.StatusServiceUnavailable, "tool envelope lookup not configured")
		return
	}
	env, err := a.tools.EnvelopeFor(r.Context(), jobID, a.defaultEnvelope)
	if err != nil {
		a.auditReject(r.Context(), jobID, toolenvelope.ToolReadFile, err.Error())
		writeError(w, http.StatusInternalServerError, "envelope lookup failed: "+err.Error())
		return
	}
	if !env.Allows(toolenvelope.ToolReadFile) {
		a.auditReject(r.Context(), jobID, toolenvelope.ToolReadFile, "tool not in job envelope")
		writeError(w, http.StatusForbidden, ErrToolDisallowed.Error())
		return
	}
	a.auditAllow(r.Context(), jobID, toolenvelope.ToolReadFile, "path="+req.Path)
	if a.exec == nil {
		writeError(w, http.StatusServiceUnavailable, "pod executor not configured")
		return
	}
	res, err := a.exec.ReadFile(r.Context(), jobID, req)
	if err != nil {
		if errors.Is(err, ErrNoPod) || errors.Is(err, ErrPodNotRunning) || errors.Is(err, ErrExecNotConfigured) {
			a.writeExecError(w, r, jobID, toolenvelope.ToolReadFile, err)
			return
		}
		writeError(w, http.StatusBadRequest, "read_file: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
