// Power Supervisor: the clock-and-OS half of M7-T1.
//
// Observer is the OS boundary (one read of AC/battery, idle time, sleep).
// Supervisor polls it, resolves a Decision through the pure Decide policy,
// applies it to the PowerManager, holds/releases the OS power assertion, and
// fires pause/resume hooks so the daemon can stop and restart deep work
// without an operator.
//
// The poll loop lives here (no OS calls); production Observers and OSWatchers
// live in cmd/athanor/power_*.go so internal/ stays free of os/exec (Gate G1).
package power

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Observer is the OS boundary: a cheap, non-blocking read of the power and
// idle state. Implementations live in cmd/ (build-tagged per platform).
type Observer interface {
	Observe(ctx context.Context) (Observation, error)
}

// Hooks are the daemon's reactions to power transitions. Nil fields are
// no-ops, so tests can supply only what they assert on.
type Hooks struct {
	// OnPause fires when deep work transitions to paused.
	OnPause func(reason string)
	// OnResume fires when the gate reopens (or on a wake while open).
	OnResume func()
	// Audit, when non-nil, receives one event per applied decision. serve
	// wires it to the append-only log under category "power" (§28.1).
	Audit func(event string, data map[string]any)
}

// SupervisorDeps wires a Supervisor.
type SupervisorDeps struct {
	Manager  *PowerManager
	Observer Observer
	Config   Config
	Interval time.Duration
	Watcher  OSWatcher
	Hooks    Hooks
	Log      *slog.Logger
}

// Supervisor owns the poll loop and the last applied pause/assertion state.
type Supervisor struct {
	deps SupervisorDeps
	log  *slog.Logger

	mu          sync.Mutex
	paused      bool
	asserting   bool
	initialized bool
	lastTick    time.Time

	cancel context.CancelFunc
	done   chan struct{}
}

// NewSupervisor builds a Supervisor with conservative fallbacks.
func NewSupervisor(d SupervisorDeps) *Supervisor {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Watcher == nil {
		d.Watcher = &NoopWatcher{}
	}
	if d.Interval <= 0 {
		d.Interval = 30 * time.Second
	}
	return &Supervisor{deps: d, log: d.Log}
}

// Start runs one immediate Tick then polls every Interval until ctx is
// cancelled. Close waits for the loop to exit.
func (s *Supervisor) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		s.Tick(ctx)
		t := time.NewTicker(s.deps.Interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.Tick(ctx)
			}
		}
	}()
}

// Close stops the loop and waits for it to exit.
func (s *Supervisor) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.done != nil {
		<-s.done
	}
}

// Tick performs one observe→decide→apply cycle. It is exported so tests can
// drive the supervisor deterministically without timers.
func (s *Supervisor) Tick(ctx context.Context) {
	if s.deps.Manager == nil || s.deps.Observer == nil {
		return
	}
	now := time.Now()
	obs, err := s.deps.Observer.Observe(ctx)
	if err != nil {
		s.log.Warn("power: observe failed", "err", err)
		return
	}

	s.mu.Lock()
	// A poll gap far larger than the interval means the process was almost
	// certainly suspended: surface it as a wake event (a momentary state,
	// not a persistent one).
	if !s.lastTick.IsZero() && now.Sub(s.lastTick) > 3*s.deps.Interval {
		obs.JustWoke = true
	}
	s.lastTick = now
	s.mu.Unlock()

	if obs.Now.IsZero() {
		obs.Now = now
	}
	s.apply(Decide(obs, s.deps.Config), obs)
}

// apply installs a Decision and fires the transition hooks. It is the only
// writer of the supervisor's pause/assertion state.
func (s *Supervisor) apply(d Decision, obs Observation) {
	s.deps.Manager.ApplyDecision(d)

	s.mu.Lock()
	wasPaused := s.paused
	wasAsserting := s.asserting
	first := !s.initialized
	s.paused = d.Pause
	s.asserting = d.Assertion
	s.initialized = true
	s.mu.Unlock()

	switch {
	case d.Assertion && !wasAsserting:
		if err := s.deps.Watcher.AcquirePowerAssertion(); err != nil {
			s.log.Warn("power: acquire assertion failed", "err", err)
		}
	case !d.Assertion && wasAsserting:
		if err := s.deps.Watcher.ReleasePowerAssertion(); err != nil {
			s.log.Warn("power: release assertion failed", "err", err)
		}
	}

	if d.Pause && (!wasPaused || first) && s.deps.Hooks.OnPause != nil {
		s.deps.Hooks.OnPause(d.Reason)
	}
	// Resume fires when the gate reopens, or on a wake while the gate was
	// already open (so a job interrupted by sleep is re-driven).
	woke := obs.JustWoke && s.deps.Config.ResumeOnWake && !d.Pause
	if ((!d.Pause && wasPaused) || woke) && s.deps.Hooks.OnResume != nil {
		s.deps.Hooks.OnResume()
	}

	if s.deps.Hooks.Audit != nil {
		s.deps.Hooks.Audit("power_decision", map[string]any{
			"profile":         string(d.Profile),
			"pause":           d.Pause,
			"assertion":       d.Assertion,
			"on_ac":           obs.OnAC,
			"battery_percent": obs.BatteryPercent,
			"idle_seconds":    int64(obs.IdleFor / time.Second),
			"reason":          d.Reason,
			"woke":            obs.JustWoke,
		})
	}
}
