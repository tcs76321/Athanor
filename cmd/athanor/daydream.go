package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/mce"
	"github.com/tcs76321/athanor/internal/power"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
)

// M5-T6 minimal daydream driver (ARCHITECTURE §17.1–§17.2; ADR-0025 §6).
//
// One §17.1 action — Memory Consolidation — runs on a power/idle-gated loop:
// when the daemon is idle, daydreaming is allowed, and the kill switch is not
// frozen, a pass compacts terminal-job event logs (deterministic) and accepted
// documentation artifacts (semantic) at Temp 0.0. The full Daydream Engine
// (the other five actions, the §17.3 DaydreamLog, and the OS AC/battery
// watcher) is M7-T1/T2.
//
// The loop is off by default: the default power profile is interactive, whose
// limits set AllowDaydreaming=false. Enabling autonomy turns it on.

const (
	// defaultConsolidationBatch bounds how many items one source contributes
	// to a single pass.
	defaultConsolidationBatch = 20
	// minDaydreamInterval floors the loop cadence so a tiny idle_resume_after
	// cannot spin it.
	minDaydreamInterval = time.Minute
	// defaultDaydreamWallTime bounds a pass when the config leaves the limit
	// unset.
	defaultDaydreamWallTime = 30 * time.Minute
)

// daydreamPower is the slice of *power.PowerManager the loop consults.
type daydreamPower interface{ GetLimits() power.Limits }

// daydreamFreezer is the §22 kill-switch surface (the engine's Freezer shape).
type daydreamFreezer interface{ Frozen() bool }

// daydreamIndexer is the M5-T8 repository-indexing slice the loop consults
// (ADR-0028 §6). One bounded pass per project per tick, so exploration yields
// to real work like every other daydream action.
type daydreamIndexer interface {
	IndexOnce(ctx context.Context, projectID, path string) (mce.IndexResult, error)
}

// daydreamDeps is everything the loop consults, so serve.go reads at the call
// site rather than through a long positional constructor.
type daydreamDeps struct {
	cfg         *config.Config
	jobs        *job.Repository
	events      *store.Store
	artifacts   *artifact.Store
	compactions *mce.CompactStore
	compactor   mce.Compactor
	power       daydreamPower
	freezer     daydreamFreezer
	// projects + indexer drive the §17.1 Repository Exploration action
	// (M5-T8). Both nil disables it; the M5-T6 consolidation tests construct
	// a runner without them.
	projects *project.Repo
	indexer  daydreamIndexer
	log      *slog.Logger
}

// daydreamRunner owns the loop's lifecycle.
type daydreamRunner struct {
	deps   daydreamDeps
	cancel context.CancelFunc
	done   chan struct{}
}

// startDaydream launches the memory-consolidation loop. It always returns a
// runner; every pass is independently gated, so a disabled, frozen, or busy
// daemon simply does nothing.
func startDaydream(parent context.Context, deps daydreamDeps) *daydreamRunner {
	if deps.log == nil {
		deps.log = slog.Default()
	}
	ctx, cancel := context.WithCancel(parent)
	r := &daydreamRunner{deps: deps, cancel: cancel, done: make(chan struct{})}
	go r.loop(ctx)
	return r
}

// Close stops the loop and waits for it to exit.
func (r *daydreamRunner) Close() {
	r.cancel()
	<-r.done
}

// interval returns the loop cadence, floored.
func (r *daydreamRunner) interval() time.Duration {
	d := r.deps.cfg.Power.IdleResumeAfter.D()
	if d < minDaydreamInterval {
		return minDaydreamInterval
	}
	return d
}

// loop runs one pass per tick until ctx is cancelled.
func (r *daydreamRunner) loop(ctx context.Context) {
	defer close(r.done)
	t := time.NewTicker(r.interval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.pass(ctx); err != nil {
				r.deps.log.Warn("daydream pass failed", "err", err)
			}
		}
	}
}

// pass runs at most one pass of each daydream action when every gate allows
// it: configuration, the power profile, the kill switch, and idle state.
func (r *daydreamRunner) pass(ctx context.Context) error {
	if !config.Val(r.deps.cfg.Power.DaydreamOnIdle, true) {
		return nil
	}
	if r.deps.power != nil && !r.deps.power.GetLimits().AllowDaydreaming {
		return nil
	}
	if r.deps.freezer != nil && r.deps.freezer.Frozen() {
		return nil
	}
	active, err := r.deps.jobs.Active(ctx)
	if err != nil {
		return fmt.Errorf("daydream: listing active jobs: %w", err)
	}
	if len(active) > 0 {
		return nil // real work is queued; §17.2 "yield immediately"
	}

	// Both actions share one §17.2 wall-time budget.
	timeout := time.Duration(r.deps.cfg.Power.DaydreamMaxWallTimeMinutes) * time.Minute
	if timeout <= 0 {
		timeout = defaultDaydreamWallTime
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := r.consolidate(ctx); err != nil {
		return err
	}
	return r.exploreRepositories(ctx)
}

// consolidate runs one bounded pass over both §10.3 sources and audits it.
func (r *daydreamRunner) consolidate(ctx context.Context) error {
	sources := []struct {
		name   string
		source mce.MemorySource
	}{
		{"deterministic", &terminalJobSource{jobs: r.deps.jobs, events: r.deps.events, compactions: r.deps.compactions}},
		{"semantic", &acceptedDocSource{artifacts: r.deps.artifacts, compactions: r.deps.compactions}},
	}
	var total mce.ConsolidationResult
	for _, s := range sources {
		res, err := mce.NewConsolidator(r.deps.compactions, r.deps.compactor, s.source).
			RunOnce(ctx, defaultConsolidationBatch)
		if err != nil {
			return fmt.Errorf("daydream %s: %w", s.name, err)
		}
		total.Processed += res.Processed
		total.Compacted += res.Compacted
		total.Cached += res.Cached
		total.Skipped += res.Skipped
		total.TooLarge += res.TooLarge
		total.Failed += res.Failed
		total.BytesBefore += res.BytesBefore
		total.BytesAfter += res.BytesAfter
	}
	if total.Processed == 0 {
		return nil // caught up; no audit noise
	}
	return r.audit(ctx, "memory_consolidation", map[string]any{
		"processed":    total.Processed,
		"compacted":    total.Compacted,
		"cached":       total.Cached,
		"skipped":      total.Skipped,
		"too_large":    total.TooLarge,
		"failed":       total.Failed,
		"bytes_before": total.BytesBefore,
		"bytes_after":  total.BytesAfter,
	})
}

// exploreRepositories is the §17.1 Repository Exploration action (M5-T8;
// ADR-0028 §6): one bounded indexing pass per project with a repository, so
// the single-connection database and the model are touched for at most one
// batch before the loop yields.
func (r *daydreamRunner) exploreRepositories(ctx context.Context) error {
	if r.deps.indexer == nil || r.deps.projects == nil {
		return nil
	}
	projects, err := r.deps.projects.WithRepository(ctx, 0)
	if err != nil {
		return fmt.Errorf("daydream: listing repositories: %w", err)
	}
	var indexed, pruned, failed int
	for _, p := range projects {
		res, err := r.deps.indexer.IndexOnce(ctx, p.ID, p.RepositoryPath)
		if err != nil {
			log := r.deps.log
			if log == nil {
				log = slog.Default()
			}
			log.Warn("daydream: repository exploration failed", "project", p.ID, "err", err)
			continue
		}
		indexed += res.Indexed
		pruned += res.Pruned
		failed += res.Failed
	}
	if indexed == 0 && pruned == 0 && failed == 0 {
		return nil // caught up; no audit noise
	}
	return r.audit(ctx, "repository_exploration", map[string]any{
		"projects": len(projects),
		"indexed":  indexed,
		"pruned":   pruned,
		"failed":   failed,
	})
}

// audit appends a per-pass `daydream` event.
func (r *daydreamRunner) audit(ctx context.Context, event string, data map[string]any) error {
	data["event"] = event
	if _, err := r.deps.events.AppendEvent(ctx, store.Event{
		Category: "daydream",
		Data:     data,
	}); err != nil {
		return fmt.Errorf("daydream: audit: %w", err)
	}
	return nil
}

// terminalJobSource yields one deterministic-compaction item per terminal job
// whose event log has not been consolidated (log + episodic, §10.3). The event
// log is never mutated — compaction produces a derived memo (ADR-0025 §4).
type terminalJobSource struct {
	jobs        *job.Repository
	events      *store.Store
	compactions *mce.CompactStore
}

func (s *terminalJobSource) Next(ctx context.Context, limit int) ([]mce.MemoryItem, error) {
	jobs, err := s.jobs.Terminal(ctx, limit*4)
	if err != nil {
		return nil, err
	}
	var out []mce.MemoryItem
	for _, j := range jobs {
		if len(out) >= limit {
			break
		}
		done, err := s.compactions.HasJob(ctx, j.ID, mce.CompactionDeterministic)
		if err != nil {
			return nil, err
		}
		if done {
			continue
		}
		evs, err := s.events.QueryEvents(ctx, store.EventFilter{JobID: j.ID})
		if err != nil {
			return nil, err
		}
		if len(evs) == 0 {
			continue
		}
		content := joinEventLog(evs)
		out = append(out, mce.MemoryItem{
			Content:    []byte(content),
			Profile:    mce.Profile{Type: mce.EpistemicLog, State: mce.TemporalEpisodic},
			Ref:        mce.SourceRef{JobID: j.ID, ProjectID: j.ProjectID, RelPath: j.ID},
			SourceHash: contentHash(content),
		})
	}
	return out, nil
}

// acceptedDocSource yields one semantic-compaction item per accepted
// documentation artifact not yet consolidated (documentation + archival,
// §10.3). The artifact bytes are untouched.
type acceptedDocSource struct {
	artifacts   *artifact.Store
	compactions *mce.CompactStore
}

func (s *acceptedDocSource) Next(ctx context.Context, limit int) ([]mce.MemoryItem, error) {
	arts, err := s.artifacts.ListAccepted(ctx, limit*4)
	if err != nil {
		return nil, err
	}
	var out []mce.MemoryItem
	for _, art := range arts {
		if len(out) >= limit {
			break
		}
		if art.Kind != artifact.KindDocument {
			continue // code is divided, never compacted (§10.3)
		}
		done, err := s.compactions.HasSource(ctx, art.ContentHash, mce.CompactionSemantic)
		if err != nil {
			return nil, err
		}
		if done {
			continue
		}
		content, err := s.artifacts.ReadContent(ctx, art.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, mce.MemoryItem{
			Content:    content,
			Profile:    mce.Profile{Type: mce.EpistemicDocumentation, State: mce.TemporalArchival},
			Ref:        mce.SourceRef{ProjectID: art.ProjectID, JobID: art.JobID, RelPath: art.StoragePath},
			SourceHash: art.ContentHash,
		})
	}
	return out, nil
}

// joinEventLog renders an event slice as compactable text: timestamp, category,
// and the JSON data, one event per line.
func joinEventLog(evs []store.EventRecord) string {
	var b strings.Builder
	for _, e := range evs {
		fmt.Fprintf(&b, "%s %s %s\n", e.TS.Format(time.RFC3339), e.Category, e.DataJSON)
	}
	return b.String()
}

// contentHash is the origin hash a source uses as its already-consolidated
// marker.
func contentHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
