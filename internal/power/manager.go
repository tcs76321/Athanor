// Package power provides resource management for the Athanor daemon,
// throttling resource usage based on user activity state
// (ARCHITECTURE §24: autonomous background work when the user is idle
// and AC-powered; deferential throttling while the user is active).
//
// The profile abstraction is intentionally separate from the battery/
// AC policy in §24 — this package answers "how much background work is
// allowed right now?", which the job scheduler consults before starting
// jobs or Daydreaming work.
package power

import (
	"log/slog"
	"sync"
)

// Profile represents the power management profile.
type Profile string

const (
	// ProfileAutonomous - High resource utilization.
	// Multiple concurrent jobs allowed; Daydreaming (memory
	// consolidation/indexing, §17) is ACTIVE.
	ProfileAutonomous Profile = "autonomous"

	// ProfileInteractive - Deferential resource utilization.
	// Max 1 concurrent job. Daydreaming is PAUSED. CPU quota is reduced.
	ProfileInteractive Profile = "interactive"
)

// Limits defines the resource limits for a given profile.
type Limits struct {
	MaxConcurrentJobs int     // Maximum number of concurrent jobs allowed
	AllowDaydreaming  bool    // Whether Daydreaming (§17) is enabled
	CPUQuota          float64 // CPU quota as fraction of total (0.0-1.0)
}

// OSWatcher defines the interface for OS-level power assertions.
// This is a stub for future CGO/D-Bus integration.
type OSWatcher interface {
	// AcquirePowerAssertion requests the OS to prevent sleep/idle.
	AcquirePowerAssertion() error
	// ReleasePowerAssertion releases the OS power assertion.
	ReleasePowerAssertion() error
}

// NoopWatcher is a no-op implementation of OSWatcher for testing
// and environments where OS integration is not available.
type NoopWatcher struct{}

// AcquirePowerAssertion logs the request and returns nil.
func (n *NoopWatcher) AcquirePowerAssertion() error {
	slog.Debug("NoopWatcher: AcquirePowerAssertion called")
	return nil
}

// ReleasePowerAssertion logs the request and returns nil.
func (n *NoopWatcher) ReleasePowerAssertion() error {
	slog.Debug("NoopWatcher: ReleasePowerAssertion called")
	return nil
}

// PowerManager manages power profiles and resource limits.
type PowerManager struct {
	mu             sync.RWMutex
	watcher        OSWatcher
	currentProfile Profile
	limits         Limits
	// paused is the §24 "deep work pauses" signal (on battery without an
	// override, or while the system sleeps). It is distinct from the kill
	// switch: it is transient and does not require an operator reason to
	// clear.
	paused    bool
	observers []func(Limits)
}

// NewPowerManager creates a new PowerManager with the given OSWatcher.
// If watcher is nil, a NoopWatcher is used.
func NewPowerManager(watcher OSWatcher) *PowerManager {
	if watcher == nil {
		watcher = &NoopWatcher{}
	}
	pm := &PowerManager{
		watcher: watcher,
	}
	// Default to interactive profile (safe default)
	pm.setProfileInternal(ProfileInteractive)
	return pm
}

// profileLimits returns the Limits for a given profile. Unknown profiles
// resolve to the conservative interactive-equivalent limits — the package
// fails toward less background work, never more (§24 spirit).
func profileLimits(p Profile) Limits {
	switch p {
	case ProfileAutonomous:
		return Limits{
			MaxConcurrentJobs: 4,
			AllowDaydreaming:  true,
			CPUQuota:          1.0,
		}
	case ProfileInteractive:
		return Limits{
			MaxConcurrentJobs: 1,
			AllowDaydreaming:  false,
			CPUQuota:          0.3,
		}
	default:
		// Safe fallback: conservative limits for unknown profiles.
		return Limits{
			MaxConcurrentJobs: 1,
			AllowDaydreaming:  false,
			CPUQuota:          0.3,
		}
	}
}

// setProfileInternal updates the profile and limits without locking.
// Caller must hold the write lock.
func (pm *PowerManager) setProfileInternal(p Profile) {
	pm.currentProfile = p
	pm.limits = profileLimits(p)
	slog.Info("PowerManager profile changed", "profile", p, "limits", pm.limits)
}

// GetLimits returns the current resource limits.
// Safe for concurrent use.
func (pm *PowerManager) GetLimits() Limits {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.limits
}

// SetProfile changes the power profile.
// Safe for concurrent use.
func (pm *PowerManager) SetProfile(p Profile) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if pm.currentProfile == p {
		slog.Debug("PowerManager profile unchanged", "profile", p)
		return
	}
	pm.setProfileInternal(p)
	pm.notifyObservers()
}

// ToggleAutonomousMode enables or disables autonomous mode.
// This is a convenience method for the Web UI to manually trigger state changes.
// Safe for concurrent use.
func (pm *PowerManager) ToggleAutonomousMode(enabled bool) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	var newProfile Profile
	if enabled {
		newProfile = ProfileAutonomous
	} else {
		newProfile = ProfileInteractive
	}

	if pm.currentProfile == newProfile {
		slog.Debug("PowerManager autonomous mode unchanged", "enabled", enabled)
		return
	}

	pm.setProfileInternal(newProfile)
	pm.notifyObservers()
}

// CurrentProfile returns the current power profile.
// Safe for concurrent use.
func (pm *PowerManager) CurrentProfile() Profile {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.currentProfile
}

// ApplyDecision installs a §24 Decision from the Supervisor: the profile
// (which selects the concurrency cap and daydreaming allowance) and the pause
// flag. This is the automatic path; ToggleAutonomousMode remains the manual
// UI override. Observe gets notified only on an actual change.
func (pm *PowerManager) ApplyDecision(d Decision) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if pm.currentProfile == d.Profile && pm.paused == d.Pause {
		return
	}
	pm.currentProfile = d.Profile
	pm.limits = profileLimits(d.Profile)
	pm.paused = d.Pause
	slog.Info("PowerManager decision applied", "profile", d.Profile, "pause", d.Pause, "reason", d.Reason)
	pm.notifyObservers()
}

// Paused reports whether the §24 policy has paused deep work (battery below
// the threshold without an override, or system sleep). It satisfies the
// engine's power PauseGate.
func (pm *PowerManager) Paused() bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.paused
}

// MaxConcurrentJobs returns the current cap on concurrent jobs, sourced
// from the active profile. Callers (the engine) consult this on every
// enqueue so profile changes take effect immediately.
func (pm *PowerManager) MaxConcurrentJobs() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	n := pm.limits.MaxConcurrentJobs
	if n < 1 {
		return 1
	}
	return n
}

// OnProfileChange registers an observer that fires after every profile
// change with the new limits. Observers run synchronously under the
// write lock; they should be quick. Safe to call from any goroutine.
// M1-T8.4 wires the engine here; M6 will add the real OS watcher that
// triggers profile changes.
func (pm *PowerManager) OnProfileChange(fn func(Limits)) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.observers = append(pm.observers, fn)
}

// notifyObservers fires every registered observer with the new limits.
// Caller must hold pm.mu (write lock).
func (pm *PowerManager) notifyObservers() {
	for _, fn := range pm.observers {
		fn(pm.limits)
	}
}
