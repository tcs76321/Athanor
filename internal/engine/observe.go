package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tcs76321/athanor/internal/engine/acceptance"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/notebook"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// observationInstructions asks the model for exactly one optional file read
// before a repair. The engine executes the read; the model never gets a
// general tool loop or a write/exec surface (ADR-0113).
type observationRequest struct {
	ReadFile *string `json:"read_file"`
}

const observationInstructions = `Before repairing the candidate, you may inspect ONE repository file.
Choose the file most likely to contain the code or test the failure points at.
Reply with one JSON object only:
{"read_file":"relative/path"} or {"read_file":null}
Do not request absolute paths, directories, or more than one file.`

// observeForRepair asks for one file, reads it through the envelope, and
// records it in the notebook so the repair prompt sees it. It is best-effort:
// a malformed request, a disallowed path, or a read failure is audited and
// ignored, never a job failure.
func (e *Engine) observeForRepair(ctx context.Context, j job.Job, p project.Project,
	t project.Task, role string) {

	if e.runner == nil {
		return
	}
	resp, err := e.call(ctx, j, p, t, llm.PhaseRefining, role, observationInstructions, nil)
	if err != nil {
		e.audit(ctx, j.ID, map[string]any{"event": "observation_request_failed", "error": err.Error()})
		return
	}
	req, perr := acceptance.ParseVerdict[observationRequest](resp.Content)
	if perr != nil {
		e.audit(ctx, j.ID, map[string]any{"event": "observation_request_unparsed"})
		return
	}
	if req.ReadFile == nil {
		return
	}
	path := strings.TrimSpace(*req.ReadFile)
	if path == "" {
		return
	}
	res, rerr := e.runner.ReadFile(ctx, j.ID, toolenvelope.ReadFileRequest{
		Tool: toolenvelope.ToolReadFile, Path: path, MaxBytes: 8 << 10,
	})
	if rerr != nil {
		e.audit(ctx, j.ID, map[string]any{
			"event": "observation_read_failed", "path": path, "error": rerr.Error(),
		})
		return
	}
	content := res.Content
	if len(content) > 4000 {
		content = content[:4000] + "…"
	}
	e.note(ctx, j.ID, p.ID, notebook.KindObservation, "observing",
		fmt.Sprintf("read %s (%d bytes):\n%s", res.Path, res.Bytes, content))
	e.audit(ctx, j.ID, map[string]any{
		"event": "observation_read", "path": res.Path, "bytes": res.Bytes, "truncated": res.Truncated,
	})
	slog.Debug("engine: observation read", "job", j.ID, "path", res.Path)
}
