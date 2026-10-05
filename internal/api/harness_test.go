package api

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/control"
	"github.com/tcs76321/athanor/internal/engine"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/power"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/server"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

// harness wires the full M1 stack against a fake Ollama server.
type harness struct {
	ts        *httptest.Server
	st        *store.Store
	jobs      *job.Repository
	artifacts *artifact.Store
	projects  *project.Repo
	freezer   *control.KillSwitch
	eng       *engine.Engine
	api       *API
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	ollama := fakeOllama(t)
	cfg.Inference.OllamaURL = ollama.URL

	freezer, err := control.NewKillSwitch(st)
	if err != nil {
		t.Fatal(err)
	}
	projects := project.NewRepo(st)
	jobs := job.NewRepository(st)
	artifacts := artifact.NewStore(st, filepath.Join(dir, "artifacts"))
	registry, err := llm.NewRegistry(cfg.Personas)
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.New(cfg, st, jobs, projects, artifacts,
		evaluation.NewRepo(st),
		llm.NewClient(cfg.Inference.OllamaURL, nil), registry, freezer,
		power.NewPowerManager(nil),
		// M2-T4: the api harness exercises the M1 walking
		// skeleton only. A nil runner makes the code-archetype
		// sub-steps short-circuit to "skipped" with no HTTP
		// call; text/document/data/media archetypes skip
		// them entirely.
		nil,
		// M5-T4: nil eviction seam, and M5-T5: nil context
		// provider. The harness prompts are far below every
		// persona's window and carry no MCE working set, so
		// neither the §10.4 triggers nor tiers 3/6 are
		// exercised here (they have their own suites).
		nil,
		nil,
		nil)

	srv := server.New("test")
	srv.SetControl(freezer)
	apiServer := New(projects, jobs, artifacts, eng, freezer, st)
	apiServer.Register(srv.Mux())
	// Disable the ADR-0011 Host-header allowlist for
	// tests that are not about the middleware. The
	// httptest server binds a random port, which is
	// not in the default 7420 allowlist.
	if err := srv.SetHostAllowlist(nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return &harness{ts: ts, st: st, jobs: jobs, artifacts: artifacts,
		projects: projects, freezer: freezer, eng: eng, api: apiServer}
}

// waitTerminal polls a job until it leaves the active states.
func (h *harness) waitTerminal(t *testing.T, jobID string) job.Job {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		j, err := h.jobs.Get(context.Background(), jobID)
		if err != nil {
			t.Fatal(err)
		}
		if j.State.Terminal() || j.State == job.StatePaused {
			return j
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s never reached a terminal state", jobID)
	return job.Job{}
}
