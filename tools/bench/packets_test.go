package main

import (
	"strings"
	"testing"
)

func TestRenderJudgePacket_ContentAndBlindness(t *testing.T) {
	p := judgePacket{ID: opaquePacketID(7), Goal: sampleGoals[0], ArtifactText: "MY ARTIFACT BODY"}
	out := renderJudgePacket(p)

	for _, want := range []string{
		"PKT-0007",
		sampleGoals[0].Goal,
		"MY ARTIFACT BODY",
		"ACCEPTANCE CRITERIA",
		`"score"`,
		sampleGoals[0].Criteria[0],
	} {
		if !strings.Contains(out, want) {
			t.Errorf("judge packet missing %q\n%s", want, out)
		}
	}

	// Blind: the packet must not name the arm or any model, or the online
	// judge is biased.
	for _, leak := range []string{"dialectical", "single", "qwen", "ornith", "gemma"} {
		if strings.Contains(out, leak) {
			t.Errorf("judge packet leaked experiment condition %q\n%s", leak, out)
		}
	}
}

func TestOpaquePacketID(t *testing.T) {
	if got := opaquePacketID(7); got != "PKT-0007" {
		t.Errorf("opaquePacketID(7) = %q, want PKT-0007", got)
	}
	if got := opaquePacketID(0); got != "PKT-0000" {
		t.Errorf("opaquePacketID(0) = %q, want PKT-0000", got)
	}
}
