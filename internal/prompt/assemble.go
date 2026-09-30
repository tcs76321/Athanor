package prompt

import (
	"fmt"
	"strings"

	"github.com/tcs76321/athanor/internal/llm"
)

// runtimePolicy returns the tier-3 (§11.1) phase instructions from §13.1.
// Every phase pins its purpose; judgment phases restate determinism.
func runtimePolicy(phase string) (string, error) {
	switch phase {
	case llm.PhasePlanning:
		return "PHASE: PLANNING (low temperature 0.2).\nRead the task, project context, and acceptance criteria. Identify missing\ninformation. Propose an implementation plan including tests and\ndocumentation updates.", nil
	case llm.PhaseDiverging:
		return "PHASE: DIVERGENCE (high temperature).\nGenerate candidate solutions that genuinely differ in approach. Explore\northogonal options; avoid premature convergence on one strategy.", nil
	case llm.PhaseEvaluating:
		return "PHASE: EVALUATION (temperature 0.0 — maximally deterministic).\nCheck the work strictly against each acceptance criterion. Identify\nfailures precisely. Do not improvise criteria that were not stated.", nil
	case llm.PhaseReflecting:
		return "PHASE: REFLECTION.\nAnalyze why candidates failed. Identify missing constraints. Propose\nimprovements or hybrid approaches.", nil
	case llm.PhaseSynthesizing:
		return "PHASE: SYNTHESIS (low temperature 0.2).\nProduce the final artifact, complete and self-contained. No preamble,\nnarration, or meta-commentary before or after the artifact. If a\nsafety-critical limitation exists, append a single trailing line\nbeginning with LIMITATION: and no other text.", nil
	case llm.PhaseComparing:
		return "PHASE: COMPARISON (temperature 0.0 — maximally deterministic).\nCompare the candidate artifact against the previous best using the\nacceptance criteria and evaluation results. Output a structured verdict:\nwinner (new|previous|none), confidence (0.0-1.0), reasons, and missing\nrequirements.", nil
	default:
		return "", fmt.Errorf("prompt: no runtime policy defined for phase %q (§13.1)", phase)
	}
}

// sectionSpec is one rendered §11.2 section together with the §10.5 tier
// it belongs to (ADR-0023 §2). Rendering happens for every populated
// section; the ladder then decides which tiers survive.
type sectionSpec struct {
	name string
	tier Tier
	text string
}

// renderSections renders every populated §11.2 section, in construction
// order. An empty section is omitted (the pre-T5 `add` contract), which
// is also how a tier becomes "absent" for the ladder: nothing to drop.
func renderSections(in Input) ([]sectionSpec, error) {
	policy, err := runtimePolicy(in.Phase)
	if err != nil {
		return nil, err
	}
	all := []sectionSpec{
		{SectionSystem, TierStaticSystem, staticSystem},
		{SectionSecurityAndTools, TierStaticSystem, securityAndToolsFor(in.Tools)},
		{SectionRuntimePolicy, TierStaticSystem, policy},
		{SectionProjectContext, TierTaskCriteria, projectContext(in.Project)},
		{SectionTaskContext, TierTaskCriteria, taskContext(in.Task)},
		{SectionAcceptanceCriteria, TierTaskCriteria, criteria(in.Criteria)},
		{SectionActiveChunk, TierWorkingSet, activeChunkOrEmpty(in.ActiveChunk)},
		{SectionCorrections, TierCorrections, correctionsSection(in.Corrections)},
		{SectionEpisodic, TierEpisodic, episodicSection(in.Episodic)},
		{SectionDormantIndex, TierDormantIndex, dormantIndexSection(in.DormantIndex)},
		{SectionUserPreferences, TierTaskCriteria, userPreferencesSection(in.UserPreferences)},
		{SectionCandidateArtifacts, TierWorkingSet, candidateArtifactsSection(in.Candidates)},
		{SectionEvaluationInstructions, TierInstructions, in.EvaluationInstructions},
		{SectionStrategyNotes, TierInstructions, strategyNotesSection(in.StrategyNotes)},
		{SectionInterruptionNotes, TierInstructions, interruptionNotesSection(in.InterruptionNotes)},
	}
	out := make([]sectionSpec, 0, len(all))
	for _, s := range all {
		if strings.TrimSpace(s.text) == "" {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// Assemble builds the deterministic prompt. It is a pure function:
// identical inputs produce byte-identical output.
//
// M5-T5 (§10.5, ADR-0023): assembly is two-phase. Every populated section
// is rendered first, priced per tier, and handed to the eviction ladder
// with the caller's ceiling and the job's suppression set; then only the
// surviving tiers are emitted — always in §11.2 construction order, which
// the ladder never touches. With no ceiling (or nothing over it) the
// output is byte-identical to the pre-T5 assembler.
func Assemble(in Input) (Result, error) {
	if in.Project.Name == "" {
		return Result{}, fmt.Errorf("prompt: project name is required")
	}
	if in.Task.Title == "" {
		return Result{}, fmt.Errorf("prompt: task title is required")
	}
	specs, err := renderSections(in)
	if err != nil {
		return Result{}, err
	}

	weights := make(map[Tier]int, len(ladderOrder)+3)
	for _, s := range specs {
		weights[s.tier] += EstimateTokens(s.text)
	}
	report := applyLadder(weights, in.Ceiling, in.Suppressed)

	var sections []Section
	total := 0
	var b strings.Builder
	systemText := &strings.Builder{}
	userText := &strings.Builder{}
	for _, s := range specs {
		if report.Suppresses(s.tier) {
			continue
		}
		sec := Section{Name: s.name, Text: s.text, Tokens: EstimateTokens(s.text)}
		sections = append(sections, sec)
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(sec.Text)
		total += sec.Tokens
		// §11.2 sections 1–3 form the system message; the rest the user
		// message. The split point is fixed, so output is deterministic.
		switch sec.Name {
		case SectionSystem, SectionSecurityAndTools, SectionRuntimePolicy:
			if systemText.Len() > 0 {
				systemText.WriteString("\n\n")
			}
			systemText.WriteString(sec.Text)
		default:
			if userText.Len() > 0 {
				userText.WriteString("\n\n")
			}
			userText.WriteString(sec.Text)
		}
	}

	return Result{
		Text:       b.String(),
		Sections:   sections,
		TotalToken: total,
		Eviction:   report,
		Messages: []llm.Message{
			{Role: "system", Content: systemText.String()},
			{Role: "user", Content: userText.String()},
		},
	}, nil
}

func projectContext(p Project) string {
	var b strings.Builder
	b.WriteString("PROJECT\n")
	fmt.Fprintf(&b, "name: %s\n", p.Name)
	fmt.Fprintf(&b, "archetype: %s\n", p.Archetype)
	fmt.Fprintf(&b, "goal: %s", p.Goal)
	return b.String()
}

func taskContext(t Task) string {
	var b strings.Builder
	b.WriteString("TASK\n")
	fmt.Fprintf(&b, "title: %s\n", t.Title)
	if t.Description != "" {
		fmt.Fprintf(&b, "description: %s", t.Description)
	}
	return b.String()
}

// criteria renders acceptance criteria as a numbered list. Input order is
// preserved verbatim — criteria are requirements, not suggestions.
func criteria(cs []string) string {
	if len(cs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("ACCEPTANCE CRITERIA (all must be met)\n")
	for i, c := range cs {
		fmt.Fprintf(&b, "%d. %s", i+1, c)
		if i < len(cs)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
