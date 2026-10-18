package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLoadMemoryScripts(t *testing.T) {
	scripts, err := loadMemoryScripts("../../eval/bench/memory-scripts.yaml")
	if err != nil {
		t.Fatalf("loadMemoryScripts: %v", err)
	}
	if len(scripts) != 4 {
		t.Fatalf("scripts = %d, want 4", len(scripts))
	}
	held := 0
	for _, s := range scripts {
		if s.HeldOut {
			held++
		}
		if len(s.Steps) < 3 {
			t.Errorf("%s: steps = %d, want >= 3", s.ID, len(s.Steps))
		}
	}
	if held != 3 {
		t.Errorf("held-out scripts = %d, want 3", held)
	}
	byID := map[string]memoryScript{}
	for _, s := range scripts {
		byID[s.ID] = s
	}
	if got := byID["mem-correction-error-type"]; got.Scope != "project" || len(got.Steps) != 3 {
		t.Errorf("correction script = %+v", got)
	}
	if got := byID["mem-compaction-fact"]; len(got.Steps) != 3 || got.Steps[1].Kind != "filler" {
		t.Errorf("compaction script = %+v", got)
	}
}

type memoryFake struct {
	mu          sync.Mutex
	projects    int
	goals       int
	corrections int
	insights    int
	patches     int
	lastTest    string
	failJobs    bool
}

func (f *memoryFake) handler() http.Handler {
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("POST /projects", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.projects++
		f.mu.Unlock()
		write(w, map[string]any{"id": "p1", "task_id": "t0"})
	})
	mux.HandleFunc("PATCH /projects/p1/execution", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			TestCommand string `json:"test_command"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.patches++
		f.lastTest = body.TestCommand
		f.mu.Unlock()
		write(w, map[string]any{"id": "p1"})
	})
	mux.HandleFunc("POST /projects/p1/goals", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.goals++
		id := fmt.Sprintf("j%d", f.goals)
		f.mu.Unlock()
		write(w, map[string]any{"job_id": id})
	})
	mux.HandleFunc("GET /jobs/", func(w http.ResponseWriter, r *http.Request) {
		if f.failJobs {
			write(w, map[string]any{"state": "failed"})
			return
		}
		write(w, map[string]any{"state": "completed", "artifact_id": "a1"})
	})
	mux.HandleFunc("POST /projects/p1/corrections", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.corrections++
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		write(w, map[string]any{"id": "c1"})
	})
	mux.HandleFunc("POST /strategy/insights", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.insights++
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		write(w, map[string]any{"id": "i1"})
	})
	return mux
}

type memoryFakeCounts struct {
	projects    int
	goals       int
	corrections int
	insights    int
	patches     int
	lastTest    string
}

func (f *memoryFake) counts() memoryFakeCounts {
	f.mu.Lock()
	defer f.mu.Unlock()
	return memoryFakeCounts{
		projects: f.projects, goals: f.goals, corrections: f.corrections,
		insights: f.insights, patches: f.patches, lastTest: f.lastTest,
	}
}

func runMemoryTest(t *testing.T, only string, failJobs bool) (memoryFakeCounts, string) {
	t.Helper()
	fake := &memoryFake{failJobs: failJobs}
	ts := httptest.NewServer(fake.handler())
	t.Cleanup(ts.Close)
	out := t.TempDir()
	runMemory([]string{
		"-scripts", "../../eval/bench/memory-scripts.yaml",
		"-addr", strings.TrimPrefix(ts.URL, "http://"),
		"-out", out,
		"-only", only,
	})
	report, err := os.ReadFile(filepath.Join(out, "memory-report.md"))
	if err != nil {
		t.Fatal(err)
	}
	return fake.counts(), string(report)
}

func TestMemoryRunnerCorrection(t *testing.T) {
	fake, report := runMemoryTest(t, "mem-correction-error-type", false)
	if fake.projects != 1 || fake.goals != 2 || fake.corrections != 1 || fake.patches != 2 {
		t.Errorf("calls: projects=%d goals=%d corrections=%d patches=%d, want 1/2/1/2",
			fake.projects, fake.goals, fake.corrections, fake.patches)
	}
	if !strings.Contains(report, "| mem-correction-error-type | pass | pass |") {
		t.Errorf("report missing pass row:\n%s", report)
	}
	if !strings.Contains(report, "injection unverified") {
		t.Errorf("report should say injection is unverified without a state dir:\n%s", report)
	}
}

func TestMemoryRunnerInsight(t *testing.T) {
	fake, report := runMemoryTest(t, "mem-insight-input-validation", false)
	if fake.insights != 1 || fake.goals != 2 {
		t.Errorf("calls: insights=%d goals=%d, want 1/2", fake.insights, fake.goals)
	}
	if !strings.Contains(report, "| mem-insight-input-validation | pass | pass |") {
		t.Errorf("report missing insight pass row:\n%s", report)
	}
}

func TestMemoryRunnerCompactionPartial(t *testing.T) {
	// The real day-2 may fail without the filler; the script must still be
	// labeled partial (unsupported), never fail, because it was not run as
	// specified.
	_, report := runMemoryTest(t, "mem-compaction-fact", true)
	if !strings.Contains(report, "| mem-compaction-fact | partial | fail |") {
		t.Errorf("compaction should be partial, not pass/fail:\n%s", report)
	}
	if !strings.Contains(report, "compaction filler") {
		t.Errorf("report should explain the unsupported filler:\n%s", report)
	}
}
