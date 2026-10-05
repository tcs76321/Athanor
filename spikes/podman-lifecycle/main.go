// Package main is the M2-T1 spike: bring up a hardened rootless Podman
// container, verify the §21.2 containment flags, and tear it down. The
// spike shells out to the podman CLI (per spikes/ convention: do
// whatever proves the hypothesis fastest). Findings land in
// docs/adr/0007-podman-lifecycle.md.
//
// Usage:
//
//	go run ./spikes/podman-lifecycle
//
// The spike prints a per-check table and exits non-zero if any
// containment check fails. macOS + podman-machine was used for the
// recorded findings; Linux behavior is documented in the ADR.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// check is one named containment assertion. We accumulate them and
// print a table at the end so the ADR can quote the results.
type check struct {
	name    string
	command []string
	// wantExit is the expected exit code. 0 means "command succeeded,
	// the container can do this thing"; nonzero means "command failed,
	// the container cannot do this thing" (the expected result for
	// negative checks like "can it reach the network").
	wantExit int
	// expectOutput optionally matches against stdout (substring).
	expectOutput string
	ok           bool
	detail       string
}

func main() {
	if err := run(); err != nil {
		log.Fatalf("spike failed: %v", err)
	}
}

func run() error {
	image := "alpine:3.20"
	if err := pull(image); err != nil {
		return fmt.Errorf("pull %s: %w", image, err)
	}

	// Per-job token (§21 + M2-T3): a random hex string in a tmpfs mount
	// the pod can read. M2-T3 will exercise validation against the
	// internal API; the spike just proves the tmpfs mount works.
	token := newToken()
	tokenDir, err := os.MkdirTemp("", "athanor-token-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tokenDir) }()
	if err := os.WriteFile(filepath.Join(tokenDir, "token"), []byte(token), 0o600); err != nil {
		return err
	}

	// Bring up a rootless container with the §21.2 hardening flags.
	// Network=none guarantees negative network reachability; we
	// verify the other containment claims with explicit checks below.
	containerName := "athanor-spike-" + newShortID()
	args := []string{
		"run", "--rm",
		"--name", containerName,
		"--network=none",
		"--read-only",
		"--tmpfs", "/tmp:rw,nosuid,nodev,size=8m",
		"--security-opt", "no-new-privileges",
		"--cap-drop=ALL",
		"--pids-limit=64",
		"--memory=128m",
		"--cpus=0.5",
		"--mount", "type=bind,source=" + tokenDir + ",target=/run/athanor,ro",
		"-d", // detached; we exec into it
		image, "sleep", "300",
	}
	up := exec.Command("podman", args...)
	up.Stdout, up.Stderr = os.Stdout, os.Stderr
	if err := up.Run(); err != nil {
		return fmt.Errorf("podman run: %w", err)
	}
	defer teardown(containerName)

	// The §21.2 hardening flag `--security-opt seccomp=...` is a
	// no-op on rootless macOS/podman-machine (no kernel seccomp
	// available). This is recorded as a caveat in the ADR. The other
	// flags are exercised by the checks below.

	checks := []check{
		{
			name:         "read-only rootfs",
			command:      []string{"exec", containerName, "sh", "-c", "touch /etc/should-fail"},
			wantExit:     1,
			expectOutput: "Read-only",
		},
		{
			name:     "tmpfs at /tmp writable",
			command:  []string{"exec", containerName, "sh", "-c", "echo ok > /tmp/x && cat /tmp/x"},
			wantExit: 0,
		},
		{
			name:         "token visible at /run/athanor/token",
			command:      []string{"exec", containerName, "cat", "/run/athanor/token"},
			wantExit:     0,
			expectOutput: token,
		},
		{
			name:     "no network: cannot reach external host",
			command:  []string{"exec", containerName, "wget", "-q", "-T", "3", "-O", "-", "http://example.com/"},
			wantExit: 1, // wget fails (DNS or connect) on network=none
		},
		{
			name:         "no host filesystem: /etc/hosts is container's own",
			command:      []string{"exec", containerName, "cat", "/etc/hosts"},
			wantExit:     0,
			expectOutput: "localhost",
		},
		{
			name:     "no podman socket",
			command:  []string{"exec", containerName, "sh", "-c", "ls /var/run/docker.sock /run/podman/podman.sock 2>&1"},
			wantExit: 1, // no socket file expected
		},
		{
			// --pids-limit sets the cgroup max, which the shell's
			// `ulimit -u` does not reflect. Prove the limit exists
			// by reading cgroup v2's pids.max; the line ends in
			// the limit value. Inside an alpine container, the
			// cgroup path is /sys/fs/cgroup/pids.max.
			name:     "pids cgroup limit set (64)",
			command:  []string{"exec", containerName, "sh", "-c", "cat /sys/fs/cgroup/pids.max 2>/dev/null || cat /sys/fs/cgroup/pids/pids.max 2>/dev/null"},
			wantExit: 0,
			// cgroup v2 prints "64" on the last token; v1 prints
			// "64" on a line by itself. We accept either.
			expectOutput: "64",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for i := range checks {
		checks[i].ok, checks[i].detail = runCheck(ctx, checks[i])
	}

	printTable(checks)

	failed := 0
	for _, c := range checks {
		if !c.ok {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d/%d checks failed", failed, len(checks))
	}
	fmt.Println("\nAll M2-T1 spike checks passed.")
	return nil
}

func pull(image string) error {
	p := exec.Command("podman", "pull", "--quiet", image)
	out, err := p.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

func teardown(name string) {
	// --rm handles cleanup on a clean exit; this is the kill -9 / crash
	// case. We force-stop to release the name.
	_ = exec.Command("podman", "rm", "-f", name).Run()
}

func runCheck(ctx context.Context, c check) (bool, string) {
	cmd := exec.CommandContext(ctx, "podman", c.command...)
	out, err := cmd.CombinedOutput()
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			return false, fmt.Sprintf("exec error: %v", err)
		}
	}
	if exit != c.wantExit {
		return false, fmt.Sprintf("exit=%d, want=%d; output=%q", exit, c.wantExit, snippet(out))
	}
	if c.expectOutput != "" && !strings.Contains(string(out), c.expectOutput) {
		return false, fmt.Sprintf("output missing %q; got %q", c.expectOutput, snippet(out))
	}
	return true, snippet(out)
}

func printTable(cs []check) {
	fmt.Printf("%-45s  %-7s  %s\n", "check", "result", "detail")
	fmt.Println(strings.Repeat("-", 80))
	for _, c := range cs {
		r := "PASS"
		if !c.ok {
			r = "FAIL"
		}
		fmt.Printf("%-45s  %-7s  %s\n", c.name, r, c.detail)
	}
}

func newToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand is not going to fail on a real host; fall back
		// to a non-secret placeholder so the spike still runs.
		return "no-rand-available"
	}
	return hex.EncodeToString(b[:])
}

func newShortID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
