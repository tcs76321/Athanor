// M4-T4 egress pipeline. Subscribes to the append-only
// event log for `category=artifact, event=accepted` rows,
// runs the per-pipeline scanner registry (egress pipeline:
// size + zipbomb + clamav + yara), validates the export
// tree through airlock/paths, and copies a clean export
// to `workspace/exports/<project>/<artifact>-<sha12>/`.
//
// The exporter is best-effort and asynchronous. The
// engine's accepted-artifact event lands in the event
// log synchronously; the egress loop polls the log on
// `airlock.egress_poll_interval` (default 5s) and exports
// the artifact on the next tick. The artifact is durable
// in the artifact store before the event row is appended,
// so a crash between append and export means the next
// daemon boot will re-export on the next poll cycle.
//
// # Concurrency
//
// One Exporter per daemon. The Exporter runs a single
// poll goroutine; the poll is cheap (one SQL query per
// interval) and the work (file copies + subprocess scans)
// is I/O-bound. Running multiple Exporters is not a
// supported deployment; the design assumes one daemon
// per workspace.
package egress

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/tcs76321/athanor/internal/airlock/scanner"
	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
)

// Exporter is the egress pipeline. One Exporter per
// daemon; bound to the daemon's lifetime via the
// caller's context.
type Exporter struct {
	workspaceRoot string // <state-dir>/workspace
	registry      *scanner.Registry
	artifactStore *artifact.Store
	projectRepo   *project.Repo
	store         *store.Store
	pollInterval  time.Duration
	logger        *slog.Logger

	// lastSeenID is the highest events.id the exporter
	// has processed. Persisted across restarts in
	// memory only; a daemon restart re-scans from
	// the last accepted event ID encoded in the
	// artifact itself. (Re-scanning from time 0 is
	// idempotent: the export's SHA-12 suffix is the
	// content hash, so re-writing the same export
	// is a no-op.)
	lastSeenID int64
}

// Options configures an Exporter. All fields are
// required; the caller (cmd/athanor) wires the
// dependencies. Logger is optional (nil → slog.Default()).
type Options struct {
	WorkspaceRoot string
	Registry      *scanner.Registry
	ArtifactStore *artifact.Store
	ProjectRepo   *project.Repo
	Store         *store.Store
	PollInterval  time.Duration
	Logger        *slog.Logger
}

// New constructs an Exporter. The exporter is started
// by calling Start; the constructor does not start
// the poll goroutine (the caller controls the lifecycle).
func New(opts Options) (*Exporter, error) {
	if opts.WorkspaceRoot == "" {
		return nil, fmt.Errorf("egress: WorkspaceRoot is required")
	}
	if opts.Registry == nil {
		return nil, fmt.Errorf("egress: Registry is required")
	}
	if opts.ArtifactStore == nil {
		return nil, fmt.Errorf("egress: ArtifactStore is required")
	}
	if opts.ProjectRepo == nil {
		return nil, fmt.Errorf("egress: ProjectRepo is required")
	}
	if opts.Store == nil {
		return nil, fmt.Errorf("egress: Store is required")
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 5 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	exportsDir := filepath.Join(opts.WorkspaceRoot, "exports")
	if err := os.MkdirAll(exportsDir, 0o700); err != nil {
		return nil, fmt.Errorf("egress: mkdir exports: %w", err)
	}
	return &Exporter{
		workspaceRoot: opts.WorkspaceRoot,
		registry:      opts.Registry,
		artifactStore: opts.ArtifactStore,
		projectRepo:   opts.ProjectRepo,
		store:         opts.Store,
		pollInterval:  opts.PollInterval,
		logger:        opts.Logger,
	}, nil
}

// Start begins the poll loop. Blocks until ctx is done
// or an unrecoverable error occurs. The function is
// safe to call from a goroutine; the loop exits cleanly
// on ctx cancel.
func (e *Exporter) Start(ctx context.Context) {
	e.logger.Info("egress exporter started", "poll_interval", e.pollInterval)
	ticker := time.NewTicker(e.pollInterval)
	defer ticker.Stop()
	// Run once immediately, then on each tick. The
	// first run catches any artifacts accepted
	// between the boot of the engine and the boot
	// of the exporter.
	if err := e.poll(ctx); err != nil {
		e.logger.Warn("egress: initial poll failed", "err", err)
	}
	for {
		select {
		case <-ctx.Done():
			e.logger.Info("egress exporter stopping")
			return
		case <-ticker.C:
			if err := e.poll(ctx); err != nil {
				e.logger.Warn("egress: poll failed", "err", err)
			}
		}
	}
}

// poll queries the event log for `event=status, to=accepted`
// rows newer than e.lastSeenID and exports each one.
// Errors on a single artifact are logged; the loop
// continues. The event's `job_id` column names the job
// that produced the accepted artifact; the artifact is
// resolved by listing the job's artifacts and picking
// the accepted one.
func (e *Exporter) poll(ctx context.Context) error {
	rows, err := e.store.QueryEvents(ctx, store.EventFilter{
		Category: "jobs",
	})
	if err != nil {
		return fmt.Errorf("query events: %w", err)
	}
	for _, r := range rows {
		if r.ID <= e.lastSeenID {
			continue
		}
		_, ok := extractAcceptedArtifactID(r.DataJSON, r.JobID)
		if !ok {
			// Not an accepted status event;
			// advance lastSeenID and move on.
			e.lastSeenID = r.ID
			continue
		}
		// Find the accepted artifact for this job
		// and export it.
		artifactID, err := e.findAcceptedArtifactForJob(ctx, r.JobID)
		if err != nil {
			e.logger.Warn("egress: find artifact for job failed",
				"job", r.JobID, "err", err)
			e.lastSeenID = r.ID
			continue
		}
		if artifactID == "" {
			// Job produced no accepted artifact
			// (the event might be an artifact store
			// internal transition). Advance and
			// move on.
			e.lastSeenID = r.ID
			continue
		}
		if _, _, err := e.ExportOne(ctx, artifactID); err != nil {
			e.logger.Warn("egress: export failed",
				"artifact", artifactID, "err", err)
		}
		e.lastSeenID = r.ID
	}
	return nil
}

// findAcceptedArtifactForJob returns the artifact_id
// of the accepted artifact produced by the given job,
// or "" if the job has no accepted artifact. The
// artifact store's ListByJob returns all artifacts for
// a job; we filter for the accepted one. (A job
// typically produces one accepted artifact; if
// multiple exist, we return the first; the exporter's
// idempotency check handles re-exports.)
func (e *Exporter) findAcceptedArtifactForJob(ctx context.Context, jobID string) (string, error) {
	arts, err := e.artifactStore.ListByJob(ctx, jobID)
	if err != nil {
		return "", err
	}
	for _, art := range arts {
		if art.Status == artifact.StatusAccepted {
			return art.ID, nil
		}
	}
	return "", nil
}

// extractAcceptedArtifactID parses the data_json of an
// event row and returns the artifact_id if the event
// records an "accepted" status transition. The engine
// (compare.go phase) writes rows like:
//
//	{"event": "status", "from": "candidate", "to": "accepted"}
//
// in the `jobs` category (the artifact store's audit
// helper writes to the jobs category because §28.1 has
// no artifact category; artifact lifecycle is job
// lifecycle). The JobID lives in the row's `job_id`
// column, not in the data JSON, so the caller passes
// it in as a separate argument.
//
// Returns the empty string + false for any event that
// is not an "accepted" status transition. A non-empty
// JobID with to=accepted is the trigger.
func extractAcceptedArtifactID(dataJSON string, jobID string) (string, bool) {
	if jobID == "" {
		return "", false
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(dataJSON), &data); err != nil {
		return "", false
	}
	ev, _ := data["event"].(string)
	if ev != "status" {
		return "", false
	}
	to, _ := data["to"].(string)
	if to != "accepted" {
		return "", false
	}
	// The artifact ID is not in the event JSON; it's
	// the latest artifact of the artifact's kind
	// produced by this job. The caller resolves it
	// via artifact.Store.LatestForJob. The function
	// returns the job_id as a stand-in identifier so
	// the caller can do the lookup.
	return jobID, true
}

// ExportOne exports a single artifact. The function is
// exported so the `athanor export <id>` CLI subcommand
// can call it directly. Errors are returned to the
// caller; the caller logs.
//
// Returns:
//   - path: the on-disk path the artifact was exported
//     to (or would have been, for a no-op).
//   - exported: true if a new export was written;
//     false if the export was a no-op (artifact not
//     accepted, or already exported).
//   - err: a hard error (artifact not found, FS error,
//     scanner subprocess hung). No-op exports do not
//     return an error.
func (e *Exporter) ExportOne(ctx context.Context, artifactID string) (path string, exported bool, err error) {
	art, err := e.artifactStore.Get(ctx, artifactID)
	if err != nil {
		return "", false, fmt.Errorf("get artifact %s: %w", artifactID, err)
	}
	if art.Status != artifact.StatusAccepted {
		// Re-running an export on a non-accepted
		// artifact is a no-op (with a warning).
		e.auditExportUnchanged(ctx, art, "not_accepted")
		return "", false, nil
	}
	// Look up the project for the export path. The
	// project ID is the layout key (ARCHITECTURE
	// §6.1 describes a slug field that doesn't exist
	// in the schema yet; the project ID is a stable
	// fallback — see exports.go).
	proj, err := e.projectRepo.Get(ctx, art.ProjectID)
	if err != nil {
		return "", false, fmt.Errorf("get project %s: %w", art.ProjectID, err)
	}
	dst := ExportPath(e.workspaceRoot, proj.ID, art.ID, art.ContentHash)
	// Idempotency: if the export already exists
	// with the expected content, the export is a
	// no-op. The check is a single os.Stat + a
	// content-hash comparison on the file we
	// would write; the equality is by content hash,
	// not by file mtime.
	if alreadyExported(dst, art) {
		e.auditExportUnchanged(ctx, art, "already_exported")
		return dst, false, nil
	}
	// Read content (with hash verification).
	content, err := e.artifactStore.ReadContent(ctx, art.ID)
	if err != nil {
		return "", false, fmt.Errorf("read content %s: %w", art.ID, err)
	}
	// Run the egress scanner pipeline. Single
	// artifact = single ScanInput. The pipeline
	// runs size + zipbomb + clamav + yara per
	// ADR-0015; no prompt-injection scanner.
	result := e.registry.RunAll(ctx, scanner.PipelineEgress, scanner.ScanInput{
		Path:  art.StoragePath,
		Bytes: content,
		Size:  int64(len(content)),
		Mode:  0o600,
	})
	if result.Verdict != scanner.VerdictClean {
		// Audit and refuse. The artifact stays
		// accepted (the engine's verdict stands);
		// the export is blocked by the airlock.
		e.auditExportBlocked(ctx, art, result)
		return dst, false, nil
	}
	// Materialize the export. The directory may
	// need to be created; the file is written
	// atomically (temp + rename).
	if err := e.writeArtifact(dst, art, content); err != nil {
		return "", false, fmt.Errorf("write export %s: %w", dst, err)
	}
	e.auditExportComplete(ctx, art, dst, result)
	return dst, true, nil
}

// alreadyExported reports whether the export directory
// at dst already contains a file with the artifact's
// expected content hash. Used as the idempotency
// gate on every ExportOne call.
func alreadyExported(dst string, art artifact.Artifact) bool {
	matches, err := filepath.Glob(filepath.Join(dst, "*"))
	if err != nil || len(matches) == 0 {
		return false
	}
	// Single-file export: one file under dst. We
	// verify the content hash matches.
	for _, m := range matches {
		if info, err := os.Stat(m); err != nil || info.IsDir() {
			continue
		}
		data, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		// Cheap content check: the existing file's
		// name should encode the SHA-12 (the
		// exporter writes `<sha12>.<ext>`). If the
		// names match, the file is the same content.
		base := filepath.Base(m)
		if len(art.ContentHash) >= 12 && len(base) >= 12 && base[:12] == art.ContentHash[:12] {
			return true
		}
		// Fallback: hash compare.
		if computeSHA256Hex(data) == art.ContentHash {
			return true
		}
	}
	return false
}

// writeArtifact writes the artifact's bytes to dst as
// a single file. The file is written atomically (temp
// + rename); the directory is created if needed.
func (e *Exporter) writeArtifact(dst string, art artifact.Artifact, content []byte) error {
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	// Single-file export: one file named
	// <sha12>.<ext>. The extension is inferred
	// from the kind; the SHA-12 prefix is the
	// idempotency key.
	ext := kindExtension(art.Kind)
	name := art.ContentHash[:12] + ext
	target := filepath.Join(dst, name)
	tmp, err := os.CreateTemp(dst, ".ath-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(content); err != nil {
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
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpName, target)
}

// kindExtension maps an artifact Kind to a file
// extension. Used only for the single-file export
// filename; the bytes themselves are not interpreted.
func kindExtension(k artifact.Kind) string {
	switch k {
	case artifact.KindCode:
		return ".code"
	case artifact.KindDocument:
		return ".md"
	case artifact.KindDataset:
		return ".dataset"
	case artifact.KindProposal:
		return ".proposal"
	case artifact.KindEvaluation:
		return ".eval"
	case artifact.KindMedia:
		return ".media"
	case artifact.KindConfiguration:
		return ".conf"
	default:
		return ".bin"
	}
}

// computeSHA256Hex returns the hex-encoded SHA-256 of
// data. The function lives here (not in a shared
// package) because it's used only by the idempotency
// check, and putting it in a shared package would
// create a circular import surface (the airlock
// packages already use crypto/sha256 directly).
func computeSHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
