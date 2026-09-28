package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/mce/division"
)

func tempPtr(v float64) *float64 { return &v }

// TestMCESummarizerUsesWideAtZeroTemperature pins the ADR-0021 §7 contract:
// the Dormant Index summary is produced by the `wide` persona at temperature
// 0.0, and only the first line of the reply is kept.
func TestMCESummarizerUsesWideAtZeroTemperature(t *testing.T) {
	var captured struct {
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":"declares func A\nignored second line"},"done":true}`)
	}))
	defer srv.Close()

	personas := config.Personas{
		Wide:        config.PersonaConfig{Model: "wide-model", ContextTarget: 4096, Temperature: tempPtr(0.7)},
		Tall:        config.PersonaConfig{Model: "tall-model"},
		Main:        config.PersonaConfig{Model: "main-model"},
		Security:    config.PersonaConfig{Model: "security-model"},
		Alternative: config.PersonaConfig{Model: "alternative-model"},
	}
	reg, err := llm.NewRegistry(personas)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	s := newMCESummarizer(reg, llm.NewClient(srv.URL, nil))

	got, err := s.Summarize(context.Background(), division.Chunk{
		FilePath: "a.go", Lang: "go", LineStart: 3, LineEnd: 7, Content: []byte("func A() {}"),
	})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if got != "declares func A" {
		t.Errorf("summary = %q, want only the first line", got)
	}
	if captured.Model != "wide-model" {
		t.Errorf("model = %q, want the wide persona's model", captured.Model)
	}
	if captured.Options.Temperature != 0.0 {
		t.Errorf("temperature = %v, want 0.0 (ADR-0021 §7)", captured.Options.Temperature)
	}
	if len(captured.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(captured.Messages))
	}
	if captured.Messages[0].Role != "system" {
		t.Errorf("first message role = %q, want system", captured.Messages[0].Role)
	}
}

func TestClampLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"one line", "one line"},
		{"first\nsecond", "first"},
		{"  spaced  \nrest", "spaced"},
		{"emoji 🧪🧪🧪 tail", "emoji 🧪🧪🧪 tail"},
	}
	for _, c := range cases {
		if got := clampLine(c.in, maxSummaryRunes); got != c.want {
			t.Errorf("clampLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// A max smaller than the rune width must not split a rune.
	if got := clampLine("aa🧪bb", 3); got != "aa" {
		t.Errorf("clampLine multi-byte = %q, want \"aa\"", got)
	}
}
