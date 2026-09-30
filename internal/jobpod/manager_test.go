package jobpod

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeClient records every `podman` invocation and returns canned
// output. Tests use the recording slice to assert the right argv
// was passed at the right moment.
type fakeClient struct {
	mu        sync.Mutex
	calls     [][]string
	stdin     [][]byte
	responder func(args []string) (stdout, stderr []byte, err error)
}

func (f *fakeClient) Run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	return f.record(args, nil)
}

func (f *fakeClient) RunStdin(ctx context.Context, stdin []byte, args ...string) ([]byte, []byte, error) {
	return f.record(args, stdin)
}

// record appends the invocation (with its stdin, if any) and dispatches
// to the responder. Shared by Run and RunStdin so both are observable.
func (f *fakeClient) record(args []string, stdin []byte) ([]byte, []byte, error) {
	f.mu.Lock()
	cp := make([]string, len(args))
	copy(cp, args)
	f.calls = append(f.calls, cp)
	f.stdin = append(f.stdin, append([]byte(nil), stdin...))
	responder := f.responder
	f.mu.Unlock()
	if responder == nil {
		return nil, nil, nil
	}
	return responder(cp)
}

// Stdins returns the stdin bytes passed to each invocation, in order.
func (f *fakeClient) Stdins() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]byte, len(f.stdin))
	for i, s := range f.stdin {
		out[i] = append([]byte(nil), s...)
	}
	return out
}

func (f *fakeClient) Calls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = append([]string(nil), c...)
	}
	return out
}

// stubFreezer satisfies the Freezer interface for tests.
type stubFreezer struct{ frozen bool }

func (s *stubFreezer) Frozen() bool { return s.frozen }

// goodID is a valid v4 UUID for use in tests.
const goodID = "3b241101-e2bb-4255-8caf-4136c566a962"

// validSpec returns a Spec that passes validation.
func validSpec() Spec {
	return Spec{
		ID:      goodID,
		Image:   "alpine:3.20",
		Command: []string{"sleep", "10"},
	}
}

// TestStart_ValidatesSpec asserts every invalid Spec shape returns
// ErrInvalidSpec without making a client call.
func TestStart_ValidatesSpec(t *testing.T) {
	client := &fakeClient{}
	m := New(client, &stubFreezer{}, "")

	cases := []struct {
		name string
		mut  func(*Spec)
	}{
		{"empty id", func(s *Spec) { s.ID = "" }},
		{"non-uuid id", func(s *Spec) { s.ID = "not-a-uuid" }},
		{"v1 uuid", func(s *Spec) { s.ID = "3b241101-e2bb-1255-8caf-4136c566a962" }},
		{"empty image", func(s *Spec) { s.Image = "" }},
		{"empty command", func(s *Spec) { s.Command = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := validSpec()
			tc.mut(&s)
			_, err := m.Start(context.Background(), s)
			if !errors.Is(err, ErrInvalidSpec) {
				t.Errorf("err = %v, want ErrInvalidSpec", err)
			}
			if len(client.Calls()) != 0 {
				t.Errorf("client was called %d times, want 0 (validation must fail fast)", len(client.Calls()))
			}
		})
	}
}

// TestStart_RespectsFreezer asserts a frozen freezer blocks new pods
// without making a client call.
func TestStart_RespectsFreezer(t *testing.T) {
	client := &fakeClient{}
	m := New(client, &stubFreezer{frozen: true}, "")
	_, err := m.Start(context.Background(), validSpec())
	if !errors.Is(err, ErrFrozen) {
		t.Errorf("err = %v, want ErrFrozen", err)
	}
	if len(client.Calls()) != 0 {
		t.Errorf("client called %d times while frozen, want 0", len(client.Calls()))
	}
}

// TestStart_HappyPath asserts the pod is registered with StatePending
// after a successful Start, and the first client call is `podman run`
// with the §21.2 flag block in argv.
func TestStart_HappyPath(t *testing.T) {
	client := &fakeClient{}
	m := New(client, &stubFreezer{}, "")

	pod, err := m.Start(context.Background(), validSpec())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if pod.State != StatePending {
		t.Errorf("pod.State = %s, want pending", pod.State)
	}
	if pod.ID != goodID {
		t.Errorf("pod.ID = %q, want %q", pod.ID, goodID)
	}

	calls := client.Calls()
	if len(calls) < 1 {
		t.Fatal("client was not called")
	}
	first := calls[0]
	if first[0] != "run" {
		t.Errorf("first call[0] = %q, want \"run\"", first[0])
	}
	joined := joinArgs(first)
	for _, want := range []string{"--read-only", "--cap-drop", "ALL", "--network", "none"} {
		if !contains(joined, want) {
			t.Errorf("argv missing %q\ngot: %s", want, joined)
		}
	}

	got, err := m.Get(goodID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != goodID {
		t.Errorf("Get returned %q, want %q", got.ID, goodID)
	}
}

// TestStart_DuplicateID asserts a second Start with the same ID
// returns ErrAlreadyExists.
func TestStart_DuplicateID(t *testing.T) {
	client := &fakeClient{}
	m := New(client, &stubFreezer{}, "")
	if _, err := m.Start(context.Background(), validSpec()); err != nil {
		t.Fatal(err)
	}
	_, err := m.Start(context.Background(), validSpec())
	if !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("err = %v, want ErrAlreadyExists", err)
	}
}

// TestStop_HappyPath asserts Stop calls `podman rm -f <id>` and
// removes the pod from the in-memory map.
func TestStop_HappyPath(t *testing.T) {
	client := &fakeClient{}
	m := New(client, &stubFreezer{}, "")
	if _, err := m.Start(context.Background(), validSpec()); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(context.Background(), goodID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	calls := client.Calls()
	last := calls[len(calls)-1]
	if last[0] != "rm" || last[1] != "-f" || last[2] != goodID {
		t.Errorf("last call = %v, want [rm -f %s]", last, goodID)
	}
	if _, err := m.Get(goodID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Stop err = %v, want ErrNotFound", err)
	}
}

// TestStop_NotFound asserts Stop on an unknown ID returns ErrNotFound
// without calling the client.
func TestStop_NotFound(t *testing.T) {
	client := &fakeClient{}
	m := New(client, &stubFreezer{}, "")
	callsBefore := len(client.Calls())
	if err := m.Stop(context.Background(), goodID); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if len(client.Calls()) != callsBefore {
		t.Errorf("client was called for a non-existent pod")
	}
}

// TestStop_Idempotent asserts the second Stop on a stopped pod is a
// no-op (ErrNotFound because the pod is no longer in the map).
func TestStop_Idempotent(t *testing.T) {
	client := &fakeClient{}
	m := New(client, &stubFreezer{}, "")
	if _, err := m.Start(context.Background(), validSpec()); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(context.Background(), goodID); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	callsBefore := len(client.Calls())
	if err := m.Stop(context.Background(), goodID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Stop err = %v, want ErrNotFound", err)
	}
	if len(client.Calls()) != callsBefore {
		t.Errorf("second Stop called the client; should be a no-op")
	}
}

// TestSupervise_TransitionsToStopped asserts the supervisor drives
// a pod to StateStopped when `podman inspect` reports `exited 0`,
// and (M2-T5) drops the in-memory entry when the transition fires.
// The fake client returns "running" on the first inspect and
// "exited 0" on the second; the supervisor's behavior is observed
// indirectly via the call count and the post-condition (no entry
// in m.pods, Get returns ErrNotFound). Catching the StateStopped
// value in a polling loop is racy by design — the supervisor sets
// state and forgets in the same critical section.
func TestSupervise_TransitionsToStopped(t *testing.T) {
	runCount := 0
	inspectCount := 0
	client := &fakeClient{
		responder: func(args []string) ([]byte, []byte, error) {
			switch args[0] {
			case "run":
				runCount++
				return nil, nil, nil
			case "inspect":
				inspectCount++
				if inspectCount == 1 {
					return []byte("running 0"), nil, nil
				}
				return []byte("exited 0"), nil, nil
			}
			return nil, nil, nil
		},
	}
	m := New(client, &stubFreezer{}, "").(*manager)
	if _, err := m.Start(context.Background(), validSpec()); err != nil {
		t.Fatal(err)
	}
	// Wait for the supervisor to drop the entry. The post-condition
	// is the M2-T5 invariant.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.RLock()
		_, present := m.pods[goodID]
		m.mu.RUnlock()
		if !present {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	m.mu.RLock()
	_, stillPresent := m.pods[goodID]
	m.mu.RUnlock()
	if stillPresent {
		t.Fatalf("in-memory entry for %s was not removed after terminal state", goodID)
	}
	if runCount != 1 {
		t.Errorf("run called %d times, want 1", runCount)
	}
	if inspectCount < 2 {
		t.Errorf("inspect called %d times, want >= 2 (running + exited)", inspectCount)
	}
	// Post-condition: Get returns ErrNotFound.
	if _, err := m.Get(goodID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after terminal err = %v, want ErrNotFound", err)
	}
	// M2-T5: TokenFor also returns ErrNotFound, matching the Stop path.
	if _, err := m.TokenFor(goodID); !errors.Is(err, ErrNotFound) {
		t.Errorf("TokenFor after terminal err = %v, want ErrNotFound", err)
	}
}

// TestSupervise_TransitionsToFailed asserts the supervisor drops
// the in-memory entry when the pod exits non-zero. The supervisor
// sets StateFailed and a non-nil ExitErr before the forget; the
// M2-T5 invariant is observed via the post-condition (no entry in
// m.pods, Get returns ErrNotFound) and the call sequence.
func TestSupervise_TransitionsToFailed(t *testing.T) {
	inspectCount := 0
	client := &fakeClient{
		responder: func(args []string) ([]byte, []byte, error) {
			switch args[0] {
			case "run":
				return nil, nil, nil
			case "inspect":
				inspectCount++
				if inspectCount == 1 {
					return []byte("running 0"), nil, nil
				}
				return []byte("exited 1"), nil, nil
			}
			return nil, nil, nil
		},
	}
	m := New(client, &stubFreezer{}, "").(*manager)
	if _, err := m.Start(context.Background(), validSpec()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.RLock()
		_, present := m.pods[goodID]
		m.mu.RUnlock()
		if !present {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	m.mu.RLock()
	_, stillPresent := m.pods[goodID]
	m.mu.RUnlock()
	if stillPresent {
		t.Errorf("in-memory entry for %s was not removed after failed state", goodID)
	}
	if inspectCount < 2 {
		t.Errorf("inspect called %d times, want >= 2 (running + exited)", inspectCount)
	}
	// Post-condition: Get returns ErrNotFound.
	if _, err := m.Get(goodID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after terminal err = %v, want ErrNotFound", err)
	}
	// M2-T5: TokenFor also returns ErrNotFound, matching the Stop path.
	if _, err := m.TokenFor(goodID); !errors.Is(err, ErrNotFound) {
		t.Errorf("TokenFor after terminal err = %v, want ErrNotFound", err)
	}
}

// TestParseInspect covers the inspect output parser directly so the
// supervisor's behavior is not just tested through the manager.
func TestParseInspect(t *testing.T) {
	cases := []struct {
		in           string
		wantStatus   string
		wantExitCode int
	}{
		{"running 0", "running", 0},
		{"exited 0", "exited", 0},
		{"exited 1", "exited", 1},
		{"stopped 137", "stopped", 137},
		{"  running 0  \n", "running", 0},
		{"unknown", "unknown", 0},
		{"", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			s, c := parseInspect(tc.in)
			if s != tc.wantStatus || c != tc.wantExitCode {
				t.Errorf("parseInspect(%q) = (%q, %d), want (%q, %d)",
					tc.in, s, c, tc.wantStatus, tc.wantExitCode)
			}
		})
	}
}

// contains is a tiny helper for substring assertions, used to keep
// the test file free of an extra strings import.
func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	if len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// TestSweep_RemovesOrphans: a fresh boot finds three stale
// athanor-job-* containers; the manager knows about one of them.
// Sweep removes the two orphans, keeps the known one, and reports
// the right counts.
func TestSweep_RemovesOrphans(t *testing.T) {
	knownID := "11111111-1111-4111-8111-111111111111"
	orphan1 := "athanor-job-22222222-2222-4222-8222-222222222222"
	orphan2 := "athanor-job-33333333-3333-4333-8333-333333333333"
	kept := "athanor-job-" + knownID

	client := &fakeClient{
		responder: func(args []string) ([]byte, []byte, error) {
			switch args[0] {
			case "ps":
				return []byte(orphan1 + "\n" + orphan2 + "\n" + kept + "\n"), nil, nil
			case "rm":
				return nil, nil, nil
			}
			return nil, nil, nil
		},
	}
	m := New(client, &stubFreezer{}, "").(*manager)
	// Seed the in-memory map with the known pod. We do this by
	// inserting directly so we don't have to run Start's argv
	// pipeline (which would also call `podman run`).
	m.pods[knownID] = &podEntry{
		pod:   &Pod{ID: knownID, State: StateRunning},
		stopC: make(chan struct{}),
	}

	res, err := m.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if res.Inspected != 3 {
		t.Errorf("Inspected = %d, want 3", res.Inspected)
	}
	if res.Removed != 2 {
		t.Errorf("Removed = %d, want 2", res.Removed)
	}
	if res.Kept != 1 {
		t.Errorf("Kept = %d, want 1", res.Kept)
	}

	// Inspect the recorded calls: one ps and two rm.
	calls := client.Calls()
	rmCount := 0
	for _, c := range calls {
		if c[0] == "rm" {
			rmCount++
		}
	}
	if rmCount != 2 {
		t.Errorf("rm called %d times, want 2", rmCount)
	}
}

// TestSweep_EmptyWhenNoContainers: a clean host returns
// SweepResult{0, 0, 0} with no rm calls.
func TestSweep_EmptyWhenNoContainers(t *testing.T) {
	client := &fakeClient{
		responder: func(args []string) ([]byte, []byte, error) {
			if args[0] == "ps" {
				return []byte(""), nil, nil
			}
			return nil, nil, nil
		},
	}
	m := New(client, &stubFreezer{}, "").(*manager)
	res, err := m.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if res.Inspected != 0 || res.Removed != 0 || res.Kept != 0 {
		t.Errorf("SweepResult = %+v, want all zeros", res)
	}
}

// TestSweep_IgnoresForeignContainers: defensive guard. If podman's
// --filter ever changes semantics and a non-athanor container shows
// up in the list, Sweep must not touch it.
func TestSweep_IgnoresForeignContainers(t *testing.T) {
	foreign := "some-other-container"
	orphan := "athanor-job-44444444-4444-4444-8444-444444444444"
	client := &fakeClient{
		responder: func(args []string) ([]byte, []byte, error) {
			switch args[0] {
			case "ps":
				return []byte(foreign + "\n" + orphan + "\n"), nil, nil
			case "rm":
				return nil, nil, nil
			}
			return nil, nil, nil
		},
	}
	m := New(client, &stubFreezer{}, "").(*manager)
	res, err := m.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if res.Removed != 1 {
		t.Errorf("Removed = %d, want 1 (only the orphan)", res.Removed)
	}

	// Confirm we never rm'd the foreign container.
	for _, c := range client.Calls() {
		if c[0] == "rm" && contains(joinArgs(c), foreign) {
			t.Errorf("Sweep touched foreign container %q", foreign)
		}
	}
}

// ---------------------------------------------------------------------------
// M2-T3: token-dir lifecycle tests.
// ---------------------------------------------------------------------------

// TestStart_WritesTokenFile asserts that when the manager has a
// tokenBase, Start creates <tokenBase>/<jobID>/token with the token
// the spec will be bound to, and that the spec.Token and spec.TokenDir
// fields are populated before the client.Run call so the bind-mount
// argv includes the right path.
func TestStart_WritesTokenFile(t *testing.T) {
	base := t.TempDir()
	client := &fakeClient{}
	m := New(client, &stubFreezer{}, base)

	spec := validSpec()
	// Force the spec to start with no token — the manager must
	// generate one because tokenBase is set.
	spec.Token = ""
	spec.TokenDir = ""

	if _, err := m.Start(context.Background(), spec); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// The token file must exist under <base>/<jobID>/token.
	dir := filepath.Join(base, goodID)
	path := filepath.Join(dir, tokenFileName)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading token file: %v", err)
	}
	if len(got) != 32 {
		t.Errorf("token file content length = %d, want 32 hex chars", len(got))
	}
	if _, err := hex.DecodeString(string(got)); err != nil {
		t.Errorf("token file content is not hex: %v", err)
	}

	// The bind-mount argv must reference the same dir.
	calls := client.Calls()
	if len(calls) < 1 {
		t.Fatal("no client calls")
	}
	joined := joinArgs(calls[0])
	if !contains(joined, dir) {
		t.Errorf("argv missing token dir %q\ngot: %s", dir, joined)
	}
	if !contains(joined, "/run/athanor") {
		t.Errorf("argv missing bind target /run/athanor\ngot: %s", joined)
	}
}

// TestStart_RemovesTokenDirOnClientFailure asserts that if podman
// rejects the run, the token dir we just created does not leak to
// disk. The fail-closed posture is part of the security contract.
func TestStart_RemovesTokenDirOnClientFailure(t *testing.T) {
	base := t.TempDir()
	client := &fakeClient{
		responder: func(args []string) (stdout, stderr []byte, err error) {
			return nil, nil, errors.New("simulated podman failure")
		},
	}
	m := New(client, &stubFreezer{}, base)

	spec := validSpec()
	spec.Token = ""
	spec.TokenDir = ""

	_, err := m.Start(context.Background(), spec)
	if err == nil {
		t.Fatal("Start succeeded unexpectedly; want error from fake podman")
	}

	dir := filepath.Join(base, goodID)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("token dir %q still exists after failed Start: stat err = %v", dir, err)
	}
}

// TestStart_RejectsPartialToken asserts that the "Token without
// TokenDir" or "TokenDir without Token" combinations are rejected
// with ErrInvalidSpec. The two must travel together; otherwise the
// bind mount would either fail or — worse — succeed with the wrong
// contents.
func TestStart_RejectsPartialToken(t *testing.T) {
	cases := []struct {
		name        string
		token, dir  string
	}{
		{"token only", "deadbeef", ""},
		{"dir only", "", "/tmp/whatever"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeClient{}
			m := New(client, &stubFreezer{}, "")
			spec := validSpec()
			spec.Token = tc.token
			spec.TokenDir = tc.dir
			_, err := m.Start(context.Background(), spec)
			if !errors.Is(err, ErrInvalidSpec) {
				t.Errorf("err = %v, want ErrInvalidSpec", err)
			}
			if len(client.Calls()) != 0 {
				t.Errorf("client called %d times for invalid spec, want 0", len(client.Calls()))
			}
		})
	}
}

// TestStop_RemovesTokenDir asserts that Stop removes the per-job
// token dir, regardless of whether podman rm -f succeeded.
func TestStop_RemovesTokenDir(t *testing.T) {
	base := t.TempDir()
	client := &fakeClient{}
	m := New(client, &stubFreezer{}, base)

	spec := validSpec()
	spec.Token = ""
	spec.TokenDir = ""

	if _, err := m.Start(context.Background(), spec); err != nil {
		t.Fatalf("Start: %v", err)
	}
	dir := filepath.Join(base, goodID)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("token dir missing after Start: %v", err)
	}
	if err := m.Stop(context.Background(), goodID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("token dir %q still exists after Stop: stat err = %v", dir, err)
	}
}

// TestToken_NotInPodmanArgv asserts the token never appears in the
// argv the daemon hands to podman. The token is on disk; only the
// bind-mount path appears in argv. This is the structural proof
// that the token is not exfiltrated through the podman command
// line (visible in process listings).
func TestToken_NotInPodmanArgv(t *testing.T) {
	base := t.TempDir()
	client := &fakeClient{}
	m := New(client, &stubFreezer{}, base)

	spec := validSpec()
	spec.Token = ""
	spec.TokenDir = ""

	if _, err := m.Start(context.Background(), spec); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Read the token from disk — that's the value we must NOT see
	// in any client.Run argv.
	tokenPath := filepath.Join(base, goodID, tokenFileName)
	tokBytes, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("reading token: %v", err)
	}
	tok := string(tokBytes)

	for i, call := range client.Calls() {
		for j, arg := range call {
			if contains(arg, tok) {
				t.Errorf("token %q appeared in client.Run argv at call %d arg %d (%q); the token must never enter podman's argv", tok, i, j, arg)
			}
		}
	}
}
