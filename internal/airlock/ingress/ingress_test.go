// M4-T2 ingress tests. The end-to-end flow is:
//
//  1. Create a temp workspace (inbox/, .processed/, quarantine/).
//  2. Open a real SQLite store (in-memory or temp file).
//  3. Construct a registry with two fake scanners (one
//     always-clean, one always-rejected) so we can drive
//     both code paths.
//  4. Start the Watcher with a non-frozen Freezer stub.
//  5. Drop a file into inbox/, wait for fsnotify + the
//     processor to run.
//  6. Assert the disposition: .processed/ for clean;
//     quarantine/<date>/ for rejected; the original file
//     still in inbox/.
//
// The tests are timing-sensitive (fsnotify has a small
// latency). `t.TempDir()` is used for the workspace so
// the OS is the source of truth for inode events. The
// processor is polled with a short retry loop in the
// assertion phase.
package ingress

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/airlock/scanner"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

// fakeFreezer is a Freezer stub. Tests toggle Frozen via
// the field; the type implements Freezer. The atomic.Bool
// is required: the test goroutine writes, the processor
// goroutine reads. A plain bool triggers the race detector.
type fakeFreezer struct {
	frozen atomic.Bool
}

func (f *fakeFreezer) Frozen() bool { return f.frozen.Load() }

// openTestStore opens a real SQLite database in a temp
// file and runs the embedded migrations. The CGO driver
// rejects in-memory when concurrent goroutines write, so
// each test gets its own file. Migrations are required
// because the ingress test exercises the quarantine
// table (migration 0008).
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// waitFor polls cond every 10ms until it returns true or
// the timeout elapses. Used to bridge the fsnotify
// latency and the processor's drain cycle.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}

// writeFile writes content to a file in dir, returning
// the relpath under dir.
func writeFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return name
}

// fixedScanner is a Scanner that returns a fixed result.
type fixedScanner struct {
	name   string
	result scanner.ScanResult
}

func (f *fixedScanner) Name() string { return f.name }

func (f *fixedScanner) Scan(ctx context.Context, in scanner.ScanInput) (scanner.ScanResult, error) {
	return f.result, nil
}

// newTestWatcher builds a Watcher in a fresh temp
// workspace with the given scanner list (one entry per
// pipeline list; see Option for details).
type watcherOption struct {
	clean    bool // include the "clean" scanner in the ingress list
	rejected bool // include the "rejected" scanner in the ingress list
}

// newTestWatcher builds a Watcher in a fresh temp
// workspace. The option controls which fake scanner
// is wired into the ingress pipeline:
//   - opt.clean:    every file → VerdictClean (lands in .processed/)
//   - opt.rejected: every file → VerdictRejected (lands in quarantine/)
//
// Tests that need both behaviors construct their own
// registry; the helper is for the common cases. Exactly
// one of clean or rejected must be set.
func newTestWatcher(t *testing.T, st *store.Store, opt watcherOption) (*Watcher, *fakeFreezer) {
	t.Helper()
	if opt.clean == opt.rejected {
		t.Fatal("newTestWatcher: set exactly one of clean or rejected")
	}
	root := filepath.Join(t.TempDir(), "inbox")
	scanners := map[string]scanner.Scanner{}
	var name string
	if opt.clean {
		name = "clean"
		scanners[name] = &fixedScanner{name: name, result: scanner.ScanResult{Verdict: scanner.VerdictClean}}
	} else {
		name = "rejected"
		scanners[name] = &fixedScanner{name: name, result: scanner.ScanResult{Verdict: scanner.VerdictRejected, Reason: "test-rejected"}}
	}
	// The other two pipelines (egress, user_prompt) need
	// at least one scanner (fail-closed at construction).
	// T2 ships only ingress; T4 wires the real egress
	// and user_prompt scanners. Reuse the same fake.
	pipeline := []string{name}
	reg, err := scanner.NewRegistry(scanners, pipeline, pipeline, pipeline)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	fr := &fakeFreezer{}
	w, err := New(context.Background(), Options{
		Root:       root,
		Registry:   reg,
		Store:      st,
		Quarantine: store.NewQuarantineRepo(st),
		Freezer:    fr,
		Logger:     slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})
	if err != nil {
		t.Fatalf("ingress.New: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w, fr
}

// sha256Hex is a tiny test helper.
func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// TestIngress_CleanFileLandsInProcessed is the headline
// AC: a clean file goes to .processed/ and the original
// remains in inbox/.
func TestIngress_CleanFileLandsInProcessed(t *testing.T) {
	st := openTestStore(t)
	w, _ := newTestWatcher(t, st, watcherOption{clean: true})
	name := writeFile(t, w.root, "hello.txt", []byte("hello world"))
	waitFor(t, 3*time.Second, func() bool {
		matches, _ := filepath.Glob(filepath.Join(w.processed, "*.txt"))
		return len(matches) > 0
	})
	if _, err := os.Stat(filepath.Join(w.root, name)); err != nil {
		t.Errorf("original file %q missing: %v (originals-untouched invariant)", name, err)
	}
	if _, err := store.NewQuarantineRepo(st).Get(context.Background(), sha256Hex([]byte("hello world"))); err == nil {
		t.Errorf("unexpected quarantine row for clean file")
	}
}

// TestIngress_RejectedFileQuarantined is the other half
// of the AC: a rejected file goes to quarantine/, the
// original stays in inbox/, and the quarantine row is
// written with the scanner's reason.
func TestIngress_RejectedFileQuarantined(t *testing.T) {
	st := openTestStore(t)
	w, _ := newTestWatcher(t, st, watcherOption{rejected: true})
	name := writeFile(t, w.root, "evil.txt", []byte("malicious payload"))
	wantSHA := sha256Hex([]byte("malicious payload"))
	waitFor(t, 3*time.Second, func() bool {
		_, err := store.NewQuarantineRepo(st).Get(context.Background(), wantSHA)
		return err == nil
	})
	if _, err := os.Stat(filepath.Join(w.root, name)); err != nil {
		t.Errorf("original file %q missing: %v (originals-untouched invariant)", name, err)
	}
	q, err := store.NewQuarantineRepo(st).Get(context.Background(), wantSHA)
	if err != nil {
		t.Fatalf("quarantine Get: %v", err)
	}
	if !strings.Contains(q.Reason, "test-rejected") {
		t.Errorf("quarantine reason = %q, want it to contain 'test-rejected'", q.Reason)
	}
	if q.Pipeline != "ingress" {
		t.Errorf("quarantine pipeline = %q, want ingress", q.Pipeline)
	}
	today := time.Now().UTC().Format("2006-01-02")
	wantStored := filepath.Join(w.quarantine, today, wantSHA+".txt")
	if _, err := os.Stat(wantStored); err != nil {
		t.Errorf("quarantine file %q missing: %v", wantStored, err)
	}
}

// TestIngress_PathLayerRejectionStaysInInbox asserts
// that a path-layer rejection (a symlink to /etc/passwd)
// does NOT remove the file from inbox/. The audit row
// carries the path error; no .processed or quarantine
// marker is written.
func TestIngress_PathLayerRejectionStaysInInbox(t *testing.T) {
	st := openTestStore(t)
	w, _ := newTestWatcher(t, st, watcherOption{clean: true})
	target := "/etc/passwd"
	link := "leak"
	fullLink := filepath.Join(w.root, link)
	if err := os.Symlink(target, fullLink); err != nil {
		t.Skipf("symlink not supported on this filesystem: %v", err)
	}
	waitFor(t, 3*time.Second, func() bool {
		_, err := os.Lstat(fullLink)
		return err == nil
	})
	if _, err := os.Lstat(fullLink); err != nil {
		t.Errorf("symlink %q missing: %v (originals-untouched invariant on path-layer rejection)", link, err)
	}
	processedMatches, _ := filepath.Glob(filepath.Join(w.processed, "*"))
	if len(processedMatches) > 0 {
		t.Errorf("path-layer rejection wrote a .processed/ marker: %v", processedMatches)
	}
	rows, err := store.NewQuarantineRepo(st).List(context.Background(), time.Time{}, "ingress", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) > 0 {
		t.Errorf("path-layer rejection wrote a quarantine row: %v", rows)
	}
}

// TestIngress_DuplicateIgnored: re-ingesting the same
// content finds the existing quarantine row and drops
// the second event with a duplicate_ignored audit row.
func TestIngress_DuplicateIgnored(t *testing.T) {
	st := openTestStore(t)
	w, _ := newTestWatcher(t, st, watcherOption{rejected: true})
	content := []byte("same content twice")
	name := writeFile(t, w.root, "first.txt", content)
	wantSHA := sha256Hex(content)
	waitFor(t, 3*time.Second, func() bool {
		_, err := store.NewQuarantineRepo(st).Get(context.Background(), wantSHA)
		return err == nil
	})
	writeFile(t, w.root, "second.txt", content)
	time.Sleep(300 * time.Millisecond)
	rows, err := store.NewQuarantineRepo(st).List(context.Background(), time.Time{}, "ingress", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("quarantine row count = %d, want 1 (duplicate must be ignored)", len(rows))
	}
	for _, n := range []string{name, "second.txt"} {
		if _, err := os.Stat(filepath.Join(w.root, n)); err != nil {
			t.Errorf("original %q missing: %v", n, err)
		}
	}
}

// TestIngress_FrozenKillSwitchPausesProcessor: when the
// Freezer reports frozen, the processor pauses; on
// unfreeze, the queue drains.
func TestIngress_FrozenKillSwitchPausesProcessor(t *testing.T) {
	st := openTestStore(t)
	w, fr := newTestWatcher(t, st, watcherOption{rejected: true})
	fr.frozen.Store(true)
	writeFile(t, w.root, "frozen.txt", []byte("won't process yet"))
	time.Sleep(300 * time.Millisecond)
	wantSHA := sha256Hex([]byte("won't process yet"))
	if _, err := store.NewQuarantineRepo(st).Get(context.Background(), wantSHA); err == nil {
		t.Errorf("file processed while frozen (kill switch broken)")
	}
	fr.frozen.Store(false)
	waitFor(t, 5*time.Second, func() bool {
		_, err := store.NewQuarantineRepo(st).Get(context.Background(), wantSHA)
		return err == nil
	})
}
