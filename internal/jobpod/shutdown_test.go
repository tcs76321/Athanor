package jobpod

import (
	"context"
	"errors"
	"os"
	"testing"
)

// TestShutdown_SealsAndStartRefused: once Shutdown runs, Start returns
// ErrSealed before validating or touching podman, so a racing ensure cannot
// create a pod that outlives the process (M9 soak hardening).
func TestShutdown_SealsAndStartRefused(t *testing.T) {
	client := &fakeClient{}
	m := New(client, &stubFreezer{}, "")
	if _, err := m.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if _, err := m.Start(context.Background(), validSpec()); !errors.Is(err, ErrSealed) {
		t.Fatalf("Start after Shutdown err = %v, want ErrSealed", err)
	}
	// No podman `run` should have been attempted after the seal.
	for _, c := range client.Calls() {
		if len(c) > 0 && c[0] == "run" {
			t.Fatalf("Start after seal invoked podman: %v", c)
		}
	}
}

// TestShutdown_RemovesAllManagedContainers: Shutdown removes every
// athanor-job-* container — even one the manager does not track (the orphan
// the M9 soak left) — and clears the in-memory map.
func TestShutdown_RemovesAllManagedContainers(t *testing.T) {
	client := &fakeClient{}
	orphan := podName("11111111-1111-4111-8111-111111111111")
	client.responder = func(args []string) ([]byte, []byte, error) {
		if len(args) > 0 && args[0] == "ps" {
			return []byte(orphan + "\n" + podName(goodID) + "\n"), nil, nil
		}
		return nil, nil, nil
	}
	m := New(client, &stubFreezer{}, "")
	if _, err := m.Start(context.Background(), validSpec()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	res, err := m.Shutdown(context.Background())
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if res.Removed != 2 {
		t.Errorf("Removed = %d, want 2 (the tracked pod and the orphan)", res.Removed)
	}
	if _, err := m.Get(goodID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Shutdown err = %v, want ErrNotFound", err)
	}
	removed := map[string]bool{}
	for _, c := range client.Calls() {
		if len(c) == 3 && c[0] == "rm" && c[1] == "-f" {
			removed[c[2]] = true
		}
	}
	if !removed[podName(goodID)] || !removed[orphan] {
		t.Errorf("removed = %v, want both %s and %s", removed, podName(goodID), orphan)
	}
}

// TestShutdown_Idempotent: a second Shutdown is a clean no-op.
func TestShutdown_Idempotent(t *testing.T) {
	client := &fakeClient{}
	m := New(client, &stubFreezer{}, "")
	if _, err := m.Shutdown(context.Background()); err != nil {
		t.Fatalf("first Shutdown: %v", err)
	}
	if res, err := m.Shutdown(context.Background()); err != nil || res.Removed != 0 {
		t.Fatalf("second Shutdown = (%+v, %v), want ({}, nil)", res, err)
	}
}

// TestShutdown_RemovesTokenDir: the per-job token dir does not outlive the
// process.
func TestShutdown_RemovesTokenDir(t *testing.T) {
	client := &fakeClient{}
	base := t.TempDir()
	m := New(client, &stubFreezer{}, base)
	if _, err := m.Start(context.Background(), validSpec()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if entries, err := os.ReadDir(base); err != nil || len(entries) == 0 {
		t.Fatalf("expected a token dir under %s before Shutdown (entries=%d, err=%v)", base, len(entries), err)
	}
	if _, err := m.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if entries, err := os.ReadDir(base); err != nil {
		t.Fatalf("reading token base after Shutdown: %v", err)
	} else if len(entries) != 0 {
		t.Errorf("token base still has %d entries after Shutdown", len(entries))
	}
}
