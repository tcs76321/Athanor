package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// fakeIndexer records the arguments the route passes and returns a canned
// summary.
type fakeIndexer struct {
	gotProject, gotPath string
	sum                 IndexSummary
	err                 error
}

func (f *fakeIndexer) IndexProject(_ context.Context, projectID, path string) (IndexSummary, error) {
	f.gotProject, f.gotPath = projectID, path
	return f.sum, f.err
}

// fakeBoundedIndexer also satisfies BoundedIndexRunner, so the project-create
// pre-index path can be exercised without a real MCE/LLM.
type fakeBoundedIndexer struct {
	fakeIndexer
	onceCalls             int
	onceProject, oncePath string
	onceSum               IndexSummary
	onceErr               error
}

func (f *fakeBoundedIndexer) IndexOnceSummary(_ context.Context, projectID, path string) (IndexSummary, error) {
	f.onceCalls++
	f.onceProject, f.oncePath = projectID, path
	return f.onceSum, f.onceErr
}

// postIndex issues an index request and returns the status and raw body.
func postIndex(t *testing.T, url, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

func TestIndexRouteUnavailable(t *testing.T) {
	h := newHarness(t)
	status, _ := postIndex(t, h.ts.URL+"/projects/x/index", "")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when no runner is wired", status)
	}
}

func TestIndexRouteUnknownProject(t *testing.T) {
	h := newHarness(t)
	h.api.SetIndexRunner(&fakeIndexer{})
	status, _ := postIndex(t, h.ts.URL+"/projects/ghost/index", "{}")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an unknown project", status)
	}
}

func TestIndexRouteRequiresRepoOrPath(t *testing.T) {
	h := newHarness(t)
	h.api.SetIndexRunner(&fakeIndexer{})
	p, _, err := h.projects.Create(context.Background(), "idx", "code",
		"index this repository completely", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	status, _ := postIndex(t, h.ts.URL+"/projects/"+p.ID+"/index", "{}")
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409 with no repository_path and no path", status)
	}
}

func TestIndexRouteRunsRunner(t *testing.T) {
	h := newHarness(t)
	f := &fakeIndexer{sum: IndexSummary{Indexed: 4, Skipped: 1, Chunks: 9, Embedded: 9}}
	h.api.SetIndexRunner(f)
	p, _, err := h.projects.Create(context.Background(), "idx", "code",
		"index this repository completely", "/tmp/repo", nil)
	if err != nil {
		t.Fatal(err)
	}

	status, raw := postIndex(t, h.ts.URL+"/projects/"+p.ID+"/index", "{}")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, raw)
	}
	if f.gotProject != p.ID || f.gotPath != "/tmp/repo" {
		t.Fatalf("runner got (%q,%q), want (%q,/tmp/repo)", f.gotProject, f.gotPath, p.ID)
	}
	var got IndexSummary
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Indexed != 4 || got.Embedded != 9 {
		t.Fatalf("summary = %+v, want indexed=4 embedded=9", got)
	}

	// An explicit path overrides the project's repository_path.
	if _, _ = postIndex(t, h.ts.URL+"/projects/"+p.ID+"/index", `{"path":"/other"}`); f.gotPath != "/other" {
		t.Fatalf("override path = %q, want /other", f.gotPath)
	}
}

// TestProjectCreatePreindexesRepository pins the first-use on-ramp: a
// repository-backed project runs one bounded index pass before its first job
// is enqueued, so the job's Dormant Index is not empty.
func TestProjectCreatePreindexesRepository(t *testing.T) {
	h := newHarness(t)
	f := &fakeBoundedIndexer{onceSum: IndexSummary{Indexed: 3, Chunks: 7, Summarized: 3}}
	h.api.SetIndexRunner(f)

	body := []byte(`{
		"name": "preindex", "archetype": "text",
		"goal": "Write a short essay about lossless context.",
		"repository_path": "/tmp/repo"
	}`)
	resp, err := http.Post(h.ts.URL+"/projects", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var out projectResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if f.onceCalls != 1 || f.oncePath != "/tmp/repo" || f.onceProject != out.ID {
		t.Fatalf("pre-index calls=%d project=%q path=%q, want one call for (%q,/tmp/repo)",
			f.onceCalls, f.onceProject, f.oncePath, out.ID)
	}
	if out.Index == nil || out.Index.Indexed != 3 || out.Index.Chunks != 7 {
		t.Fatalf("response index = %+v, want indexed=3 chunks=7", out.Index)
	}
	if out.IndexWarning != "" {
		t.Fatalf("response warning = %q, want empty", out.IndexWarning)
	}
	if out.JobID == "" {
		t.Fatal("no job id in response")
	}
	h.waitTerminal(t, out.JobID)
}

// TestProjectCreateSkipsPreindexWhenStartFalse keeps the metadata-only path
// cheap: no repository walk and no job.
func TestProjectCreateSkipsPreindexWhenStartFalse(t *testing.T) {
	h := newHarness(t)
	f := &fakeBoundedIndexer{onceSum: IndexSummary{Indexed: 3}}
	h.api.SetIndexRunner(f)

	body := []byte(`{
		"name": "metadata", "archetype": "text",
		"goal": "Write a short essay about lossless context.",
		"repository_path": "/tmp/repo", "start": false
	}`)
	resp, err := http.Post(h.ts.URL+"/projects", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var out projectResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if f.onceCalls != 0 {
		t.Fatalf("pre-index calls = %d, want 0 with start:false", f.onceCalls)
	}
	if out.JobID != "" {
		t.Fatalf("job id = %q, want empty with start:false", out.JobID)
	}
}
