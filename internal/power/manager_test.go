package power

import (
	"sync"
	"testing"
)

// safeLimits is the conservative fallback every unknown profile must
// resolve to (fail toward less background work, never more).
func safeLimits() Limits {
	return Limits{MaxConcurrentJobs: 1, AllowDaydreaming: false, CPUQuota: 0.3}
}

func TestNewDefaultsToInteractive(t *testing.T) {
	pm := NewPowerManager(nil)
	if got := pm.CurrentProfile(); got != ProfileInteractive {
		t.Errorf("default profile = %q, want interactive (safe default)", got)
	}
	if got := pm.GetLimits(); got != (Limits{MaxConcurrentJobs: 1, AllowDaydreaming: false, CPUQuota: 0.3}) {
		t.Errorf("default limits = %+v, want interactive limits", got)
	}
}

func TestProfileLimits(t *testing.T) {
	cases := []struct {
		profile Profile
		want    Limits
	}{
		{ProfileAutonomous, Limits{MaxConcurrentJobs: 1, AllowDaydreaming: true, CPUQuota: 1.0}},
		{ProfileInteractive, Limits{MaxConcurrentJobs: 1, AllowDaydreaming: false, CPUQuota: 0.3}},
		{Profile("nonsense"), safeLimits()},
		{Profile(""), safeLimits()},
	}
	for _, tc := range cases {
		if got := profileLimits(tc.profile); got != tc.want {
			t.Errorf("profileLimits(%q) = %+v, want %+v", tc.profile, got, tc.want)
		}
	}
}

// TestSingleSlotInvariant pins ADR-0072: no profile may raise job concurrency
// above 1, however idle or AC-powered the machine is.
func TestSingleSlotInvariant(t *testing.T) {
	for _, p := range []Profile{ProfileAutonomous, ProfileInteractive, Profile("nonsense"), Profile("")} {
		if got := profileLimits(p).MaxConcurrentJobs; got != 1 {
			t.Errorf("profileLimits(%q).MaxConcurrentJobs = %d, want 1 (ADR-0072)", p, got)
		}
	}
	pm := NewPowerManager(nil)
	pm.SetProfile(ProfileAutonomous)
	if got := pm.MaxConcurrentJobs(); got != 1 {
		t.Errorf("autonomous MaxConcurrentJobs = %d, want 1", got)
	}
}

func TestSetProfileUpdatesLimits(t *testing.T) {
	pm := NewPowerManager(nil)
	pm.SetProfile(ProfileAutonomous)
	if pm.CurrentProfile() != ProfileAutonomous {
		t.Fatalf("profile = %q, want autonomous", pm.CurrentProfile())
	}
	want := Limits{MaxConcurrentJobs: 1, AllowDaydreaming: true, CPUQuota: 1.0}
	if got := pm.GetLimits(); got != want {
		t.Errorf("limits after SetProfile(autonomous) = %+v, want %+v", got, want)
	}

	// Setting the same profile again is a no-op, not an error.
	pm.SetProfile(ProfileAutonomous)
	if pm.CurrentProfile() != ProfileAutonomous {
		t.Errorf("profile after idempotent SetProfile = %q, want autonomous", pm.CurrentProfile())
	}

	// Unknown profiles get conservative limits rather than panicking or
	// silently granting autonomous-level resources.
	pm.SetProfile(Profile("mystery"))
	if got := pm.GetLimits(); got != safeLimits() {
		t.Errorf("limits for unknown profile = %+v, want safe fallback %+v", got, safeLimits())
	}
}

func TestToggleAutonomousMode(t *testing.T) {
	pm := NewPowerManager(nil) // starts interactive

	pm.ToggleAutonomousMode(true)
	if pm.CurrentProfile() != ProfileAutonomous {
		t.Errorf("after Toggle(true): profile = %q, want autonomous", pm.CurrentProfile())
	}
	if !pm.GetLimits().AllowDaydreaming {
		t.Error("autonomous limits must allow Daydreaming")
	}

	pm.ToggleAutonomousMode(false)
	if pm.CurrentProfile() != ProfileInteractive {
		t.Errorf("after Toggle(false): profile = %q, want interactive", pm.CurrentProfile())
	}
	if pm.GetLimits().AllowDaydreaming {
		t.Error("interactive limits must pause Daydreaming")
	}

	// Re-toggling to the same state is a no-op.
	pm.ToggleAutonomousMode(false)
	if pm.CurrentProfile() != ProfileInteractive {
		t.Errorf("after idempotent Toggle(false): profile = %q, want interactive", pm.CurrentProfile())
	}
}

func TestNilWatcherUsesNoop(t *testing.T) {
	pm := NewPowerManager(nil)
	if pm.watcher == nil {
		t.Fatal("NewPowerManager(nil) left watcher nil; want NoopWatcher")
	}
	// The watcher interface must be usable (e.g. for future sleep/wake
	// integration) without erroring in environments without OS support.
	if err := pm.watcher.AcquirePowerAssertion(); err != nil {
		t.Errorf("NoopWatcher.AcquirePowerAssertion() = %v, want nil", err)
	}
	if err := pm.watcher.ReleasePowerAssertion(); err != nil {
		t.Errorf("NoopWatcher.ReleasePowerAssertion() = %v, want nil", err)
	}
}

func TestNoopWatcherIsNoop(t *testing.T) {
	var n NoopWatcher
	if err := n.AcquirePowerAssertion(); err != nil {
		t.Errorf("AcquirePowerAssertion() = %v, want nil", err)
	}
	if err := n.ReleasePowerAssertion(); err != nil {
		t.Errorf("ReleasePowerAssertion() = %v, want nil", err)
	}
}

// TestConcurrentAccess exercises simultaneous readers and writers under
// the race detector: profile flips must never expose a torn Limits value.
func TestConcurrentAccess(t *testing.T) {
	pm := NewPowerManager(nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if i%2 == 0 {
					pm.ToggleAutonomousMode(j%2 == 0)
				} else {
					lim := pm.GetLimits()
					if lim.MaxConcurrentJobs < 1 || lim.CPUQuota <= 0 || lim.CPUQuota > 1 {
						t.Errorf("torn limits observed: %+v", lim)
						return
					}
					_ = pm.CurrentProfile()
				}
			}
		}(i)
	}
	wg.Wait()
}
