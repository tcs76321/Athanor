package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/retention"
	"github.com/tcs76321/athanor/internal/store"
)

// retentionScheduler runs ADR-0127 once per day at retention.run_at. It is a
// small local cron matcher (the backup scheduler owns the general cron parser;
// retention needs exactly one daily wall-clock time).
type retentionScheduler struct {
	st       *store.Store
	cfg      *config.Config
	stateDir string
	log      *slog.Logger

	mu      sync.Mutex
	lastDay string
	cancel  context.CancelFunc
	done    chan struct{}
}

// startRetention returns nil when retention is disabled.
func startRetention(ctx context.Context, st *store.Store, cfg *config.Config, stateDir string) *retentionScheduler {
	if st == nil || cfg == nil || !cfg.Retention.RetentionEnabled() {
		return nil
	}
	s := &retentionScheduler{st: st, cfg: cfg, stateDir: stateDir, log: slog.Default()}
	s.Start(ctx)
	return s
}

func (s *retentionScheduler) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		s.Tick(ctx, time.Now())
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

func (s *retentionScheduler) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.done != nil {
		<-s.done
	}
}

// Tick runs when the local clock matches run_at and this day has not run.
func (s *retentionScheduler) Tick(ctx context.Context, now time.Time) {
	want, err := time.ParseInLocation("15:04", s.cfg.Retention.RunAt, now.Location())
	if err != nil {
		return
	}
	if now.Hour() != want.Hour() || now.Minute() != want.Minute() {
		return
	}
	day := now.Format("2006-01-02")
	s.mu.Lock()
	if s.lastDay == day {
		s.mu.Unlock()
		return
	}
	s.lastDay = day
	s.mu.Unlock()
	if _, err := s.RunNow(ctx); err != nil {
		s.log.Error("retention: scheduled run failed", "err", err)
	}
}

// RunNow archives events, prunes auxiliary rows and old archives, optionally
// vacuums, and audits one retention event.
func (s *retentionScheduler) RunNow(ctx context.Context) (retention.Result, error) {
	r := s.cfg.Retention
	archiveDir := r.ArchiveDir
	if archiveDir == "" {
		archiveDir = filepath.Join(s.stateDir, "archive", "events")
	}
	now := time.Now().UTC()
	var res retention.Result
	var err error
	if r.EventsDays > 0 {
		res, err = retention.ArchiveEvents(ctx, s.st, now.AddDate(0, 0, -r.EventsDays), retention.Options{
			ArchiveDir: archiveDir,
			Batch:      r.Batch,
		})
		if err != nil {
			return res, err
		}
	}
	pruned, err := retention.PruneAux(ctx, s.st,
		cutoff(now, r.DaydreamDays), cutoff(now, r.BudgetDays), cutoff(now, r.AlarmsDays))
	if err != nil {
		return res, err
	}
	removed, err := retention.PruneArchives(ctx, s.st, archiveDir, r.ArchiveKeepDays, r.ArchiveMaxBytes)
	if err != nil {
		return res, err
	}
	if r.Vacuum && (res.Archived > 0 || pruned.Daydream+pruned.BudgetGrants+pruned.BudgetWindows+pruned.Alarms > 0) {
		if _, err := s.st.DB().ExecContext(ctx, `VACUUM`); err != nil {
			s.log.Warn("retention: vacuum failed", "err", err)
		}
	}
	if _, err := s.st.AppendEvent(ctx, store.Event{
		Category: "retention",
		Data: map[string]any{
			"event":            "retention_run",
			"archived":         res.Archived,
			"archive_files":    res.Files,
			"archive_bytes":    res.Bytes,
			"archives_removed": removed,
			"daydream":         pruned.Daydream,
			"budget_grants":    pruned.BudgetGrants,
			"budget_windows":   pruned.BudgetWindows,
			"alarms":           pruned.Alarms,
		},
	}); err != nil {
		s.log.Warn("retention: audit", "err", err)
	}
	s.log.Info("retention: pass complete",
		"archived", res.Archived, "files", res.Files,
		"archives_removed", removed, "daydream", pruned.Daydream,
		"budget_grants", pruned.BudgetGrants, "alarms", pruned.Alarms)
	return res, nil
}

func cutoff(now time.Time, days int) time.Time {
	if days <= 0 {
		return time.Time{}
	}
	return now.AddDate(0, 0, -days)
}
