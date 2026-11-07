package engine

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/process"
	"github.com/tcs76321/athanor/internal/store"
)

// evaluateProcess writes the ADR-0119 process scorecard at terminal state. It
// is deterministic and derived from the event log, artifacts, and evaluation
// records; no model call is involved.
func (e *Engine) evaluateProcess(ctx context.Context, j job.Job) {
	if e.db == nil {
		return
	}
	events, err := e.db.QueryEvents(ctx, store.EventFilter{JobID: j.ID})
	if err != nil {
		slog.Warn("engine: reading process events", "job", j.ID, "err", err)
		return
	}
	in := process.Input{Cycles: 1}
	seenEval := false
	for _, ev := range events {
		var d struct {
			Event            string `json:"event"`
			Hits             int    `json:"hits"`
			PromptTokens     int    `json:"prompt_tokens"`
			CompletionTokens int    `json:"completion_tokens"`
		}
		if json.Unmarshal([]byte(ev.DataJSON), &d) != nil {
			continue
		}
		switch d.Event {
		case "process_eval":
			seenEval = true
		case "divergence_start":
			in.Cycles++
		case "self_refine":
			in.Repairs++
		case "reflection":
			in.Reflections++
		case "replanned":
			in.Replans++
		case "observation_read":
			in.Observations++
		case "memory_injected":
			in.MemoryHits += d.Hits
		case "llm_call":
			in.Tokens += d.PromptTokens + d.CompletionTokens
		}
	}
	if seenEval {
		return
	}
	// divergence_start is emitted once per cycle; the loop starts at 0.
	in.Cycles--
	if in.Cycles < 1 {
		in.Cycles = 1
	}
	if candidates, err := e.artifacts.ListByJob(ctx, j.ID); err == nil {
		for _, a := range candidates {
			if a.Kind == artifact.KindProposal {
				in.Candidates++
			}
		}
	}
	if e.eval != nil {
		if records, err := e.eval.ListByJob(ctx, j.ID); err == nil {
			for _, r := range records {
				if r.Score > in.BestCandidateScore {
					in.BestCandidateScore = r.Score
				}
			}
		}
	}
	if p, t, err := e.contexts(ctx, j); err == nil {
		if final, ferr := e.artifacts.LatestForJob(ctx, j.ID, finalKindFor(capabilityFor(p, t))); ferr == nil {
			in.FinalAccepted = j.State == job.StateCompleted && final.Status == artifact.StatusAccepted
			if in.FinalAccepted && e.eval != nil {
				if recs, rerr := e.eval.ListByArtifact(ctx, final.ID); rerr == nil {
					for _, r := range recs {
						if r.Score > in.FinalScore {
							in.FinalScore = r.Score
						}
					}
				}
			}
		}
	}
	if j.StartedAt != nil && j.FinishedAt != nil {
		in.Wall = j.FinishedAt.Sub(*j.StartedAt)
	} else {
		in.Wall = time.Duration(0)
	}
	card := process.Evaluate(in)
	e.audit(ctx, j.ID, map[string]any{
		"event": "process_eval", "verdict": card.Verdict,
		"one_shot_sufficient": card.OneShotSufficient,
		"escalated":           card.Escalated,
		"rescued":             card.Rescued,
		"wasted_compute":      card.WastedCompute,
		"regression":          card.Regression,
		"candidates":          in.Candidates,
		"cycles":              in.Cycles,
		"repairs":             in.Repairs,
		"reflections":         in.Reflections,
		"replans":             in.Replans,
		"observations":        in.Observations,
		"memory_hits":         in.MemoryHits,
		"tokens":              in.Tokens,
		"wall_ms":             in.Wall.Milliseconds(),
	})
}
