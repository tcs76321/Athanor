package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestProjectExecutionPatch pins the multi-step execution override: a memory
// sequence changes the test command between jobs and the engine must read the
// new value at the next job's start.
func TestProjectExecutionPatch(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.ts.Close)
	pid, _ := h.createProject(t)

	body := `{
		"test_command": "go test ./...",
		"checks": {"required_sections": ["Overview"], "max_words": 120}
	}`
	req, err := http.NewRequest(http.MethodPatch,
		h.ts.URL+"/projects/"+pid+"/execution", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH execution status = %d, want 200", resp.StatusCode)
	}

	p, err := h.projects.Get(context.Background(), pid)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.TestCommand(); got != "go test ./..." {
		t.Errorf("TestCommand() = %q, want go test ./...", got)
	}
	if p.Execution.Checks == nil ||
		len(p.Execution.Checks.RequiredSections) != 1 ||
		p.Execution.Checks.MaxWords != 120 {
		t.Errorf("execution checks did not round-trip: %+v", p.Execution.Checks)
	}
}

func TestProjectExecutionPatchUnknownProject(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.ts.Close)
	req, err := http.NewRequest(http.MethodPatch,
		h.ts.URL+"/projects/ghost/execution",
		strings.NewReader(`{"test_command": "true"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown project status = %d, want 404", resp.StatusCode)
	}
}
