package jobpod

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// containerNamePrefix is the convention every athanor-managed
// container follows. Sweep uses it as a podman --filter to avoid
// touching unrelated containers on the host.
const containerNamePrefix = "athanor-job-"

// podName is the podman container name for a job id. It MUST match the
// `--name` buildArgs sets at Start and the prefix Sweep filters on. Exec,
// Stop, and supervise all address the container by this name via podName —
// addressing it by the bare id instead is the regression that made exec fail
// with exit 125 ("no such container") and left orphan pods behind.
func podName(id string) string { return containerNamePrefix + id }

// Sweep force-removes any athanor-job-* container the manager did
// not start itself. Called once at daemon boot to clean up after a
// crash, kill -9, or other unexpected exit. Idempotent on a clean
// system: a `podman ps` that returns nothing yields
// SweepResult{0, 0, 0}.
//
// M2-T2 ships the surface. M2-T5 expands the integration test to
// actually do kill -9 cycles and assert zero surviving pods.
func (m *manager) Sweep(ctx context.Context) (SweepResult, error) {
	stdout, stderr, err := m.client.Run(ctx,
		"ps", "-a",
		"--filter", "name="+containerNamePrefix,
		"--format", "{{.Names}}",
	)
	if err != nil {
		// podman not running, machine not started, etc. Not fatal:
		// a sweep is opportunistic.
		slog.Warn("jobpod: sweep ps failed", "err", err, "stderr", string(stderr))
		return SweepResult{}, fmt.Errorf("podman ps: %w", err)
	}

	// Parse the names out of podman's output. Each non-empty line
	// is a container name. We tolerate blank lines and trailing
	// whitespace.
	var names []string
	for _, line := range strings.Split(string(stdout), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		if !strings.HasPrefix(name, containerNamePrefix) {
			// Defensive: podman's --filter is supposed to scope
			// the output, but if it ever changes semantics we
			// should not touch foreign containers.
			continue
		}
		names = append(names, name)
	}

	result := SweepResult{Inspected: len(names)}

	// Build the set of in-memory IDs for O(1) lookup. Anything not
	// in this set is an orphan and gets force-removed.
	m.mu.RLock()
	known := make(map[string]bool, len(m.pods))
	for id := range m.pods {
		known[id] = true
	}
	m.mu.RUnlock()

	for _, name := range names {
		// podman may report the short ID or the full name; we
		// asked for {{.Names}}, so the prefix should be present.
		// Strip the prefix and treat the remainder as the ID.
		id := strings.TrimPrefix(name, containerNamePrefix)
		if known[id] {
			result.Kept++
			continue
		}
		if _, _, err := m.client.Run(ctx, "rm", "-f", name); err != nil {
			// Best-effort: a failed remove is logged but does
			// not abort the sweep.
			slog.Warn("jobpod: sweep rm failed", "name", name, "err", err)
			continue
		}
		result.Removed++
	}
	return result, nil
}
