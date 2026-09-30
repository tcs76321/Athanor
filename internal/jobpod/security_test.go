package jobpod

// M2-T6 behavioral security probes (ROADMAP §6 M2-T6, ADR-0010).
//
// This file runs a real rootless podman pod with the §21.2
// hardening flags and asserts that the pod cannot reach the
// network, the podman socket, the host filesystem, or any
// credentials. The five probes are the behavioral complement to
// TestGateG2JobPodArgvCannotEscape in internal/gate/, which is
// the structural CI backstop.
//
// The file is gated by the ATHANOR_RUN_INTEGRATION env var.
// Without it the file's tests are no-ops (t.Skip) and `make
// test-race` is green. With it the file requires a working
// `podman` on PATH and an `alpine:3.20` image (the spike's
// image). Developers run the suite with `make test-integration`.
//
// These probes run in CI's non-blocking `integration` job
// (.github/workflows/ci.yml), which installs podman, pulls the
// images via `make integration-images`, and runs
// `make test-integration` on Ubuntu. They stay outside the fast
// `make check` gate (lint + vet + test-race).
//
// The test file uses os/exec and the production store package.
// Gate G1's allowlist explicitly excludes _test.go files from
// the production-source walk (internal/gate/gate_test.go line
// 118), so os/exec is permitted here.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

// skipUnlessIntegration checks the gate env var and the host's
// podman availability. If either is missing the test is skipped
// with a message that points the developer at the right make
// target.
func skipUnlessIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("ATHANOR_RUN_INTEGRATION") == "" {
		t.Skip("ATHANOR_RUN_INTEGRATION not set; run `make test-integration` to exercise this probe")
	}
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not on PATH; install podman or run on a host with podman-machine active")
	}
}

// probeStore opens a temporary SQLite store, applies migrations,
// and registers a cleanup. Each probe calls AppendEvent on the
// returned store so the audit log records every probe attempt
// (pass or fail). The store is in a real temp file but the
// path is cleaned up automatically by t.TempDir.
func probeStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "probe.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, t.TempDir()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

// oneShotArgs returns the argv that buildArgs would produce for
// spec, with --detach stripped. The production manager uses
// --detach because it supervises the pod via `podman inspect`.
// The integration probes want a one-shot, foreground run so the
// exit code and output of the probe command are directly
// observable. The remaining flags are byte-identical to
// production, so a refactor that changes the hardening flags
// in args_common.go, args_linux.go, or args_darwin.go is
// reflected in the probes automatically.
func oneShotArgs(spec Spec) []string {
	args := buildArgs(spec)
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--detach" {
			continue
		}
		out = append(out, a)
	}
	return out
}

// probeResult captures the outcome of one `podman run` of a
// probe script. The audit log records every field, so an
// operator inspecting the events table can replay what the
// probe saw.
type probeResult struct {
	name     string
	script   string
	args     []string
	exitCode int
	stdout   string
	stderr   string
	err      error // exec-level error (timeout, spawn failure)
	elapsed  time.Duration
}

// runProbe executes one probe: it builds the one-shot argv from
// spec, runs `podman <argv>`, and returns the result. The probe
// is given a hard 15s timeout so a misbehaving pod cannot hang
// the suite. The audit event is appended *before* the test
// makes its assertion, so a failing probe still produces a
// security event for the human reader.
//
// The spec is mutated in place only by reference; the caller's
// copy is unaffected.
func runProbe(t *testing.T, s *store.Store, name, script string, spec Spec) probeResult {
	t.Helper()
	spec.Image = "alpine:3.20"
	spec.Command = []string{"sh", "-c", script}
	spec.TokenDir = "" // no token mount for the probe
	spec.Env = nil     // no env vars; the probe asserts on absence

	args := oneShotArgs(spec)
	res := probeResult{
		name:   name,
		script: script,
		args:   args,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, "podman", args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	res.err = cmd.Run()
	res.elapsed = time.Since(start)
	res.stdout = stdout.String()
	res.stderr = stderr.String()
	if ctx.Err() == context.DeadlineExceeded {
		res.err = ctx.Err()
	}
	if cmd.ProcessState != nil {
		res.exitCode = cmd.ProcessState.ExitCode()
	}

	// Audit the probe regardless of outcome. ADR-0010 D4:
	// "failures logged as security events." A failed probe
	// still produces a row; the operator can see the
	// structured `result: fail` field in the events table.
	level := store.EventInfo
	result := "pass"
	if res.exitCode != 0 || res.err != nil {
		level = store.EventWarn
		result = "fail"
	}
	data := map[string]any{
		"probe":      name,
		"script":     script,
		"exit_code":  res.exitCode,
		"result":     result,
		"elapsed_ms": res.elapsed.Milliseconds(),
	}
	if res.err != nil {
		data["error"] = res.err.Error()
	}
	if _, err := s.AppendEvent(context.Background(), store.Event{
		Category: "podman",
		Level:    level,
		Data:     data,
	}); err != nil {
		// Audit failure is not a test failure: the probe
		// itself is the artifact. Log and continue.
		t.Logf("probe %s: audit event append failed: %v", name, err)
	}

	return res
}

// reportProbe formats a probe result for t.Logf. Verbose
// enough to be useful in `make test-integration` output but
// short enough not to flood the terminal.
func reportProbe(r probeResult) string {
	var b strings.Builder
	b.WriteString(r.name)
	b.WriteString(" exit=")
	b.WriteString(itoa(r.exitCode))
	b.WriteString(" elapsed=")
	b.WriteString(r.elapsed.Truncate(time.Millisecond).String())
	if r.err != nil {
		b.WriteString(" err=")
		b.WriteString(r.err.Error())
	}
	if r.stdout != "" {
		b.WriteString(" stdout=")
		b.WriteString(truncate(r.stdout, 200))
	}
	if r.stderr != "" {
		b.WriteString(" stderr=")
		b.WriteString(truncate(r.stderr, 200))
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

// TestSecurity_PodCannotReachInternet brings up a one-shot pod
// and asks it to fetch http://1.1.1.1/ via wget. With
// --network=none the connection must fail. The script always
// exits 1 (its whole purpose is to assert denial); the
// "wget_exit=$?" marker is captured in stdout so the test can
// confirm wget specifically failed.
func TestSecurity_PodCannotReachInternet(t *testing.T) {
	skipUnlessIntegration(t)
	s := probeStore(t)
	res := runProbe(t, s, "internet",
		"wget -q -T 2 -O - http://1.1.1.1/ >/dev/null 2>&1; echo wget_exit=$?; exit 1",
		Spec{ID: goodID})
	t.Log(reportProbe(res))
	// res.err is expected to be non-nil because the script
	// exits 1 (Go's exec surfaces non-zero exit codes as
	// errors). We treat that as the success signal; a real
	// podman failure (binary missing, container start
	// refused) is distinguishable by exitCode == -1 or by an
	// empty stdout with the expected markers absent.
	if !strings.Contains(res.stdout, "wget_exit=") {
		t.Fatalf("probe did not run; stdout: %q stderr: %q", res.stdout, res.stderr)
	}
	if strings.Contains(res.stdout, "wget_exit=0") {
		t.Fatalf("wget succeeded inside the pod: %s", res.stdout)
	}
}

// TestSecurity_PodCannotReachOllama brings up a one-shot pod
// and asks it to reach Ollama. Two host targets are tried:
// host.containers.internal (macOS/podman-machine host alias)
// and 10.0.2.2 (slirp4netns default gateway on Linux rootless).
// Both must fail. The script always exits 1; the test reads
// the "mac=N lin=M" markers and asserts both are non-zero.
func TestSecurity_PodCannotReachOllama(t *testing.T) {
	skipUnlessIntegration(t)
	s := probeStore(t)
	script := `wget -q -T 2 -O - http://host.containers.internal:11434/api/tags >/dev/null 2>&1; echo mac=$?; ` +
		`wget -q -T 2 -O - http://10.0.2.2:11434/api/tags >/dev/null 2>&1; echo lin=$?; ` +
		`exit 1`
	res := runProbe(t, s, "ollama", script, Spec{ID: goodID})
	t.Log(reportProbe(res))
	if !strings.Contains(res.stdout, "mac=") || !strings.Contains(res.stdout, "lin=") {
		t.Fatalf("probe did not run; stdout: %q stderr: %q", res.stdout, res.stderr)
	}
	if strings.Contains(res.stdout, "mac=0") || strings.Contains(res.stdout, "lin=0") {
		t.Fatalf("Ollama reachable from inside the pod: %s", res.stdout)
	}
}

// TestSecurity_PodCannotReachPodmanSocket asserts the podman
// socket is not mounted at the standard path. With
// --network=none and no /var/run bind mount, the file must not
// exist. The script reports a "sock_seen=NO" marker on success
// (the file is absent) and always exits 1.
func TestSecurity_PodCannotReachPodmanSocket(t *testing.T) {
	skipUnlessIntegration(t)
	s := probeStore(t)
	res := runProbe(t, s, "podman_sock",
		`if [ -e /var/run/podman/podman.sock ]; then echo sock_seen=YES; else echo sock_seen=NO; fi; exit 1`,
		Spec{ID: goodID})
	t.Log(reportProbe(res))
	if !strings.Contains(res.stdout, "sock_seen=NO") {
		t.Fatalf("podman.sock appears to be visible inside the pod: stdout=%q stderr=%q",
			res.stdout, res.stderr)
	}
}

// TestSecurity_PodCannotReadHostFS asserts the pod's root
// filesystem is its own (not the host's). Two checks:
//  1. Read-only rootfs: a write under /usr must fail with
//     "Read-only file system".
//  2. The pod's root is an overlay or rootfs mount, not the
//     host's /. The check is "the source of the / mount is
//     not /dev/... on the host's main device" — practical
//     assertion: `mount | grep ' on / '` output contains
//     "overlay" or "rootfs", not "/dev/disk" or "/dev/sd".
//
// The acceptance criterion "pod→host FS denied" is satisfied
// by the combination of (1) write-deny at the rootfs level
// and (2) the pod's root being a container-private mount.
// The structural argv test (commit 1) further asserts no
// bind mount exposes /etc, /home, /root, or the host state.
func TestSecurity_PodCannotReadHostFS(t *testing.T) {
	skipUnlessIntegration(t)
	s := probeStore(t)
	script := `touch /usr/share/probe 2>/tmp/touch_err; echo touch_exit=$?; ` +
		`cat /tmp/touch_err; echo; ` +
		`mount | grep ' on / ' | head -1 > /tmp/mount_root; echo mount_grep=$?; ` +
		`cat /tmp/mount_root; echo; ` +
		`exit 1`
	res := runProbe(t, s, "host_fs", script, Spec{ID: goodID})
	t.Log(reportProbe(res))
	if !strings.Contains(res.stdout, "touch_exit=") {
		t.Fatalf("probe did not run; stdout: %q", res.stdout)
	}
	// 1. Read-only rootfs: touch_exit must be non-zero.
	idx := strings.Index(res.stdout, "touch_exit=")
	after := res.stdout[idx+len("touch_exit="):]
	end := len(after)
	if nl := strings.Index(after, "\n"); nl >= 0 {
		end = nl
	}
	touchVal := strings.TrimSpace(after[:end])
	if touchVal == "0" {
		t.Fatalf("touch /usr/share/probe succeeded (rootfs not read-only): %s", res.stdout)
	}
	// 2. The mount-on-/ line should contain "overlay" or
	//    "rootfs", proving the pod has its own rootfs.
	//    A host-bind would show /dev/sd* or /dev/disk*.
	if !strings.Contains(res.stdout, "overlay") && !strings.Contains(res.stdout, "rootfs") {
		t.Fatalf("pod's / is not an overlay/rootfs mount — possible host FS exposure: %s", res.stdout)
	}
}

// TestSecurity_PodHasNoCredentials asserts the pod's
// environment is empty of common secret patterns. The probe
// greps the env output for token|key|secret|ollama|openai|anthropic;
// a hit means a secret leaked. The script always exits 1
// and the test asserts the grep marker is "no_match" (the
// success signal) and that /run/athanor is not present (the
// manager only mounts it when a token is set; the probe
// explicitly clears TokenDir).
func TestSecurity_PodHasNoCredentials(t *testing.T) {
	skipUnlessIntegration(t)
	s := probeStore(t)
	script := `env | grep -Ei 'token|key|secret|ollama|openai|anthropic' >/dev/null 2>&1; ` +
		`if [ $? -eq 0 ]; then echo grep_env=hit; else echo grep_env=no_match; fi; ` +
		`if [ -e /run/athanor ]; then echo athanor_mount=present; else echo athanor_mount=absent; fi; ` +
		`exit 1`
	res := runProbe(t, s, "credentials", script, Spec{ID: goodID})
	t.Log(reportProbe(res))
	if !strings.Contains(res.stdout, "grep_env=no_match") {
		t.Fatalf("pod env contained a secret pattern: %s", res.stdout)
	}
	// /run/athanor should be absent because the probe sets
	// TokenDir="" (no token mount). A present mount would
	// indicate the manager is leaking the token dir into
	// every pod, not just the authenticated one.
	if !strings.Contains(res.stdout, "athanor_mount=absent") {
		t.Fatalf("unexpected /run/athanor mount inside the probe pod: %s", res.stdout)
	}
}
