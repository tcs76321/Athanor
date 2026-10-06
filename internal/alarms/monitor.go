package alarms

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/tcs76321/athanor/internal/store"
)

// Loader builds a detector Snapshot. Production uses StoreLoader; tests inject
// a fixed snapshot.
type Loader interface {
	Load(ctx context.Context, now time.Time) (Snapshot, error)
}

// Monitor polls a Loader, runs Detect, and raises the resulting alarms.
type Monitor struct {
	svc      *Service
	loader   Loader
	th       Thresholds
	interval time.Duration
	log      *slog.Logger

	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
}

// NewMonitor builds a Monitor. A non-positive interval defaults to one minute.
func NewMonitor(svc *Service, loader Loader, th Thresholds, interval time.Duration, log *slog.Logger) *Monitor {
	if interval <= 0 {
		interval = time.Minute
	}
	if log == nil {
		log = slog.Default()
	}
	return &Monitor{svc: svc, loader: loader, th: th, interval: interval, log: log}
}

// Start runs one immediate Tick then polls until ctx is cancelled.
func (m *Monitor) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.done = make(chan struct{})
	go func() {
		defer close(m.done)
		m.Tick(ctx)
		t := time.NewTicker(m.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m.Tick(ctx)
			}
		}
	}()
}

// Close stops the loop.
func (m *Monitor) Close() {
	if m.cancel != nil {
		m.cancel()
	}
	if m.done != nil {
		<-m.done
	}
}

// Tick performs one load → detect → raise cycle. Exported for deterministic
// tests.
func (m *Monitor) Tick(ctx context.Context) {
	if m.svc == nil || m.loader == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	snap, err := m.loader.Load(ctx, now)
	if err != nil {
		m.log.Warn("alarms: snapshot load failed", "err", err)
		return
	}
	for _, a := range Detect(snap, m.th, now) {
		if _, err := m.svc.Raise(ctx, a); err != nil {
			m.log.Warn("alarms: raise failed", "category", a.Category, "err", err)
		}
	}
}

// StoreLoader builds a Snapshot from SQLite. Resource pressure is left at 0
// (no OS probe here); a future sampler can populate it through Snapshot.
type StoreLoader struct {
	store *store.Store
	th    Thresholds
}

// NewStoreLoader returns a loader over s.
func NewStoreLoader(s *store.Store, th Thresholds) *StoreLoader {
	return &StoreLoader{store: s, th: th}
}

// Load gathers the detector inputs.
func (l *StoreLoader) Load(ctx context.Context, now time.Time) (Snapshot, error) {
	snap := Snapshot{
		JobTokens:   map[string]int{},
		LoopRepeats: map[string]int{},
		EventFlags:  map[string]int{},
	}

	// Recent outcomes (most-recent-first).
	if n := l.th.QualityWindow; n > 0 {
		rows, err := l.store.DB().QueryContext(ctx,
			`SELECT result FROM strategy_outcomes ORDER BY created_at DESC LIMIT ?`, n)
		if err != nil {
			return Snapshot{}, err
		}
		for rows.Next() {
			var r string
			if err := rows.Scan(&r); err != nil {
				_ = rows.Close()
				return Snapshot{}, err
			}
			snap.RecentResults = append(snap.RecentResults, r)
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return Snapshot{}, err
		}
	}

	// Active jobs and their last progress (last event, else updated_at).
	rows, err := l.store.DB().QueryContext(ctx,
		`SELECT j.id, j.project_id, j.state,
		        COALESCE((SELECT MAX(ts) FROM events e WHERE e.job_id = j.id), j.updated_at)
		 FROM jobs j WHERE j.state NOT IN ('completed','failed','cancelled')`)
	if err != nil {
		return Snapshot{}, err
	}
	for rows.Next() {
		var id, project, state, last string
		if err := rows.Scan(&id, &project, &state, &last); err != nil {
			_ = rows.Close()
			return Snapshot{}, err
		}
		act := JobActivity{ID: id, ProjectID: project, State: state}
		if t, err := time.Parse(time.RFC3339, last); err == nil {
			act.LastProgress = t
		}
		snap.ActiveJobs = append(snap.ActiveJobs, act)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return Snapshot{}, err
	}

	// Repeated identical successful tool calls.
	if l.th.LoopRepeats > 0 {
		rows, err := l.store.DB().QueryContext(ctx,
			`SELECT job_id, tool, request_json, COUNT(*) FROM actions
			 WHERE status='succeeded'
			 GROUP BY job_id, tool, request_json HAVING COUNT(*) >= ?`, l.th.LoopRepeats)
		if err != nil {
			return Snapshot{}, err
		}
		for rows.Next() {
			var jobID, tool, req string
			var n int
			if err := rows.Scan(&jobID, &tool, &req, &n); err != nil {
				_ = rows.Close()
				return Snapshot{}, err
			}
			snap.LoopRepeats[jobID+"|"+tool+"|"+req] = n
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return Snapshot{}, err
		}
	}

	// Per-active-job token spend.
	rows, err = l.store.DB().QueryContext(ctx,
		`SELECT o.job_id, o.token_cost FROM strategy_outcomes o
		 JOIN jobs j ON j.id = o.job_id
		 WHERE j.state NOT IN ('completed','failed','cancelled')`)
	if err != nil {
		return Snapshot{}, err
	}
	for rows.Next() {
		var jobID string
		var cost int
		if err := rows.Scan(&jobID, &cost); err != nil {
			_ = rows.Close()
			return Snapshot{}, err
		}
		snap.JobTokens[jobID] += cost
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return Snapshot{}, err
	}

	// Security events in the window.
	if l.th.SecurityWindowMins > 0 {
		since := now.Add(-time.Duration(l.th.SecurityWindowMins) * time.Minute).UTC().Format(time.RFC3339)
		var n int
		if err := l.store.DB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM events
			 WHERE category IN ('airlock','network') AND ts >= ?
			 AND (data_json LIKE '%rejected%' OR data_json LIKE '%denied%')`, since).Scan(&n); err != nil {
			return Snapshot{}, err
		}
		snap.SecurityEvents = n
	}

	// Event-flag categories.
	rows, err = l.store.DB().QueryContext(ctx,
		`SELECT data_json FROM events WHERE category='jobs' AND (
		   data_json LIKE '%hallucinated_path%' OR
		   data_json LIKE '%self_modification_attempt%' OR
		   data_json LIKE '%drift_attempt%')`)
	if err != nil {
		return Snapshot{}, err
	}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			_ = rows.Close()
			return Snapshot{}, err
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			continue
		}
		if ev, ok := data["event"].(string); ok {
			snap.EventFlags[ev]++
		}
	}
	_ = rows.Close()
	return snap, rows.Err()
}
