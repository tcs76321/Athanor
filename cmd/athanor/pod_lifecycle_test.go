package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/jobpod"
	"github.com/tcs76321/athanor/internal/project"
)

type fakePodManager struct {
	specs []jobpod.Spec
	stops []string
}

func (f *fakePodManager) Start(_ context.Context, spec jobpod.Spec) (*jobpod.Pod, error) {
	f.specs = append(f.specs, spec)
	return &jobpod.Pod{ID: spec.ID, State: jobpod.StateRunning}, nil
}
func (f *fakePodManager) Stop(_ context.Context, id string) error {
	f.stops = append(f.stops, id)
	return nil
}
func (f *fakePodManager) Get(_ string) (*jobpod.Pod, error) {
	return nil, jobpod.ErrNotFound
}

type fakeProjectGetter struct {
	project project.Project
	err     error
}

func (f fakeProjectGetter) Get(_ context.Context, _ string) (project.Project, error) {
	return f.project, f.err
}

type fakeSnapshotter struct {
	calls []string
	dest  string
	err   error
}

func (f *fakeSnapshotter) Snapshot(_ context.Context, repoPath, dest string) error {
	f.calls = append(f.calls, repoPath)
	f.dest = dest
	if f.err != nil {
		return f.err
	}
	return os.MkdirAll(dest, 0o700)
}

func testLifecycleConfig() *config.Config {
	cfg, _ := config.Default()
	cfg.JobPod.Image = "athanor-jobpod:latest"
	return cfg
}

func TestPodLifecycleStagesRepoAndCleansUp(t *testing.T) {
	state := t.TempDir()
	pods := &fakePodManager{}
	snap := &fakeSnapshotter{}
	adapter := newPodLifecycle(pods, testLifecycleConfig(),
		fakeProjectGetter{project: project.Project{RepositoryPath: "/tmp/repo"}},
		state, "/host/scratch", snap)

	if err := adapter.Ensure(context.Background(), "job-1", "proj-1"); err != nil {
		t.Fatal(err)
	}
	if len(snap.calls) != 1 || snap.calls[0] != "/tmp/repo" {
		t.Fatalf("snapshot calls = %v, want one for /tmp/repo", snap.calls)
	}
	wantDest := filepath.Join(state, "scratch", "job-1", "repo")
	if snap.dest != wantDest {
		t.Errorf("snapshot dest = %q, want %q", snap.dest, wantDest)
	}
	if len(pods.specs) != 1 || pods.specs[0].RepoDir != wantDest {
		t.Fatalf("pod spec = %+v, want RepoDir %q", pods.specs, wantDest)
	}
	wantMount := filepath.Join("/host/scratch", "job-1", "repo")
	if pods.specs[0].RepoMountDir != wantMount {
		t.Errorf("repo mount = %q, want host-visible %q", pods.specs[0].RepoMountDir, wantMount)
	}
	if err := adapter.Stop(context.Background(), "job-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(wantDest)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("scratch dir still present after Stop: %v", err)
	}
}

func TestPodLifecycleNoRepoSkipsSnapshot(t *testing.T) {
	pods := &fakePodManager{}
	snap := &fakeSnapshotter{}
	adapter := newPodLifecycle(pods, testLifecycleConfig(),
		fakeProjectGetter{project: project.Project{}}, t.TempDir(), "", snap)

	if err := adapter.Ensure(context.Background(), "job-2", "proj-2"); err != nil {
		t.Fatal(err)
	}
	if len(snap.calls) != 0 {
		t.Errorf("snapshot calls = %v, want none for a project without a repository", snap.calls)
	}
	if len(pods.specs) != 1 || pods.specs[0].RepoDir != "" {
		t.Errorf("pod spec = %+v, want an empty RepoDir", pods.specs)
	}
}

func TestPodLifecycleSnapshotFailureStillStartsPod(t *testing.T) {
	state := t.TempDir()
	pods := &fakePodManager{}
	snap := &fakeSnapshotter{err: errors.New("too big")}
	adapter := newPodLifecycle(pods, testLifecycleConfig(),
		fakeProjectGetter{project: project.Project{RepositoryPath: "/tmp/big"}},
		state, "", snap)

	if err := adapter.Ensure(context.Background(), "job-3", "proj-3"); err != nil {
		t.Fatal(err)
	}
	if len(pods.specs) != 1 || pods.specs[0].RepoDir != "" {
		t.Errorf("pod spec = %+v, want an empty RepoDir after a snapshot failure", pods.specs)
	}
	if _, err := os.Stat(filepath.Join(state, "scratch", "job-3")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("partial snapshot dir left behind: %v", err)
	}
}

func TestClearScratchRemovesOrphans(t *testing.T) {
	state := t.TempDir()
	orphan := filepath.Join(state, "scratch", "old-job", "repo", "file.py")
	if err := os.MkdirAll(filepath.Dir(orphan), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orphan, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ClearScratch(state); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(state, "scratch")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("scratch still present after ClearScratch: %v", err)
	}
	if err := ClearScratch(""); err != nil {
		t.Errorf("ClearScratch(empty) = %v, want nil", err)
	}
}
