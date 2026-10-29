// Telemetry reduction over the engine's event log (ADR-0073 §3). The engine
// already emits per-call phase/persona/model/token counts and its lever
// counters; the probe previously read only the aggregate strategy_outcome row.
// This file turns that stream into per-job, per-phase, and per-persona numbers
// so a soak produces model-comparison and engine-attribution data, not just a
// pass/fail.
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// jobTelemetry is one job's event-log reduction. Every field is best-effort: a
// job with no events yields an all-zero telemetry.
type jobTelemetry struct {
	Calls            int            `json:"calls"`
	PromptTokens     int            `json:"prompt_tokens"`
	CompletionTokens int            `json:"completion_tokens"`
	ByPhaseCalls     map[string]int `json:"by_phase_calls,omitempty"`
	ByPhaseTokens    map[string]int `json:"by_phase_tokens,omitempty"`
	ByPersonaTokens  map[string]int `json:"by_persona_tokens,omitempty"`
	Models           []string       `json:"models,omitempty"`
	RepairLoops      int            `json:"repair_loops,omitempty"`
	ReflectLoops     int            `json:"reflect_loops,omitempty"`
	Rerolls          int            `json:"divergence_rerolls,omitempty"`
	CandidateRaise   int            `json:"candidate_raise,omitempty"`
	KVPressure       int            `json:"kv_pressure,omitempty"`
	KVWarn           int            `json:"kv_warn,omitempty"`
	KVCritical       int            `json:"kv_critical,omitempty"`
	ContextFloors    int            `json:"context_floor_violations,omitempty"`
	Notes            int            `json:"strategy_notes_injected,omitempty"`
	Corrections      int            `json:"corrections_injected,omitempty"`
	GitCommitted     int            `json:"git_committed,omitempty"`
	GitCommitFailed  int            `json:"git_commit_failed,omitempty"`
	// P1 MCE mechanism metrics (ADR-0075 sensitivity): what retrieval put in
	// front of the model and what compaction paid for it.
	DormantRows      int `json:"dormant_index_rows,omitempty"`
	WorkingSetChunks int `json:"working_set_chunks,omitempty"`
	WorkingSetBytes  int `json:"working_set_bytes,omitempty"`
	MemoryHits       int `json:"memory_hits,omitempty"`
	MemoryBytes      int `json:"memory_bytes,omitempty"`
	Compactions      int `json:"compactions,omitempty"`
	CompactionSource int `json:"compaction_source_bytes,omitempty"`
	CompactionBytes  int `json:"compaction_bytes,omitempty"`
}

// fillTelemetry reduces the job's event log into m.Telemetry (ADR-0073 §3). It
// scans the job's events once and dispatches on the event name, so a long soak
// pays one read per job rather than one per signal. Errors are non-fatal: the
// row simply carries no telemetry.
func (m *jobMetrics) fillTelemetry(db *sql.DB) {
	rows, err := db.Query(`SELECT data_json FROM events WHERE job_id = ? ORDER BY id`, m.JobID)
	if err != nil {
		return
	}
	defer func() { _ = rows.Close() }()
	t := jobTelemetry{}
	seenModel := map[string]bool{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return
		}
		// The event names read here are the engine's §28.1 vocabulary; an
		// unknown event is ignored, so a new engine event cannot corrupt the
		// reduction.
		var d struct {
			Event            string `json:"event"`
			Phase            string `json:"phase"`
			Persona          string `json:"persona"`
			Model            string `json:"model"`
			PromptTokens     int    `json:"prompt_tokens"`
			CompletionTokens int    `json:"completion_tokens"`
			Action           string `json:"action"`
			Rows             int    `json:"rows"`
			Chunks           int    `json:"chunks"`
			Bytes            int    `json:"bytes"`
			Hits             int    `json:"hits"`
			SourceBytes      int    `json:"source_bytes"`
			CompactedBytes   int    `json:"compacted_bytes"`
		}
		if json.Unmarshal([]byte(raw), &d) != nil {
			continue
		}
		switch d.Event {
		case "llm_call":
			t.Calls++
			t.PromptTokens += d.PromptTokens
			t.CompletionTokens += d.CompletionTokens
			if d.Phase != "" {
				if t.ByPhaseCalls == nil {
					t.ByPhaseCalls = map[string]int{}
					t.ByPhaseTokens = map[string]int{}
				}
				t.ByPhaseCalls[d.Phase]++
				t.ByPhaseTokens[d.Phase] += d.PromptTokens + d.CompletionTokens
			}
			if d.Persona != "" {
				if t.ByPersonaTokens == nil {
					t.ByPersonaTokens = map[string]int{}
				}
				t.ByPersonaTokens[d.Persona] += d.PromptTokens + d.CompletionTokens
			}
			if d.Model != "" && !seenModel[d.Model] {
				seenModel[d.Model] = true
				t.Models = append(t.Models, d.Model)
			}
		case "self_refine":
			t.RepairLoops++
		case "reflection":
			t.ReflectLoops++
		case "divergence_reroll":
			t.Rerolls++
		case "difficulty_hint":
			t.CandidateRaise++
		case "kv_cache_pressure":
			t.KVPressure++
			switch d.Action {
			case "warn":
				t.KVWarn++
			case "critical":
				t.KVCritical++
			}
		case "dormant_index_sourced":
			t.DormantRows += d.Rows
		case "working_set_sourced":
			t.WorkingSetChunks += d.Chunks
			t.WorkingSetBytes += d.Bytes
		case "memory_injected":
			t.MemoryHits += d.Hits
			t.MemoryBytes += d.Bytes
		case "memory_compacted":
			t.Compactions++
			t.CompactionSource += d.SourceBytes
			t.CompactionBytes += d.CompactedBytes
		case "context_floor_violation":
			t.ContextFloors++
		case "strategy_notes_injected":
			t.Notes++
		case "corrections_injected":
			t.Corrections++
		case "git_committed":
			t.GitCommitted++
		case "git_commit_failed":
			t.GitCommitFailed++
		}
	}
	sort.Strings(t.Models)
	m.Telemetry = &t
}

// telemetryRow aggregates telemetry across the jobs of one model.
type telemetryRow struct {
	Model            string
	Jobs             int
	Calls            int
	PromptTokens     int
	CompletionTokens int
	Repairs          int
	Reflections      int
	Rerolls          int
	Raises           int
	KVPressure       int
	KVWarn           int
	KVCritical       int
	ContextFloors    int
	DormantRows      int
	WorkingChunks    int
	WorkingBytes     int
	MemoryHits       int
	MemoryBytes      int
	Compactions      int
	CompactionSource int
	CompactionBytes  int
}

// telemetryTable aggregates per-model telemetry over the rows that carry it.
func telemetryTable(metrics []jobMetrics) []telemetryRow {
	agg := map[string]*telemetryRow{}
	var order []string
	for _, m := range metrics {
		if m.Telemetry == nil {
			continue
		}
		r, ok := agg[m.ModelLabel]
		if !ok {
			r = &telemetryRow{Model: m.ModelLabel}
			agg[m.ModelLabel] = r
			order = append(order, m.ModelLabel)
		}
		t := m.Telemetry
		r.Jobs++
		r.Calls += t.Calls
		r.PromptTokens += t.PromptTokens
		r.CompletionTokens += t.CompletionTokens
		r.Repairs += t.RepairLoops
		r.Reflections += t.ReflectLoops
		r.Rerolls += t.Rerolls
		r.Raises += t.CandidateRaise
		r.KVPressure += t.KVPressure
		r.KVWarn += t.KVWarn
		r.KVCritical += t.KVCritical
		r.ContextFloors += t.ContextFloors
		r.DormantRows += t.DormantRows
		r.WorkingChunks += t.WorkingSetChunks
		r.WorkingBytes += t.WorkingSetBytes
		r.MemoryHits += t.MemoryHits
		r.MemoryBytes += t.MemoryBytes
		r.Compactions += t.Compactions
		r.CompactionSource += t.CompactionSource
		r.CompactionBytes += t.CompactionBytes
	}
	sort.Strings(order)
	out := make([]telemetryRow, 0, len(order))
	for _, k := range order {
		out = append(out, *agg[k])
	}
	return out
}

// phaseTelemetry aggregates calls and tokens by phase across every model.
func phaseTelemetry(metrics []jobMetrics) []struct {
	Phase  string
	Calls  int
	Tokens int
} {
	type row struct{ calls, tokens int }
	agg := map[string]*row{}
	var order []string
	for _, m := range metrics {
		if m.Telemetry == nil {
			continue
		}
		for phase, calls := range m.Telemetry.ByPhaseCalls {
			r, ok := agg[phase]
			if !ok {
				r = &row{}
				agg[phase] = r
				order = append(order, phase)
			}
			r.calls += calls
			r.tokens += m.Telemetry.ByPhaseTokens[phase]
		}
	}
	sort.Strings(order)
	out := make([]struct {
		Phase  string
		Calls  int
		Tokens int
	}, 0, len(order))
	for _, p := range order {
		out = append(out, struct {
			Phase  string
			Calls  int
			Tokens int
		}{p, agg[p].calls, agg[p].tokens})
	}
	return out
}

// renderTelemetryTables renders the per-model and per-phase telemetry sections
// (ADR-0073 §3) for the rolling report.
func renderTelemetryTables(metrics []jobMetrics) string {
	var b strings.Builder
	perModel := telemetryTable(metrics)
	if len(perModel) == 0 {
		return ""
	}
	b.WriteString("\n## Telemetry — per model (ADR-0073)\n\n")
	b.WriteString("| model | jobs | calls | prompt tok | completion tok | repairs | reflections | rerolls | raises | kv warn | kv critical | floor violations | dormant rows | ws chunks | ws bytes | memory hits | compactions | compacted bytes |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range perModel {
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d |\n",
			r.Model, r.Jobs, r.Calls, r.PromptTokens, r.CompletionTokens,
			r.Repairs, r.Reflections, r.Rerolls, r.Raises, r.KVWarn, r.KVCritical,
			r.ContextFloors, r.DormantRows, r.WorkingChunks, r.WorkingBytes,
			r.MemoryHits, r.Compactions, r.CompactionBytes)
	}
	phases := phaseTelemetry(metrics)
	if len(phases) > 0 {
		b.WriteString("\n## Telemetry — calls by phase (all models)\n\n")
		b.WriteString("| phase | calls | tokens |\n|---|---|---|\n")
		for _, p := range phases {
			fmt.Fprintf(&b, "| %s | %d | %d |\n", p.Phase, p.Calls, p.Tokens)
		}
	}
	return b.String()
}
