package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/prompt"
)

// fakeWorkingSet is a ContextProvider that also exposes the optional P0 pack.
type fakeWorkingSet struct {
	chunk prompt.ChunkText
	calls int
}

func (f *fakeWorkingSet) ActiveChunk(context.Context, ContextQuery) (prompt.ChunkText, bool, error) {
	return prompt.ChunkText{}, false, nil
}

func (f *fakeWorkingSet) DormantIndex(context.Context, ContextQuery) ([]prompt.IndexLine, error) {
	return nil, nil
}

func (f *fakeWorkingSet) WorkingSet(context.Context, ContextQuery, int, int) ([]prompt.ChunkText, error) {
	f.calls++
	return []prompt.ChunkText{f.chunk}, nil
}

// TestWorkingSetIsAutoForCodeOnly pins the shipped default: a code job gets
// the repository pack; a text job does not.
func TestWorkingSetIsAutoForCodeOnly(t *testing.T) {
	env := newEnv(t)
	provider := &fakeWorkingSet{chunk: prompt.ChunkText{
		ID: "chunk-1", RelPath: "main.go", LineStart: 1, LineEnd: 2,
		Kind: "function", Text: "func main() {}\n",
	}}
	env.eng.ctxProvider = provider

	codeJob := env.submitCode(t)
	env.eng.Run(context.Background(), codeJob)
	if provider.calls == 0 {
		t.Fatal("code job did not request the working set")
	}
	found := false
	for _, p := range env.ollama.promptTexts() {
		if strings.Contains(p, "func main() {}") {
			found = true
			break
		}
	}
	if !found {
		t.Error("code prompt did not include the working-set chunk")
	}

	provider.calls = 0
	textJob := env.submit(t)
	env.eng.Run(context.Background(), textJob)
	if provider.calls != 0 {
		t.Errorf("text job requested the working set %d times; auto is code-only", provider.calls)
	}
}
