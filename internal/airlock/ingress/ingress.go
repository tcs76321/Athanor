// M4-T2 ingress pipeline (ROADMAP M4-T2; ADR-0015).
//
// The pipeline observes `workspace/inbox/` via fsnotify and
// processes every new file through:
//
//  1. path-layer validation (`airlock/paths`) — reject
//     absolute, traversal, NULL bytes, symlink escape, device
//     nodes, setuid/setgid, executables (default-on).
//  2. SHA-256 of the file bytes — for idempotency.
//  3. duplicate detection — drop if the SHA-256 is already in
//     `quarantined_files` or `.processed/`.
//  4. scanner registry (`airlock/scanner.PipelineIngress`).
//  5. disposition — `.processed/<sha256>.<ext>` on clean;
//     `quarantine/<date>/<sha256>.<ext>` on uncertain or
//     rejected; file remains in `inbox/` for the user.
//
// # Concurrency model
//
// The Watcher runs one fsnotify.Watcher goroutine that
// translates raw events into `pendingFile` records and pushes
// them onto a buffered channel. The Processor consumes the
// channel serially — file scanning is I/O-bound, and running
// scanners concurrently against a single file would just
// thrash. A future optimization (M4-T8) can shard the
// channel across N processors if throughput demands it.
package ingress

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/tcs76321/athanor/internal/airlock/paths"
	"github.com/tcs76321/athanor/internal/airlock/scanner"
	"github.com/tcs76321/athanor/internal/store"
)

// Freezer is the kill-switch surface consulted before
// processing every pending file (§22.1). When the daemon is
// frozen, the processor stops draining the queue but does
// not lose events. Satisfied by *control.KillSwitch in
// production; tests pass a stub.
type Freezer interface {
	Frozen() bool
}

// pendingFile is one inbox entry the watcher observed.
// The Processor hashes + scans + disposes.
type pendingFile struct {
	relPath string // path under the inbox root, e.g. "report.pdf" or "subdir/x.txt"
	seenAt  time.Time
}

// Watcher is the fsnotify-based observer. One Watcher per
// inbox root. The Watcher's lifetime is bound to the
// caller's context; cancel the context to stop it.
type Watcher struct {
	root        string
	processed   string // <root>/.processed (created at construction)
	quarantine  string // <root>/../quarantine (sibling of inbox; created at construction)
	registry    *scanner.Registry
	store       *store.Store
	quarantine_ *store.QuarantineRepo
	freezer     Freezer
	logger      *slog.Logger

	watcher   *fsnotify.Watcher
	pending   chan pendingFile
	closeOnce sync.Once
	closed    chan struct{}
}

// Options configures a Watcher. Required fields: Root,
// Registry, Store, Quarantine, Freezer. Logger is optional
// (nil → slog.Default()).
type Options struct {
	Root       string
	Registry   *scanner.Registry
	Store      *store.Store
	Quarantine *store.QuarantineRepo
	Freezer    Freezer
	Logger     *slog.Logger
}

// New constructs and starts a Watcher. The Watcher's
// processor goroutine begins draining the queue immediately.
// Errors during start (e.g. inbox not creatable) are
// returned; the caller should refuse to boot the daemon.
func New(ctx context.Context, opts Options) (*Watcher, error) {
	if opts.Root == "" {
		return nil, fmt.Errorf("ingress: Root is required")
	}
	if opts.Registry == nil {
		return nil, fmt.Errorf("ingress: Registry is required")
	}
	if opts.Store == nil {
		return nil, fmt.Errorf("ingress: Store is required")
	}
	if opts.Quarantine == nil {
		return nil, fmt.Errorf("ingress: Quarantine is required")
	}
	if opts.Freezer == nil {
		return nil, fmt.Errorf("ingress: Freezer is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	processed := filepath.Join(opts.Root, ".processed")
	quarantine := filepath.Join(filepath.Dir(opts.Root), "quarantine")
	for _, dir := range []string{opts.Root, processed, quarantine} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("ingress: mkdir %s: %w", dir, err)
		}
	}
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("ingress: fsnotify: %w", err)
	}
	if err := fw.Add(opts.Root); err != nil {
		_ = fw.Close()
		return nil, fmt.Errorf("ingress: watch %s: %w", opts.Root, err)
	}
	w := &Watcher{
		root:        opts.Root,
		processed:   processed,
		quarantine:  quarantine,
		registry:    opts.Registry,
		store:       opts.Store,
		quarantine_: opts.Quarantine,
		freezer:     opts.Freezer,
		logger:      opts.Logger,
		watcher:     fw,
		pending:     make(chan pendingFile, 256),
		closed:      make(chan struct{}),
	}
	go w.loop(ctx)
	go w.processor(ctx)
	return w, nil
}

// Close stops the watcher. Idempotent. The fsnotify Watcher
// is closed and the pending channel is drained (any
// in-flight events are processed before Close returns).
func (w *Watcher) Close() error {
	var err error
	w.closeOnce.Do(func() {
		if w.watcher != nil {
			err = w.watcher.Close()
		}
		close(w.closed)
	})
	return err
}

// loop translates fsnotify events into pendingFile records.
// It is the only goroutine that touches the fsnotify Watcher;
// the processor consumes the channel serially.
func (w *Watcher) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.closed:
			return
		case ev, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			// Only Create and Write events matter;
			// Chmod and Remove are noise for our
			// purposes. (Chmod on a quarantined file
			// would be a hostile action; the
			// audit-row layer can record that
			// separately if needed.)
			if ev.Op&(fsnotify.Create|fsnotify.Write) == 0 {
				continue
			}
			rel, err := filepath.Rel(w.root, ev.Name)
			if err != nil {
				w.logger.Warn("ingress: relpath failed", "path", ev.Name, "err", err)
				continue
			}
			// Skip our own write-out directories.
			if rel == ".processed" || strings.HasPrefix(rel, ".processed"+string(filepath.Separator)) ||
				rel == "quarantine" || strings.HasPrefix(rel, "quarantine"+string(filepath.Separator)) {
				continue
			}
			select {
			case w.pending <- pendingFile{relPath: rel, seenAt: time.Now()}:
			default:
				w.logger.Warn("ingress: pending channel full; dropping event",
					"relpath", rel)
			}
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			w.logger.Warn("ingress: fsnotify error", "err", err)
		}
	}
}

// processor consumes pending files and runs the pipeline.
// Honors the kill switch: when frozen, the loop blocks on
// the channel select (no drain) but events accumulate.
func (w *Watcher) processor(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.closed:
			// Drain any remaining pending events
			// before exiting, so a graceful
			// shutdown doesn't leave the queue
			// half-processed.
			for {
				select {
				case pf := <-w.pending:
					w.process(ctx, pf)
				default:
					return
				}
			}
		case pf := <-w.pending:
			if w.freezer.Frozen() {
				// Re-enqueue and pause. The
				// re-enqueue will block if the
				// channel is full, in which case
				// we drop the event with a
				// warning (rare; a 256-deep
				// channel with the daemon
				// frozen is an unusual case).
				select {
				case w.pending <- pf:
				default:
					w.logger.Warn("ingress: frozen; pending full; dropping",
						"relpath", pf.relPath)
				}
				select {
				case <-time.After(500 * time.Millisecond):
				case <-ctx.Done():
					return
				}
				continue
			}
			w.process(ctx, pf)
		}
	}
}

// process runs the full pipeline for one file. Every write
// goes to .processed/ or quarantine/, never to the user's
// original file.
func (w *Watcher) process(ctx context.Context, pf pendingFile) {
	fullPath := filepath.Join(w.root, pf.relPath)
	// Route the externally-controlled relative path through the §21.3
	// containment layer and open it with O_NOFOLLOW, so Validate's Lstat
	// and the read are the same open file: a symlink swapped in after the
	// check cannot be followed (the kernel-level half of M4-T1). The
	// previous code validated, then re-opened by path, leaving the
	// Lstat→open TOCTOU window open for inbox filenames.
	f, err := paths.OpenNoFollow(w.root, pf.relPath)
	if err != nil {
		w.auditRejected(ctx, pf, err.Error())
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		w.logger.Warn("ingress: stat failed", "relpath", pf.relPath, "err", err)
		return
	}
	bytes, err := io.ReadAll(f)
	if err != nil {
		w.logger.Warn("ingress: read failed", "relpath", pf.relPath, "err", err)
		return
	}
	sha := sha256.Sum256(bytes)
	size := int64(len(bytes))
	shaHex := hex.EncodeToString(sha[:])
	if _, err := w.quarantine_.Get(ctx, shaHex); err == nil {
		w.auditDuplicate(ctx, pf, shaHex, "quarantine")
		return
	} else if !errors.Is(err, store.ErrQuarantineNotFound) {
		w.logger.Warn("ingress: quarantine lookup failed", "relpath", pf.relPath, "err", err)
		return
	}
	if w.processedExists(shaHex) {
		w.auditDuplicate(ctx, pf, shaHex, "processed")
		return
	}
	result := w.registry.RunAll(ctx, scanner.PipelineIngress, scanner.ScanInput{
		Path:  fullPath,
		Bytes: bytes,
		Size:  int64(len(bytes)),
		Mode:  info.Mode(),
	})
	if result.Verdict == scanner.VerdictClean {
		if err := w.copyToProcessed(shaHex, pf.relPath, bytes); err != nil {
			w.logger.Warn("ingress: copy-to-processed failed", "relpath", pf.relPath, "err", err)
			return
		}
		w.auditAccepted(ctx, pf, shaHex, size, result)
		return
	}
	stored, err := w.copyToQuarantine(shaHex, pf.relPath, bytes)
	if err != nil {
		w.logger.Warn("ingress: copy-to-quarantine failed", "relpath", pf.relPath, "err", err)
		return
	}
	q := store.Quarantine{
		SHA256:     shaHex,
		RelPath:    pf.relPath,
		Reason:     result.Reason,
		SourceSize: size,
		StoredPath: stored,
		Pipeline:   "ingress",
		IngestedAt: time.Now().UTC(),
		Details:    scannerResultDetailsJSON(result),
	}
	if _, err := w.quarantine_.Put(ctx, q); err != nil {
		w.logger.Warn("ingress: quarantine put failed", "relpath", pf.relPath, "err", err)
		return
	}
	w.auditQuarantined(ctx, pf, shaHex, size, stored, result)
}

// processedExists checks whether a `.processed/<sha256>.*`
// marker already exists.
func (w *Watcher) processedExists(sha256hex string) bool {
	matches, err := filepath.Glob(filepath.Join(w.processed, sha256hex+".*"))
	if err != nil {
		return false
	}
	return len(matches) > 0
}

// copyToProcessed writes the bytes to
// `<processed>/<sha256>.<ext>` atomically.
func (w *Watcher) copyToProcessed(sha256hex, relPath string, bytes []byte) error {
	ext := filepath.Ext(relPath)
	name := sha256hex + ext
	dst := filepath.Join(w.processed, name)
	return atomicWrite(dst, bytes, 0o600)
}

// copyToQuarantine writes the bytes to
// `<quarantine>/<YYYY-MM-DD>/<sha256>.<ext>` atomically.
// The date-bucketed layout keeps the directory from
// accumulating tens of thousands of files in a single
// entry, which is hostile to fs ops and operator grep.
func (w *Watcher) copyToQuarantine(sha256hex, relPath string, bytes []byte) (string, error) {
	ext := filepath.Ext(relPath)
	bucket := time.Now().UTC().Format("2006-01-02")
	dir := filepath.Join(w.quarantine, bucket)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := sha256hex + ext
	dst := filepath.Join(dir, name)
	if err := atomicWrite(dst, bytes, 0o600); err != nil {
		return "", err
	}
	return dst, nil
}

// atomicWrite writes bytes to a temp file in the same
// directory as dst, fsyncs, and renames over dst. The
// rename is atomic on POSIX.
func atomicWrite(dst string, bytes []byte, perm os.FileMode) error {
	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, ".ath-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.Write(bytes); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, dst)
}

// scannerResultDetailsJSON encodes a PipelineResult's
// per-scanner breakdown as JSON for the quarantine row's
// `details` column. A marshal error is non-fatal: the row
// is still written, with an empty details payload.
func scannerResultDetailsJSON(result scanner.PipelineResult) []byte {
	type perScanner struct {
		Verdict string `json:"verdict"`
		Reason  string `json:"reason,omitempty"`
	}
	out := struct {
		Verdict  string                `json:"verdict"`
		Reason   string                `json:"reason,omitempty"`
		PerScn   map[string]perScanner `json:"per_scanner"`
		Duration string                `json:"duration"`
	}{
		Verdict:  result.Verdict.String(),
		Reason:   result.Reason,
		PerScn:   make(map[string]perScanner, len(result.PerScanner)),
		Duration: result.TotalDuration.String(),
	}
	for _, ps := range result.PerScanner {
		out.PerScn[ps.Scanner] = perScanner{
			Verdict: ps.Result.Verdict.String(),
			Reason:  ps.Result.Reason,
		}
	}
	raw, err := jsonMarshal(out)
	if err != nil {
		return nil
	}
	return raw
}
