package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/onboard"
)

func TestLadderChatReadsContent(t *testing.T) {
	var got map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"message":{"content":"391"},"done":true}`))
	}))
	defer ts.Close()

	answer, err := ladderChat(context.Background(), ts.URL, "m", "prompt", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if answer != "391" {
		t.Errorf("answer = %q, want 391", answer)
	}
	if got["think"] != false {
		t.Errorf("think = %v, want false", got["think"])
	}
	if got["stream"] != false {
		t.Errorf("stream = %v, want false", got["stream"])
	}
}

func TestLadderStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qualification.json")
	s := onboard.NewLadderState()
	s.Record(onboard.CaseResult{Role: "security", Model: "m", CaseID: "c", Correct: true})
	if err := saveLadderState(path, s); err != nil {
		t.Fatal(err)
	}
	got, err := loadLadderState(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Done("security", "m", "c") {
		t.Error("recorded case was not reloaded")
	}
}

func TestLadderCandidatesBoundsAndKeepsConfigured(t *testing.T) {
	models := []onboard.Model{
		{Name: "small", SizeBytes: 1},
		{Name: "large", SizeBytes: 100},
		{Name: "medium", SizeBytes: 50},
	}
	got := ladderCandidates(onboard.Role{Role: "main", Model: "configured"}, models, 2)
	if len(got) != 2 {
		t.Fatalf("candidates = %v, want 2", got)
	}
	if got[0] != "configured" {
		t.Errorf("candidates[0] = %q, want the configured model first", got[0])
	}
	if got[1] != "large" {
		t.Errorf("candidates[1] = %q, want the largest fitting model", got[1])
	}
}
