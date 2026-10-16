package jobpod

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// Shutdown seals the manager against new starts and force-removes every
// athanor-managed container, regardless of the in-memory map. It is the
// graceful-exit counterpart to Sweep (M2-T5): Sweep runs at boot and keeps
// the pods the manager knows about; Shutdown runs at exit and removes them
// all.
//
// It closes a race found in the M9 soak. serve.go calls engine.StopPods,
// which stops the pods of the active jobs it can list, but an in-flight
// phase could start a *new* pod after that enumeration and it survived to
// the next boot. Sealing makes Start fail with ErrSealed from here on, so a
// racing ensure cannot create a pod that outlives the process; the
// unconditional remove then catches any pod created before the seal took
// effect.
//
// Idempotent: a second call finds nothing to remove. A podman failure while
// listing is returned; a failure while removing one container is logged and
// the rest are still attempted.
func (m *manager) Shutdown(ctx context.Context) (SweepResult, error) {
	// Seal first: no new Start may reserve an ID or invoke podman.
	m.sealed.Store(true)

	// Drain the in-memory map under the lock, cancelling each supervisor
	// and removing each token dir. Clearing the map makes a concurrent
	// Get/TokenFor return ErrNotFound — the process is exiting, so nothing
	// should resolve a token.
	m.mu.Lock()
	entries := make([]*podEntry, 0, len(m.pods))
	for _, e := range m.pods {
		entries = append(entries, e)
	}
	m.pods = map[string]*podEntry{}
	m.mu.Unlock()
	for _, e := range entries {
		if e.cancel != nil {
			e.cancel()
		}
		_ = RemoveTokenDir(e.tokenDir)
	}

	// Remove every athanor-managed container. Unlike Sweep there is no
	// "known" set to keep: at shutdown every one of our containers must go.
	stdout, stderr, err := m.client.Run(ctx,
		"ps", "-a",
		"--filter", "name="+containerNamePrefix,
		"--format", "{{.Names}}",
	)
	if err != nil {
		slog.Warn("jobpod: shutdown ps failed", "err", err, "stderr", string(stderr))
		return SweepResult{}, fmt.Errorf("podman ps: %w", err)
	}

	result := SweepResult{}
	for _, line := range strings.Split(string(stdout), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || !strings.HasPrefix(name, containerNamePrefix) {
			// Tolerate blank lines and, defensively, foreign containers
			// podman's --filter should already have excluded.
			continue
		}
		result.Inspected++
		if _, _, err := m.client.Run(ctx, "rm", "-f", name); err != nil {
			slog.Warn("jobpod: shutdown rm failed", "name", name, "err", err)
			continue
		}
		result.Removed++
	}
	return result, nil
}
