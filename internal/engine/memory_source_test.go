package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/mce"
	"github.com/tcs76321/athanor/internal/store"
)

type fakeMemory struct {
	hits  []mce.MemoryHit
	calls int
}

func (f *fakeMemory) RelevantMemory(context.Context, string, string, int) ([]mce.MemoryHit, error) {
	f.calls++
	return f.hits, nil
}

// TestMemoryReachesEpisodicTier proves the M5-T7 retriever is consumed by the
// prompt: a compacted memo is rendered in tier 5 and audited.
func TestMemoryReachesEpisodicTier(t *testing.T) {
	env := newEnv(t)
	mem := &fakeMemory{hits: []mce.MemoryHit{
		{ID: "memo-1", Kind: mce.SourceMemo, Content: "The project uses snake_case for internal fields."},
		{ID: "chunk-1", Kind: mce.SourceChunk},
	}}
	env.eng.memorySource = mem

	jobID := env.submit(t)
	env.eng.Run(context.Background(), jobID)

	if mem.calls == 0 {
		t.Fatal("memory source was never queried")
	}
	foundPrompt, foundEvent := false, false
	for _, p := range env.ollama.promptTexts() {
		if strings.Contains(p, "The project uses snake_case for internal fields.") {
			foundPrompt = true
			break
		}
	}
	events, err := env.db.QueryEvents(context.Background(), store.EventFilter{JobID: jobID})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if strings.Contains(e.DataJSON, "memory_injected") {
			foundEvent = true
			break
		}
	}
	if !foundPrompt {
		t.Error("compacted memory did not reach the prompt")
	}
	if !foundEvent {
		t.Error("memory injection was not audited")
	}
}
