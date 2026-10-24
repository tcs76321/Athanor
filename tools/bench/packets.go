package main

import (
	"fmt"
	"strings"
)

// judgePacket is the paste-ready unit for the online-agent scoring
// channel. It is deliberately blind to the arm and the model: the judge
// must not know whether the artifact came from the single-shot or the
// dialectical arm, or which model produced it, or the score would be
// biased. The runner keeps the opaque ID → metadata mapping.
type judgePacket struct {
	ID           string // opaque transcription key, e.g. "PKT-0007"
	Goal         sampleGoal
	ArtifactText string
}

// opaquePacketID returns the nth opaque transcription key. It encodes no
// arm or model information.
func opaquePacketID(n int) string { return fmt.Sprintf("PKT-%04d", n) }

// renderJudgePacket returns a self-contained prompt the operator can
// paste into a browser agent: the goal, the acceptance criteria, the
// artifact verbatim, and a strict output schema. Nothing identifying the
// experiment condition is included.
func renderJudgePacket(p judgePacket) string {
	var b strings.Builder
	b.WriteString("# Athanor M3-T7 — artifact judge packet\n\n")
	fmt.Fprintf(&b, "PACKET_ID: %s\n\n", p.ID)
	fmt.Fprintf(&b, "GOAL: %s\n\n", p.Goal.Goal)
	fmt.Fprintf(&b, "ARCHETYPE: %s\n\n", p.Goal.Archetype)
	b.WriteString("ACCEPTANCE CRITERIA:\n")
	for i, c := range p.Goal.Criteria {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, c)
	}
	b.WriteString("\nARTIFACT (verbatim):\n---\n")
	b.WriteString(p.ArtifactText)
	if !strings.HasSuffix(p.ArtifactText, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("---\n\n")
	b.WriteString("Evaluate the artifact against EACH acceptance criterion. First list the\n")
	b.WriteString("criteria that are fully met and those that are missing or only partially met.\n")
	b.WriteString("Then assign a score from 0.0 to 1.0: use 1.0 only when every criterion is\n")
	b.WriteString("fully met with no material defect; subtract for any missing or partial\n")
	b.WriteString("criterion. Do not default to 1.0.\n")
	b.WriteString("Output JSON only, matching this schema exactly:\n")
	b.WriteString(`{"score": 0.0, "criteria_met": [], "criteria_missing": [], "notes": ""}`)
	b.WriteString("\n")
	return b.String()
}
