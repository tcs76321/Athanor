package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/mce"
	"github.com/tcs76321/athanor/internal/prompt"
)

// compactionCapture is the subset of Ollama's /api/chat request the adapter
// tests assert on.
type compactionCapture struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Options struct {
		Temperature float64 `json:"temperature"`
		NumCtx      int     `json:"num_ctx"`
	} `json:"options"`
}

func compactionServer(t *testing.T, captured *compactionCapture, reply string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":`+reply+`},"done":true}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestMCECompactorUsesSecurityAtZeroTemperature pins the §10.3 contract: the
// security persona at temperature 0.0, the versioned Core prompt, and an
// untruncated multi-line reply.
func TestMCECompactorUsesSecurityAtZeroTemperature(t *testing.T) {
	var captured compactionCapture
	srv := compactionServer(t, &captured, `"line one\nline two\nline three"`)
	comp, err := newMCECompactor(testRegistry(t), llm.NewClient(srv.URL, nil), config.ContextEngine{})
	if err != nil {
		t.Fatalf("newMCECompactor: %v", err)
	}

	item := mce.MemoryItem{
		Content: []byte("boom: exit 1\n"),
		Profile: mce.Profile{Type: mce.EpistemicLog, State: mce.TemporalEpisodic},
		Ref:     mce.SourceRef{RelPath: "job-1"},
	}
	got, err := comp.Compact(context.Background(), item, mce.CompactionDeterministic)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if got != "line one\nline two\nline three" {
		t.Errorf("output altered or truncated: %q", got)
	}
	if captured.Model != "security-model" {
		t.Errorf("model = %q, want the security persona's model", captured.Model)
	}
	if captured.Options.Temperature != 0.0 {
		t.Errorf("temperature = %v, want 0.0 (§10.3)", captured.Options.Temperature)
	}
	if len(captured.Messages) != 2 || captured.Messages[0].Role != "system" {
		t.Fatalf("messages = %+v, want system+user", captured.Messages)
	}
	if captured.Messages[0].Content != prompt.CompactDeterministicSystemV1 {
		t.Error("deterministic kind did not use the deterministic system prompt")
	}
	if !strings.Contains(captured.Messages[1].Content, "boom: exit 1") {
		t.Error("source content missing from the user message")
	}
}

// TestMCECompactorSemanticKind pins that the semantic kind routes to the
// semantic system prompt.
func TestMCECompactorSemanticKind(t *testing.T) {
	var captured compactionCapture
	srv := compactionServer(t, &captured, `"decision: keep it simple"`)
	comp, err := newMCECompactor(testRegistry(t), llm.NewClient(srv.URL, nil), config.ContextEngine{})
	if err != nil {
		t.Fatal(err)
	}
	item := mce.MemoryItem{
		Content: []byte("we agreed to keep it simple\n"),
		Profile: mce.Profile{Type: mce.EpistemicConversation, State: mce.TemporalArchival},
	}
	if _, err := comp.Compact(context.Background(), item, mce.CompactionSemantic); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if captured.Messages[0].Content != prompt.CompactSemanticSystemV1 {
		t.Error("semantic kind did not use the semantic system prompt")
	}
}

// TestMCECompactorTemplateVersions pins the content-address key half.
func TestMCECompactorTemplateVersions(t *testing.T) {
	comp, err := newMCECompactor(testRegistry(t), llm.NewClient("http://127.0.0.1:1", nil), config.ContextEngine{})
	if err != nil {
		t.Fatal(err)
	}
	if got := comp.TemplateVersion(mce.CompactionDeterministic); got != prompt.CompactDeterministicVersion {
		t.Errorf("deterministic version = %q", got)
	}
	if got := comp.TemplateVersion(mce.CompactionSemantic); got != prompt.CompactSemanticVersion {
		t.Errorf("semantic version = %q", got)
	}
}

// TestNewMCECompactorRejectsNonZeroTemperature pins the adapter-side guard of
// the §10.3 invariant.
func TestNewMCECompactorRejectsNonZeroTemperature(t *testing.T) {
	if _, err := newMCECompactor(testRegistry(t), llm.NewClient("http://127.0.0.1:1", nil),
		config.ContextEngine{CompactionTemperature: 0.5}); err == nil {
		t.Fatal("non-zero compaction temperature was accepted")
	}
}
