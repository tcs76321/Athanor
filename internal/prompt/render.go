package prompt

import (
	"fmt"
	"strings"
)

// Rendering for the M5-T5 tier payloads (§11.2 order, ADR-0023 §2).
//
// Every function here is pure and deterministic: the same payload renders
// the same bytes, which is what lets `Assemble` price a tier before the
// ladder runs and still emit a byte-identical section when nothing is
// evicted.

// securityAndToolsFor renders §11.2 §2 (tier 1).
//
// An empty envelope keeps the tool-less text verbatim: the shipped default
// is `job_pod.default_tools: []`, and the pre-T5 bytes are asserted
// unchanged (ADR-0023 §8). A non-empty envelope lists exactly the operations
// the server-side envelope allows, so the prompt can never promise one the
// route would refuse.
//
// The operations are *engine-executed* (ADR-0080): the Core runs them on the
// candidate after generation; the model cannot invoke them. The wording says
// so explicitly, because a prompt that implies a tool interface the engine
// does not implement invites tool-call transcripts in the artifact (the
// benchmark's `text-e-bio` failure class).
func securityAndToolsFor(tools []string) string {
	if len(tools) == 0 {
		return securityAndTools
	}
	var b strings.Builder
	b.WriteString("CONTAINMENT RULES (non-negotiable):\n")
	b.WriteString("- You cannot invoke tools. The Core executes operations on your behalf;\n")
	b.WriteString("  you cannot execute code, run commands, read or write files, or access the network.\n")
	b.WriteString("- Do not emit tool calls, JSON tool envelopes, or planning narration.\n")
	b.WriteString("  Output only the requested artifact.\n")
	b.WriteString("- Never claim to have taken an action you did not take.\n")
	b.WriteString("- If the task cannot be completed with the operations below, state what\n")
	b.WriteString("  is missing instead of pretending to act.\n")
	b.WriteString("\nENGINE-EXECUTED OPERATIONS (the Core may run these; you cannot invoke them):\n")
	for _, t := range tools {
		fmt.Fprintf(&b, "- %s\n", t)
	}
	return strings.TrimRight(b.String(), "\n")
}

// activeChunkSection renders §11.2 §7 (tier 3): the active division chunk
// at full fidelity. Division is lossless (ADR-0021), so the bytes are
// copied verbatim — the assembler never truncates this section.
func activeChunkSection(c *ChunkText) string {
	var b strings.Builder
	b.WriteString("ACTIVE CODE CHUNK (full fidelity, verbatim)\n")
	fmt.Fprintf(&b, "chunk_id: %s\n", c.ID)
	if c.RelPath != "" {
		fmt.Fprintf(&b, "path: %s\n", c.RelPath)
	}
	if c.LineStart > 0 || c.LineEnd > 0 {
		fmt.Fprintf(&b, "lines: %d-%d\n", c.LineStart, c.LineEnd)
	}
	if c.Kind != "" {
		fmt.Fprintf(&b, "kind: %s\n", c.Kind)
	}
	b.WriteString("--- begin chunk ---\n")
	b.WriteString(c.Text)
	b.WriteString("\n--- end chunk ---")
	return b.String()
}

// dormantIndexSection renders §11.2 §10 (tier 6): the §10.1 table of
// contents plus §10.4's swap invitation. The invitation lives here rather
// than in §11.2 §2 because the index is what it points at — rendering it
// with the index keeps the tier weights exact and guarantees the model is
// never told to request something the prompt does not list.
func dormantIndexSection(lines []IndexLine) string {
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("DORMANT INDEX (chunks held intact outside this context)\n")
	for _, l := range lines {
		fmt.Fprintf(&b, "- chunk_id=%s", l.ChunkID)
		if l.RelPath != "" {
			fmt.Fprintf(&b, " | path=%s", l.RelPath)
		}
		if l.LineStart > 0 || l.LineEnd > 0 {
			fmt.Fprintf(&b, " | lines=%d-%d", l.LineStart, l.LineEnd)
		}
		if l.Kind != "" {
			fmt.Fprintf(&b, " | kind=%s", l.Kind)
		}
		summary := l.Summary
		if strings.TrimSpace(summary) == "" {
			summary = "(summary pending)"
		}
		fmt.Fprintf(&b, " | %s\n", summary)
	}
	b.WriteString("The Core keeps one chunk active at a time and may load a dormant chunk\n")
	b.WriteString("when the context budget requires it.")
	return b.String()
}

// activeChunkOrEmpty renders §11.2 §7 when a chunk is present; a nil
// chunk yields "" so the section (and its tier, when nothing else is in
// tier 3) is absent from the assembly.
func activeChunkOrEmpty(c *ChunkText) string {
	if c == nil {
		return ""
	}
	return activeChunkSection(c)
}

// numbered renders a list payload with a heading and 1-based numbering,
// preserving input order verbatim (requirements, corrections, and notes
// are all order-significant).
func numbered(heading string, items []string) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(heading)
	b.WriteString("\n")
	for i, it := range items {
		fmt.Fprintf(&b, "%d. %s", i+1, it)
		if i < len(items)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// bulleted is `numbered` without numbering, for preferences and notes.
func bulleted(heading string, items []string) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(heading)
	b.WriteString("\n")
	for i, it := range items {
		fmt.Fprintf(&b, "- %s", it)
		if i < len(items)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// correctionsSection renders §11.2 §8 (tier 4). The producer is M6.
func correctionsSection(items []string) string {
	return numbered("RELEVANT CORRECTIONS (learned from earlier rejections)", items)
}

// episodicSection renders §11.2 §9 (tier 5). The producer is M6.
func episodicSection(items []string) string {
	return numbered("EPISODIC CONTEXT (compacted summaries of earlier work in this project)", items)
}

// userPreferencesSection renders §11.2 §11 (tier 2).
func userPreferencesSection(items []string) string {
	return bulleted("USER PREFERENCES (style and format)", items)
}

// candidateArtifactsSection renders §11.2 §12 (tier 3): prior outputs the
// phase works on, at full fidelity. This replaces the pre-T5 practice of
// concatenating candidate bytes into the §13 evaluation instructions —
// a candidate a phase must refine cannot be evictable out from under it.
func candidateArtifactsSection(items []CandidateArtifact) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	for i, c := range items {
		if i > 0 {
			b.WriteString("\n\n")
		}
		label := c.Kind
		if strings.TrimSpace(label) == "" {
			label = "artifact"
		}
		fmt.Fprintf(&b, "CANDIDATE %s (full fidelity, verbatim)\n", strings.ToUpper(label))
		b.WriteString("--- begin artifact ---\n")
		b.WriteString(c.Content)
		b.WriteString("\n--- end artifact ---")
	}
	return b.String()
}

// strategyNotesSection renders §11.2 §14 (tier 7).
func strategyNotesSection(items []string) string {
	return bulleted("STRATEGY NOTES (active insights for this phase)", items)
}

// interruptionNotesSection renders §11.2 §15 (tier 7). The producer is M6
// watch mode; the section exists so the ordering is complete.
func interruptionNotesSection(items []string) string {
	return bulleted("USER NOTES (from live watch mode)", items)
}
