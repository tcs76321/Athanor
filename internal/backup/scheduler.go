package backup

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	"github.com/tcs76321/athanor/internal/store"
)

// SchedulerDeps wires a Scheduler.
type SchedulerDeps struct {
	DB       *sql.DB
	Dir      string
	Version  func() int
	Schedule string
	Keep     int
	Events   *store.Store
	Log      *slog.Logger
}

// Scheduler runs §23.4 snapshots on a cron schedule and prunes retention.
type Scheduler struct {
	db      *sql.DB
	dir     string
	version func() int
	sched   Schedule
	keep    int
	events  *store.Store
	log     *slog.Logger

	mu      sync.Mutex
	lastRun time.Time
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewScheduler parses the cron spec and returns a scheduler.
func NewScheduler(d SchedulerDeps) (*Scheduler, error) {
	sched, err := ParseCron(d.Schedule)
	if err != nil {
		return nil, err
	}
	if d.Version == nil {
		d.Version = func() int { return 0 }
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Scheduler{
		db: d.DB, dir: d.Dir, version: d.Version, sched: sched,
		keep: d.Keep, events: d.Events, log: d.Log,
	}, nil
}

// Start runs a check every 30 seconds until ctx is cancelled.
func (s *Scheduler) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				s.Tick(ctx, now)
			}
		}
	}()
}

// Close stops the loop.
func (s *Scheduler) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.done != nil {
		<-s.done
	}
}

// Tick runs the backup when now matches the schedule and this minute has not
// already run. Exported for deterministic tests.
func (s *Scheduler) Tick(ctx context.Context, now time.Time) {
	if !s.sched.Matches(now) {
		return
	}
	s.mu.Lock()
	already := !s.lastRun.IsZero() && now.Sub(s.lastRun) < time.Minute
	s.mu.Unlock()
	if already {
		return
	}
	// Mark the attempt before running so a failure does not re-fire on the
	// next tick within the same matching minute (the schedule fires once per
	// minute; a partial/failed snapshot should not double-run).
	s.mu.Lock()
	s.lastRun = now
	s.mu.Unlock()
	if _, err := s.RunNow(ctx); err != nil {
		s.log.Error("backup: scheduled run failed (will not retry this minute)", "err", err)
		return
	}
}

// RunNow forces a snapshot + prune regardless of schedule (the CLI path).
func (s *Scheduler) RunNow(ctx context.Context) (string, error) {
	path, err := Snapshot(s.db, s.dir, s.version())
	if err != nil {
		return "", err
	}
	removed, err := Prune(s.dir, s.keep)
	if err != nil {
		s.log.Warn("backup: prune failed", "err", err)
	}
	if s.events != nil {
		_, _ = s.events.AppendEvent(ctx, store.Event{
			Category: "backup",
			Data: map[string]any{
				"event": "backup_created", "path": path, "pruned": len(removed),
			},
		})
	}
	return path, nil
}
