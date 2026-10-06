package power

import (
	"context"
	"sync"
	"testing"
	"time"
)

// seqObserver returns a fixed sequence of observations, repeating the last.
type seqObserver struct {
	mu  sync.Mutex
	obs []Observation
	i   int
}

func (o *seqObserver) Observe(context.Context) (Observation, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.obs) == 0 {
		return Observation{}, nil
	}
	if o.i >= len(o.obs) {
		return o.obs[len(o.obs)-1], nil
	}
	v := o.obs[o.i]
	o.i++
	return v, nil
}

// countingWatcher records assertion acquire/release calls.
type countingWatcher struct {
	acquires int
	releases int
}

func (w *countingWatcher) AcquirePowerAssertion() error { w.acquires++; return nil }
func (w *countingWatcher) ReleasePowerAssertion() error { w.releases++; return nil }

func newTestSupervisor(obs Observer, cfg Config, w OSWatcher) (*Supervisor, *PowerManager, *[]string) {
	pm := NewPowerManager(nil)
	events := &[]string{}
	s := NewSupervisor(SupervisorDeps{
		Manager:  pm,
		Observer: obs,
		Config:   cfg,
		Interval: time.Minute,
		Watcher:  w,
		Hooks: Hooks{
			OnPause:  func(string) { *events = append(*events, "pause") },
			OnResume: func() { *events = append(*events, "resume") },
		},
	})
	return s, pm, events
}

func TestSupervisorPauseThenResume(t *testing.T) {
	cfg := baseCfg()
	obs := &seqObserver{obs: []Observation{
		{OnAC: true, IdleFor: time.Hour},  // autonomous
		{OnAC: false, BatteryPercent: 80}, // battery, no override -> pause
		{OnAC: false, BatteryPercent: 80}, // still paused -> no second pause event
		{OnAC: true, IdleFor: time.Hour},  // AC -> resume
	}}
	s, pm, events := newTestSupervisor(obs, cfg, &countingWatcher{})
	ctx := context.Background()

	s.Tick(ctx)
	if pm.Paused() || pm.CurrentProfile() != ProfileAutonomous {
		t.Fatalf("tick1: paused=%v profile=%s, want autonomous", pm.Paused(), pm.CurrentProfile())
	}
	s.Tick(ctx)
	if !pm.Paused() {
		t.Fatal("tick2: expected paused on battery")
	}
	s.Tick(ctx)
	if !pm.Paused() {
		t.Fatal("tick3: expected still paused")
	}
	s.Tick(ctx)
	if pm.Paused() {
		t.Fatal("tick4: expected resumed on AC")
	}
	want := []string{"pause", "resume"}
	if len(*events) != len(want) {
		t.Fatalf("events = %v, want exactly one pause and one resume", *events)
	}
	for i := range want {
		if (*events)[i] != want[i] {
			t.Fatalf("events = %v, want %v", *events, want)
		}
	}
}

func TestSupervisorHoldsAssertionWhileAutonomous(t *testing.T) {
	cfg := baseCfg()
	obs := &seqObserver{obs: []Observation{
		{OnAC: true, IdleFor: time.Hour}, // autonomous -> acquire
		{OnAC: true, IdleFor: time.Hour}, // still autonomous -> no churn
		{OnAC: true, IdleFor: 0},         // active -> release
	}}
	w := &countingWatcher{}
	s, _, _ := newTestSupervisor(obs, cfg, w)
	ctx := context.Background()
	s.Tick(ctx)
	s.Tick(ctx)
	if w.acquires != 1 || w.releases != 0 {
		t.Fatalf("after two autonomous ticks: acquires=%d releases=%d, want 1/0", w.acquires, w.releases)
	}
	s.Tick(ctx)
	if w.acquires != 1 || w.releases != 1 {
		t.Fatalf("after active tick: acquires=%d releases=%d, want 1/1", w.acquires, w.releases)
	}
}

func TestSupervisorWakeTriggersResume(t *testing.T) {
	cfg := baseCfg()
	// Always AC + idle: never paused, so only a wake can fire resume.
	obs := &seqObserver{obs: []Observation{{OnAC: true, IdleFor: time.Hour}}}
	s, _, events := newTestSupervisor(obs, cfg, &countingWatcher{})
	ctx := context.Background()
	s.Tick(ctx) // establishes lastTick; no resume
	if len(*events) != 0 {
		t.Fatalf("first tick events = %v, want none", *events)
	}
	// Simulate a long suspension by rewinding lastTick.
	s.mu.Lock()
	s.lastTick = time.Now().Add(-10 * time.Minute)
	s.mu.Unlock()
	s.Tick(ctx)
	if len(*events) != 1 || (*events)[0] != "resume" {
		t.Fatalf("wake events = %v, want one resume", *events)
	}
}

func TestSupervisorObserverErrorIsNonFatal(t *testing.T) {
	s := NewSupervisor(SupervisorDeps{
		Manager:  NewPowerManager(nil),
		Observer: errObserver{},
		Config:   baseCfg(),
		Interval: time.Minute,
	})
	s.Tick(context.Background()) // must not panic
}

type errObserver struct{}

func (errObserver) Observe(context.Context) (Observation, error) {
	return Observation{}, context.DeadlineExceeded
}

func TestDecideSleepingPauseReasonRecorded(t *testing.T) {
	pm := NewPowerManager(nil)
	d := Decide(Observation{Sleeping: true, OnAC: true}, baseCfg())
	pm.ApplyDecision(d)
	if !pm.Paused() {
		t.Fatal("ApplyDecision(sleeping) should pause the manager")
	}
	// Re-applying the same decision is idempotent.
	pm.ApplyDecision(d)
	if !pm.Paused() {
		t.Fatal("manager should remain paused")
	}
}
