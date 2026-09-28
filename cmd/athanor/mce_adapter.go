package main

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/mce"
	"github.com/tcs76321/athanor/internal/mce/division"
	"github.com/tcs76321/athanor/internal/prompt"
)

// maxSummaryRunes bounds a Dormant Index summary even if the model ignores the
// "one line" instruction (§10.1).
const maxSummaryRunes = 200

// mceSummarizer adapts the `wide` persona to mce.Summarizer (ADR-0021 §7). It
// is the only place the MCE's Dormant Index summaries touch a model — the MCE
// package itself stays free of internal/llm, preserving Gate G2 rule 1's
// intent. Summaries run at temperature 0.0 so the index is reproducible.
type mceSummarizer struct {
	registry *llm.Registry
	client   *llm.Client
}

// Compile-time proof that the adapter satisfies the MCE seam.
var _ mce.Summarizer = (*mceSummarizer)(nil)

func newMCESummarizer(registry *llm.Registry, client *llm.Client) *mceSummarizer {
	return &mceSummarizer{registry: registry, client: client}
}

// Summarize requests a one-line Dormant Index summary for one chunk.
func (s *mceSummarizer) Summarize(ctx context.Context, chunk division.Chunk) (string, error) {
	persona, ok := s.registry.Persona(llm.RoleWide)
	if !ok {
		return "", fmt.Errorf("mce summarizer: persona %q missing from registry", llm.RoleWide)
	}
	resp, err := s.client.Chat(ctx, llm.Request{
		Model: persona.Model,
		Messages: prompt.SummarizeChunkMessages(
			chunk.FilePath, chunk.Lang, chunk.LineStart, chunk.LineEnd, string(chunk.Content),
		),
		Temperature:   0.0,
		ContextTarget: persona.ContextTarget,
	})
	if err != nil {
		return "", err
	}
	return clampLine(resp.Content, maxSummaryRunes), nil
}

// clampLine returns the first line of s, trimmed and bounded to max bytes on a
// rune boundary.
func clampLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut])
}
