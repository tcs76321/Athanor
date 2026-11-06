package ui

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tcs76321/athanor/internal/store"
)

// phaseStat is one row of the watch page's phase timeline. It is derived from
// the append-only event log: a transition opens a phase, the next transition
// closes it, and llm_call rows add the phase's real token spend.
type phaseStat struct {
	Phase      string
	Started    time.Time
	Ended      time.Time
	Duration   string
	Calls      int
	Prompt     int
	Completion int
	Reasoning  int
}

func (p phaseStat) Tokens() int { return p.Prompt + p.Completion + p.Reasoning }

// phaseTimeline builds the phase table for a job. Events are read oldest-first
// and bounded: a timeline is a reading aid, not a second full trace.
func (u *UI) phaseTimeline(ctx context.Context, jobID string) []phaseStat {
	events, err := u.deps.Store.QueryEvents(ctx, store.EventFilter{JobID: jobID, Limit: 5000})
	if err != nil {
		return nil
	}
	var out []phaseStat
	byName := map[string]int{}
	var current *phaseStat
	now := time.Now()
	for _, ev := range events {
		var d map[string]any
		if json.Unmarshal([]byte(ev.DataJSON), &d) != nil {
			continue
		}
		name, _ := d["event"].(string)
		switch name {
		case "transition":
			to, _ := d["to"].(string)
			if to == "" {
				continue
			}
			if current != nil {
				current.Ended = ev.TS
			}
			out = append(out, phaseStat{Phase: to, Started: ev.TS})
			current = &out[len(out)-1]
			byName[to] = len(out) - 1
		case "llm_call":
			phase, _ := d["phase"].(string)
			idx, ok := byName[phase]
			if !ok {
				continue
			}
			stat := &out[idx]
			stat.Calls++
			stat.Prompt += anyInt(d["prompt_tokens"])
			stat.Completion += anyInt(d["completion_tokens"])
			stat.Reasoning += anyInt(d["reasoning_tokens"])
		}
	}
	for i := range out {
		end := out[i].Ended
		if end.IsZero() {
			end = now
		}
		if !out[i].Started.IsZero() {
			out[i].Duration = end.Sub(out[i].Started).Round(time.Second).String()
		}
	}
	return out
}

func anyInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	default:
		return 0
	}
}
